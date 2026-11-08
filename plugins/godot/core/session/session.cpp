#include "core/session/session.h"

#include <algorithm>

namespace nipa::session {
namespace {

using json = nlohmann::json;

json root_body(const std::string& root) {
    return {{"root", root}};
}

}  // namespace

Session::Session(SessionOptions options) : options_(std::move(options)) {}

Session::~Session() {
    try {
        stop();
    } catch (...) {
    }
}

void Session::start() {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (worker_.joinable() || stopping_) {
            return;
        }
    }
    worker_ = std::thread([this] { worker_loop(); });
    changes_thread_ = std::thread([this] {
        watch_loop([this](const daemon::Client& client) { watch_changes_body(client); });
    });
    if (options_.watch_activity) {
        activity_thread_ = std::thread([this] {
            watch_loop([this](const daemon::Client& client) { watch_activity_body(client); });
        });
    }
    submit_status();
}

void Session::stop() {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (stopped_) {
            return;
        }
        stopped_ = true;
        stopping_ = true;
    }
    cancel_.cancel();
    task_cv_.notify_all();
    wait_cv_.notify_all();
    cache_cv_.notify_all();

    auto self = std::this_thread::get_id();
    for (std::thread* thread : {&worker_, &changes_thread_, &activity_thread_}) {
        if (thread->joinable() && thread->get_id() != self) {
            thread->join();
        }
    }

    std::deque<Task> leftover;
    {
        std::lock_guard<std::mutex> guard(mu_);
        leftover.swap(tasks_);
        link_ = Link{};
    }
    for (const auto& task : leftover) {
        if (!task.quiet) {
            Event e;
            e.kind = EventKind::OpFinished;
            e.op_id = task.id;
            e.name = task.name;
            e.message = "session stopped";
            events_.push(std::move(e));
        }
    }
}

bool Session::stopping() const {
    std::lock_guard<std::mutex> guard(mu_);
    return stopping_;
}

bool Session::connected() const {
    std::lock_guard<std::mutex> guard(mu_);
    return link_.client != nullptr;
}

std::string Session::version() const {
    std::lock_guard<std::mutex> guard(mu_);
    return version_;
}

status::StatusCache Session::status_cache() const {
    std::lock_guard<std::mutex> guard(mu_);
    return status_;
}

std::vector<daemon::Branch> Session::branches() const {
    std::lock_guard<std::mutex> guard(mu_);
    return branches_;
}

std::vector<daemon::Commit> Session::commits() const {
    std::lock_guard<std::mutex> guard(mu_);
    return commits_;
}

std::vector<std::string> Session::remotes() const {
    std::lock_guard<std::mutex> guard(mu_);
    return remotes_;
}

bool Session::status_blocking(std::chrono::milliseconds timeout) {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (status_.loaded()) {
            return true;
        }
    }
    submit_status();
    std::unique_lock<std::mutex> lock(mu_);
    cache_cv_.wait_for(lock, timeout, [this] { return status_.loaded() || stopping_; });
    return status_.loaded();
}

std::int64_t Session::enqueue(std::string name, bool quiet, std::function<Outcome(std::int64_t)> run) {
    std::lock_guard<std::mutex> guard(mu_);
    if (stopping_) {
        return 0;
    }
    std::int64_t id = next_id_++;
    if (!quiet) {
        Event e;
        e.kind = EventKind::OpQueued;
        e.op_id = id;
        e.name = name;
        events_.push(std::move(e));
    }
    tasks_.push_back(Task{id, std::move(name), quiet, std::move(run)});
    task_cv_.notify_one();
    return id;
}

void Session::worker_loop() {
    while (true) {
        Task task;
        {
            std::unique_lock<std::mutex> lock(mu_);
            task_cv_.wait(lock, [this] { return stopping_ || !tasks_.empty(); });
            if (stopping_) {
                return;
            }
            task = std::move(tasks_.front());
            tasks_.pop_front();
        }
        Outcome outcome;
        try {
            outcome = task.run(task.id);
        } catch (const daemon::CancelledError&) {
            outcome = Outcome{false, "cancelled", {}};
        } catch (const std::exception& e) {
            outcome = Outcome{false, e.what(), {}};
        }
        if (!task.quiet) {
            {
                std::lock_guard<std::mutex> guard(mu_);
                diffs_.clear();
                blobs_.clear();
            }
            cache_cv_.notify_all();
            Event e;
            e.kind = EventKind::OpFinished;
            e.op_id = task.id;
            e.name = task.name;
            e.ok = outcome.ok;
            e.message = outcome.message;
            e.paths = outcome.paths;
            events_.push(std::move(e));
        }
    }
}

Session::Link Session::ensure_connected() {
    std::lock_guard<std::mutex> connect_guard(connect_mu_);
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (stopping_) {
            throw daemon::CancelledError();
        }
        if (link_.client) {
            return link_;
        }
    }
    auto attached = daemon::connect(options_.connect, &cancel_);
    auto watched = attached.client->call("WatchRepo", root_body(options_.root), options_.unary_timeout, &cancel_);
    std::vector<std::string> remotes;
    auto repo = watched.find("repo");
    if (repo != watched.end() && repo->is_object() && !repo->value("url", std::string()).empty()) {
        remotes.push_back(repo->value("url", std::string()));
    }

    Link link;
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (stopping_) {
            throw daemon::CancelledError();
        }
        link.client = attached.client;
        link.generation = ++generation_;
        link_ = link;
        version_ = attached.version;
        remotes_ = remotes;
    }
    Event e;
    e.kind = EventKind::Connected;
    e.message = attached.version;
    events_.push(std::move(e));
    Event remote_event;
    remote_event.kind = EventKind::RemotesChanged;
    events_.push(std::move(remote_event));
    submit_status();
    submit_branches();
    submit_commits();
    return link;
}

void Session::run_on_link(const std::function<void(const daemon::Client&)>& fn) {
    Link link = ensure_connected();
    try {
        fn(*link.client);
    } catch (const daemon::DaemonError& error) {
        if (error.http_status == 503) {
            drop_link(link.generation, error.what());
        }
        throw;
    }
}

void Session::drop_link(std::uint64_t generation, const std::string& reason) {
    bool dropped = false;
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (link_.client && link_.generation == generation) {
            link_ = Link{};
            dropped = true;
        }
    }
    if (dropped) {
        Event e;
        e.kind = EventKind::Disconnected;
        e.message = reason;
        events_.push(std::move(e));
    }
}

void Session::watch_loop(const std::function<void(const daemon::Client&)>& body) {
    auto backoff = options_.reconnect_min;
    while (true) {
        Link link;
        try {
            link = ensure_connected();
        } catch (const daemon::CancelledError&) {
            return;
        } catch (const std::exception& e) {
            if (stopping()) {
                return;
            }
            Event event;
            event.kind = EventKind::Disconnected;
            event.message = e.what();
            events_.push(std::move(event));
            wait_backoff(backoff);
            continue;
        }
        backoff = options_.reconnect_min;
        try {
            body(*link.client);
            drop_link(link.generation, "stream ended");
        } catch (const daemon::CancelledError&) {
            return;
        } catch (const std::exception& e) {
            drop_link(link.generation, e.what());
        }
        wait_backoff(backoff);
    }
}

void Session::wait_backoff(std::chrono::milliseconds& backoff) {
    std::unique_lock<std::mutex> lock(mu_);
    wait_cv_.wait_for(lock, backoff, [this] { return stopping_; });
    backoff = std::min(backoff * 2, options_.reconnect_max);
}

void Session::watch_changes_body(const daemon::Client& client) {
    client.stream(
        "WatchChanges", root_body(options_.root),
        [this](const json&) {
            submit_status();
            return true;
        },
        options_.stream_timeout, &cancel_);
}

void Session::watch_activity_body(const daemon::Client& client) {
    json body = {{"root", options_.root}, {"interval_seconds", options_.activity_interval.count()}};
    client.stream(
        "WatchEvents", body,
        [this](const json& item) {
            if (item.contains("events") && item["events"].is_array()) {
                for (const auto& team : item["events"]) {
                    Event e;
                    e.kind = EventKind::Activity;
                    e.name = daemon::json_string(team, "kind");
                    e.actor = daemon::json_string(team, "actor");
                    e.message = daemon::json_string(team, "message");
                    std::string path = daemon::json_string(team, "path");
                    if (!path.empty()) {
                        e.paths.push_back(path);
                    }
                    events_.push(std::move(e));
                }
            }
            Event summary;
            summary.kind = EventKind::ActivitySummary;
            summary.done = daemon::json_int(item, "incoming");
            summary.total = daemon::json_int(item, "review_requests");
            summary.message = daemon::json_string(item, "error");
            events_.push(std::move(summary));
            return true;
        },
        options_.stream_timeout, &cancel_);
}

void Session::apply_status(const json& reply) {
    auto snapshot = daemon::parse_status(reply);
    std::vector<std::string> changed;
    {
        std::lock_guard<std::mutex> guard(mu_);
        changed = status_.apply(std::move(snapshot));
    }
    cache_cv_.notify_all();
    if (!changed.empty()) {
        Event e;
        e.kind = EventKind::StatusChanged;
        e.paths = changed;
        events_.push(std::move(e));
    }
}

std::int64_t Session::submit_status() {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (status_pending_ || stopping_) {
            return 0;
        }
        status_pending_ = true;
    }
    return enqueue("status", true, [this](std::int64_t) {
        {
            std::lock_guard<std::mutex> guard(mu_);
            status_pending_ = false;
        }
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("Status", root_body(options_.root), options_.unary_timeout, &cancel_);
        });
        apply_status(reply);
        return Outcome{};
    });
}

std::int64_t Session::submit_branches() {
    return enqueue("branches", true, [this](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("ProxyBranchList", {{"root", options_.root}, {"request", {{"limit", 100}}}},
                                options_.unary_timeout, &cancel_);
        });
        auto list = daemon::parse_branches(daemon::proxy_payload(reply));
        {
            std::lock_guard<std::mutex> guard(mu_);
            branches_ = std::move(list);
        }
        Event e;
        e.kind = EventKind::BranchesChanged;
        events_.push(std::move(e));
        return Outcome{};
    });
}

std::int64_t Session::submit_commits() {
    return enqueue("commits", true, [this](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("ProxyCommitLog", {{"root", options_.root}, {"request", {{"limit", 50}}}},
                                options_.unary_timeout, &cancel_);
        });
        auto list = daemon::parse_commits(daemon::proxy_payload(reply));
        {
            std::lock_guard<std::mutex> guard(mu_);
            commits_ = std::move(list);
        }
        Event e;
        e.kind = EventKind::CommitsChanged;
        events_.push(std::move(e));
        return Outcome{};
    });
}

void Session::refresh_after_op(const std::string& name) {
    if (name == "switch") {
        submit_status();
        submit_commits();
        submit_branches();
    } else if (name == "push" || name == "update") {
        submit_status();
        submit_commits();
    } else if (name == "branch-create" || name == "branch-delete") {
        submit_branches();
    }
}

Session::Outcome Session::run_stream_op(std::int64_t id, const std::string& name, const std::string& method,
                                        const json& body) {
    Outcome outcome;
    run_on_link([&](const daemon::Client& client) {
        std::string result;
        std::optional<std::string> failure;
        client.stream(
            method, body,
            [&](const json& item) {
                auto event = daemon::parse_op_event(item);
                switch (event.kind) {
                    case daemon::OpEvent::Kind::Started: {
                        Event e;
                        e.kind = EventKind::OpStarted;
                        e.op_id = id;
                        e.name = name;
                        e.message = event.phase;
                        events_.push(std::move(e));
                        break;
                    }
                    case daemon::OpEvent::Kind::Progress: {
                        Event e;
                        e.kind = EventKind::OpProgress;
                        e.op_id = id;
                        e.name = name;
                        e.message = event.phase;
                        e.done = event.done;
                        e.total = event.total;
                        events_.push(std::move(e));
                        break;
                    }
                    case daemon::OpEvent::Kind::Result:
                        result = event.result_commit_id;
                        break;
                    case daemon::OpEvent::Kind::Failure:
                        failure = event.message;
                        return false;
                    case daemon::OpEvent::Kind::Queued:
                        break;
                }
                return true;
            },
            options_.stream_timeout, &cancel_);
        outcome = failure ? Outcome{false, *failure, {}} : Outcome{true, result, {}};
    });
    refresh_after_op(name);
    return outcome;
}

std::int64_t Session::submit_stage(std::vector<std::string> add, std::vector<std::string> unstage) {
    return enqueue("stage", false, [this, add = std::move(add), unstage = std::move(unstage)](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("Stage", {{"root", options_.root}, {"add", add}, {"unstage", unstage}},
                                options_.unary_timeout, &cancel_);
        });
        apply_status(reply);
        return Outcome{};
    });
}

std::int64_t Session::submit_restore(std::vector<std::string> paths) {
    return enqueue("restore", false, [this, paths = std::move(paths)](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("Restore", {{"root", options_.root}, {"paths", paths}}, options_.unary_timeout,
                                &cancel_);
        });
        apply_status(reply);
        return Outcome{};
    });
}

std::int64_t Session::submit_update() {
    return enqueue("update", false, [this](std::int64_t id) {
        return run_stream_op(id, "update", "Update", {{"root", options_.root}});
    });
}

std::int64_t Session::submit_switch(std::string branch, std::string tag) {
    return enqueue("switch", false, [this, branch = std::move(branch), tag = std::move(tag)](std::int64_t id) {
        return run_stream_op(id, "switch", "Switch", {{"root", options_.root}, {"branch", branch}, {"tag", tag}});
    });
}

std::int64_t Session::submit_push(std::string message) {
    return enqueue("push", false, [this, message = std::move(message)](std::int64_t id) {
        return run_stream_op(id, "push", "Push", {{"root", options_.root}, {"message", message}});
    });
}

std::int64_t Session::submit_branch_create(std::string name, std::string from_branch) {
    return enqueue("branch-create", false,
                   [this, name = std::move(name), from_branch = std::move(from_branch)](std::int64_t) {
                       run_on_link([&](const daemon::Client& client) {
                           client.call("ProxyBranchCreate",
                                       {{"root", options_.root}, {"request", {{"name", name}, {"from_branch", from_branch}}}},
                                       options_.unary_timeout, &cancel_);
                       });
                       refresh_after_op("branch-create");
                       return Outcome{};
                   });
}

std::int64_t Session::submit_branch_delete(std::string name) {
    return enqueue("branch-delete", false, [this, name = std::move(name)](std::int64_t) {
        run_on_link([&](const daemon::Client& client) {
            client.call("ProxyBranchDelete", {{"root", options_.root}, {"request", {{"name", name}}}},
                        options_.unary_timeout, &cancel_);
        });
        refresh_after_op("branch-delete");
        return Outcome{};
    });
}

std::int64_t Session::submit_sign_in(std::string server, std::string username, std::string password,
                                     std::string key_path, std::string passphrase) {
    return enqueue("sign-in", false,
                   [this, server = std::move(server), username = std::move(username), password = std::move(password),
                    key_path = std::move(key_path), passphrase = std::move(passphrase)](std::int64_t) {
                       json account;
                       run_on_link([&](const daemon::Client& client) {
                           account = client.call("AccountStatus", {{"server", server}}, options_.unary_timeout, &cancel_);
                       });
                       if (account.value("signed_in", false)) {
                           return Outcome{true, account.value("email", std::string()), {}};
                       }
                       if (!key_path.empty()) {
                           run_on_link([&](const daemon::Client& client) {
                               client.call("LoginSSH",
                                           {{"server", server}, {"key_path", key_path}, {"passphrase", passphrase}},
                                           options_.unary_timeout, &cancel_);
                           });
                       } else if (!password.empty()) {
                           run_on_link([&](const daemon::Client& client) {
                               client.call("Login", {{"host", server}, {"username", username}, {"password", password}},
                                           options_.unary_timeout, &cancel_);
                           });
                       } else {
                           return Outcome{false, "no credentials for " + server, {}};
                       }
                       return Outcome{true, server, {}};
                   });
}

std::int64_t Session::submit_diff(const std::string& key, json body) {
    std::uint64_t generation = 0;
    {
        std::lock_guard<std::mutex> guard(mu_);
        generation = ++diff_generation_[key];
        diffs_.erase(key);
    }
    return enqueue("diff", true, [this, key, generation, body = std::move(body)](std::int64_t) {
        std::vector<diff::File> files;
        std::string error;
        bool ok = true;
        run_on_link([&](const daemon::Client& client) {
            try {
                client.stream(
                    "DiffFiles", body,
                    [&](const json& item) {
                        if (item.contains("failure")) {
                            error = daemon::json_string(item["failure"], "message");
                            ok = false;
                            return false;
                        }
                        if (item.contains("file")) {
                            files.push_back(daemon::parse_diff_file(item["file"]));
                        }
                        return true;
                    },
                    options_.stream_timeout, &cancel_);
            } catch (const daemon::CancelledError&) {
                throw;
            } catch (const daemon::DaemonError& e) {
                if (e.http_status == 503) {
                    throw;
                }
                ok = false;
                error = e.what();
            }
        });
        if (ok) {
            std::lock_guard<std::mutex> guard(mu_);
            if (diff_generation_[key] == generation) {
                diffs_[key] = files;
            }
        }
        cache_cv_.notify_all();
        Event e;
        e.kind = EventKind::DiffReady;
        e.name = key;
        e.ok = ok;
        e.message = error;
        events_.push(std::move(e));
        return Outcome{};
    });
}

std::int64_t Session::submit_read_blob(const std::string& key, const std::string& path) {
    return enqueue("blob", true, [this, key, path](std::int64_t) {
        std::string content;
        std::string error;
        bool ok = true;
        run_on_link([&](const daemon::Client& client) {
            try {
                client.stream(
                    "ReadBlob", {{"root", options_.root}, {"blob", {{"source", "snapshot"}, {"path", path}}}},
                    [&](const json& item) {
                        if (item.contains("failure")) {
                            error = daemon::json_string(item["failure"], "message");
                            ok = false;
                            return false;
                        }
                        if (item.contains("data")) {
                            content += daemon::base64_decode(daemon::json_string(item, "data"));
                        }
                        return true;
                    },
                    options_.stream_timeout, &cancel_);
            } catch (const daemon::CancelledError&) {
                throw;
            } catch (const daemon::DaemonError& e) {
                if (e.http_status == 503) {
                    throw;
                }
                ok = false;
                error = e.what();
            }
        });
        {
            std::lock_guard<std::mutex> guard(mu_);
            blobs_[key] = BlobEntry{ok, ok ? content : std::string()};
        }
        cache_cv_.notify_all();
        Event e;
        e.kind = EventKind::BlobReady;
        e.name = key;
        e.ok = ok;
        e.message = error;
        events_.push(std::move(e));
        return Outcome{};
    });
}

std::optional<std::string> Session::blob_result(const std::string& key) const {
    std::lock_guard<std::mutex> guard(mu_);
    auto found = blobs_.find(key);
    if (found == blobs_.end() || !found->second.ok) {
        return std::nullopt;
    }
    return found->second.content;
}

bool Session::blob_known(const std::string& key) const {
    std::lock_guard<std::mutex> guard(mu_);
    return blobs_.count(key) > 0;
}

bool Session::blob_blocking(const std::string& key, std::chrono::milliseconds timeout) {
    std::unique_lock<std::mutex> lock(mu_);
    cache_cv_.wait_for(lock, timeout, [this, &key] { return blobs_.count(key) > 0 || stopping_; });
    return blobs_.count(key) > 0;
}

std::optional<std::vector<diff::File>> Session::diff_result(const std::string& key) const {
    std::lock_guard<std::mutex> guard(mu_);
    auto found = diffs_.find(key);
    if (found == diffs_.end()) {
        return std::nullopt;
    }
    return found->second;
}

bool Session::diff_blocking(const std::string& key, std::chrono::milliseconds timeout) {
    std::unique_lock<std::mutex> lock(mu_);
    cache_cv_.wait_for(lock, timeout, [this, &key] { return diffs_.count(key) > 0 || stopping_; });
    return diffs_.count(key) > 0;
}

}  // namespace nipa::session
