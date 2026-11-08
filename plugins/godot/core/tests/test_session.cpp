#include <doctest.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <filesystem>
#include <fstream>
#include <functional>
#include <memory>
#include <mutex>
#include <thread>

#include "core/session/session.h"
#include "fake_daemon.h"

using namespace std::chrono_literals;
using nipa::session::Event;
using nipa::session::EventKind;
using nipa::session::Session;
using nipa::session::SessionOptions;
using nipa::test::FakeDaemon;
using nipa::test::Reply;
using nipa::test::StreamScript;
using nlohmann::json;

namespace {

using Events = std::vector<Event>;
using Predicate = std::function<bool(const Events&)>;

struct Harness {
    std::string dir = nipa::test::make_temp_dir();
    FakeDaemon fake;
    std::unique_ptr<Session> session;
    Events seen;

    Harness() {
        fake.on_unary("Ping", [](const json&) { return Reply{200, {{"version", "0.2.0"}}}; });
        fake.on_unary("WatchRepo", [](const json&) {
            return Reply{200, {{"repo", {{"root", "/work/clone"}, {"url", "host/org/proj"}, {"branch", "main"}}}}};
        });
        fake.on_unary("Status", [](const json&) {
            return Reply{200, {{"branch", "main"}, {"modified", {"a.gd"}}}};
        });
        fake.on_unary("ProxyBranchList", [](const json&) {
            return Reply{200, {{"response", {{"branches", {{{"name", "main"}, {"id", "1"}, {"is_default", true}}}}}}}};
        });
        fake.on_unary("ProxyCommitLog", [](const json&) {
            return Reply{200, {{"response", {{"commits", {{{"commit_id", "c1"}, {"author_name", "Ann"}, {"message", "init"}}}}}}}};
        });
        fake.on_unary("ProxyBranchCreate", [](const json&) { return Reply{200, json::object()}; });
        fake.on_unary("ProxyBranchDelete", [](const json&) { return Reply{200, json::object()}; });
        fake.on_unary("Stage", [](const json&) { return Reply{200, {{"staged", {"a.gd"}}}}; });
        fake.on_unary("Restore", [](const json&) { return Reply{200, {{"modified", {"a.gd"}}}}; });
        fake.on_stream("WatchChanges", silent);
        fake.on_stream("WatchEvents", silent);
        fake.on_stream("Update", finishing({R"({"started":{"phase":"download"}})", R"({"result":{"sync":{"commit_id":"c2"}}})"}));
        fake.on_stream("Switch", finishing({R"({"result":{"sync":{"commit_id":"c3"}}})"}));
        fake.on_stream("Push", finishing({R"({"started":{"phase":"upload"}})",
                                          R"({"progress":{"phase":"upload","objects_done":"1","objects_total":"1"}})",
                                          R"({"result":{"push":{"commit_id":"c9"}}})"}));
        fake.start();
        nipa::test::write_endpoint(dir + "/daemon.json", fake.port(), fake.token(), "0.2.0");
    }

    ~Harness() {
        if (session) {
            session->stop();
        }
        fake.stop();
        std::filesystem::remove_all(dir);
    }

    static FakeDaemon::StreamHandler finishing(std::vector<std::string> lines) {
        return [lines](const json&) {
            StreamScript script;
            script.lines = lines;
            return script;
        };
    }
    static StreamScript silent(const json&) {
        StreamScript script;
        script.endless = true;
        script.delay_ms = 20;
        return script;
    }

    SessionOptions options() const {
        SessionOptions o;
        o.connect.endpoint_path = dir + "/daemon.json";
        o.connect.env = [](const std::string&) { return std::string(); };
        o.connect.os = nipa::daemon::HostOs::Linux;
        o.connect.file_exists = [](const std::string&) { return false; };
        o.connect.spawn = [](const std::string&, const std::string&) { return false; };
        o.connect.start_timeout = 300ms;
        o.connect.poll_interval = 10ms;
        o.connect.ping_timeout = 1s;
        o.root = "/work/clone";
        o.unary_timeout = 2s;
        o.stream_timeout = 10s;
        o.reconnect_min = 20ms;
        o.reconnect_max = 100ms;
        o.activity_interval = std::chrono::seconds(1);
        return o;
    }

    void start() {
        session = std::make_unique<Session>(options());
        session->start();
    }

    // Drains events until `done` holds or the timeout passes.
    bool wait(const Predicate& done, std::chrono::milliseconds timeout = 3s) {
        auto deadline = std::chrono::steady_clock::now() + timeout;
        while (true) {
            for (auto& event : session->events().drain()) {
                seen.push_back(std::move(event));
            }
            if (done(seen)) {
                return true;
            }
            if (std::chrono::steady_clock::now() >= deadline) {
                return false;
            }
            std::this_thread::sleep_for(5ms);
        }
    }

    bool finished(std::int64_t id, bool ok) const {
        for (const auto& e : seen) {
            if (e.kind == EventKind::OpFinished && e.op_id == id) {
                return e.ok == ok;
            }
        }
        return false;
    }

    const Event* find(EventKind kind, const std::string& name = {}) const {
        for (const auto& e : seen) {
            if (e.kind == kind && (name.empty() || e.name == name)) {
                return &e;
            }
        }
        return nullptr;
    }

    int count(EventKind kind) const {
        int total = 0;
        for (const auto& e : seen) {
            if (e.kind == kind) {
                ++total;
            }
        }
        return total;
    }

    const Event* op_finished(std::int64_t id) const {
        for (const auto& e : seen) {
            if (e.kind == EventKind::OpFinished && e.op_id == id) {
                return &e;
            }
        }
        return nullptr;
    }
};

}  // namespace

TEST_CASE("start connects, loads the caches and emits the first events") {
    Harness h;
    h.start();
    CHECK(h.wait([](const Events& e) {
        int connected = 0;
        for (const auto& event : e) {
            if (event.kind == EventKind::Connected) ++connected;
        }
        return connected > 0 && std::any_of(e.begin(), e.end(), [](const Event& event) {
                   return event.kind == EventKind::StatusChanged;
               });
    }));
    CHECK(h.session->status_blocking(2s));
    CHECK(h.session->connected());
    CHECK(h.session->version() == "0.2.0");
    CHECK(h.session->remotes() == std::vector<std::string>{"host/org/proj"});
    auto status = h.session->status_cache();
    CHECK(status.snapshot().branch == "main");
    REQUIRE(status.entries().size() == 1);
    CHECK(status.entries()[0].path == "a.gd");
    CHECK(h.wait([](const Events& e) {
        return std::any_of(e.begin(), e.end(), [](const Event& event) { return event.kind == EventKind::BranchesChanged; }) &&
               std::any_of(e.begin(), e.end(), [](const Event& event) { return event.kind == EventKind::CommitsChanged; });
    }));
    CHECK(h.session->branches().size() == 1);
    CHECK(h.session->commits().size() == 1);
    CHECK(h.session->commits()[0].author_name == "Ann");
    CHECK(h.session->status_blocking(1ms));
    CHECK(h.find(EventKind::Connected)->message == "0.2.0");
}

TEST_CASE("operations run in submission order") {
    Harness h;
    h.start();
    auto update = h.session->submit_update();
    auto push = h.session->submit_push("msg");
    auto sw = h.session->submit_switch("dev");
    CHECK(update < push);
    CHECK(push < sw);
    CHECK(h.wait([&](const Events&) { return h.finished(update, true) && h.finished(push, true) && h.finished(sw, true); }));

    std::vector<std::string> order;
    for (const auto& request : h.fake.requests()) {
        if (request.method == "Update" || request.method == "Push" || request.method == "Switch") {
            order.push_back(request.method);
        }
    }
    CHECK(order == std::vector<std::string>{"Update", "Push", "Switch"});

    std::vector<std::int64_t> queued;
    std::vector<std::int64_t> finished;
    for (const auto& e : h.seen) {
        if (e.kind == EventKind::OpQueued && e.op_id >= update) queued.push_back(e.op_id);
        if (e.kind == EventKind::OpFinished && e.op_id >= update) finished.push_back(e.op_id);
    }
    CHECK(queued == std::vector<std::int64_t>{update, push, sw});
    CHECK(finished == std::vector<std::int64_t>{update, push, sw});
}

TEST_CASE("a push streams queued, started, progress and finished events") {
    Harness h;
    h.start();
    auto push = h.session->submit_push("msg");
    CHECK(h.wait([&](const Events&) { return h.op_finished(push) != nullptr; }));

    std::vector<EventKind> kinds;
    for (const auto& e : h.seen) {
        if (e.op_id == push && e.kind != EventKind::OpFinished) {
            kinds.push_back(e.kind);
        }
    }
    CHECK(kinds == std::vector<EventKind>{EventKind::OpQueued, EventKind::OpStarted, EventKind::OpProgress});
    const Event* progress = nullptr;
    for (const auto& e : h.seen) {
        if (e.kind == EventKind::OpProgress && e.op_id == push) progress = &e;
    }
    REQUIRE(progress != nullptr);
    CHECK(progress->message == "upload");
    CHECK(progress->done == 1);
    CHECK(progress->total == 1);
    CHECK(h.op_finished(push)->ok);
    CHECK(h.op_finished(push)->message == "c9");
    CHECK(h.op_finished(push)->name == "push");
}

TEST_CASE("a daemon failure in an operation is reported on OpFinished") {
    Harness h;
    h.fake.on_stream("Push", [](const json&) {
        StreamScript script;
        script.lines = {R"({"failure":{"code":409,"message":"needs lock"}})"};
        return script;
    });
    h.start();
    auto push = h.session->submit_push("msg");
    CHECK(h.wait([&](const Events&) { return h.op_finished(push) != nullptr; }));
    CHECK_FALSE(h.op_finished(push)->ok);
    CHECK(h.op_finished(push)->message == "needs lock");
}

TEST_CASE("an operation error from a unary call fails only that operation") {
    Harness h;
    h.fake.on_unary("ProxyBranchCreate", [](const json&) {
        return Reply{409, {{"code", "AlreadyExists"}, {"message", "branch exists"}}};
    });
    h.start();
    auto create = h.session->submit_branch_create("dev", "main");
    CHECK(h.wait([&](const Events&) { return h.op_finished(create) != nullptr; }));
    CHECK_FALSE(h.op_finished(create)->ok);
    CHECK(h.op_finished(create)->message == "branch exists");
}

TEST_CASE("switch refreshes branches, commits and status afterwards") {
    Harness h;
    h.start();
    CHECK(h.wait([](const Events& e) { return !e.empty(); }));
    auto sw = h.session->submit_switch("dev", "");
    CHECK(h.wait([&](const Events&) { return h.finished(sw, true); }));
    CHECK(h.wait([&](const Events&) {
        return h.fake.count("ProxyBranchList") >= 2 && h.fake.count("ProxyCommitLog") >= 2 && h.fake.count("Status") >= 2;
    }));
}

TEST_CASE("branch create and delete refresh the branch list") {
    Harness h;
    h.start();
    auto create = h.session->submit_branch_create("feature", "main");
    auto remove = h.session->submit_branch_delete("feature");
    CHECK(h.wait([&](const Events&) { return h.finished(create, true) && h.finished(remove, true); }));
    CHECK(h.fake.count("ProxyBranchCreate") == 1);
    CHECK(h.fake.count("ProxyBranchDelete") == 1);
    CHECK(h.wait([&](const Events&) { return h.fake.count("ProxyBranchList") >= 3; }));
}

TEST_CASE("stage and restore apply the status reply") {
    Harness h;
    h.start();
    CHECK(h.wait([](const Events& e) {
        return std::any_of(e.begin(), e.end(), [](const Event& event) { return event.kind == EventKind::CommitsChanged; });
    }));
    auto stage = h.session->submit_stage({"a.gd"}, {});
    CHECK(h.wait([&](const Events&) { return h.finished(stage, true); }));
    CHECK(h.session->status_cache().snapshot().staged == std::vector<std::string>{"a.gd"});
    CHECK(h.find(EventKind::StatusChanged) != nullptr);
    auto restore = h.session->submit_restore({"b.gd"});
    CHECK(h.wait([&](const Events&) { return h.finished(restore, true); }));
    CHECK(h.session->status_cache().snapshot().modified == std::vector<std::string>{"a.gd"});
}

TEST_CASE("a status change from WatchChanges emits StatusChanged with the new paths") {
    Harness h;
    std::atomic<int> status_calls{0};
    h.fake.on_unary("Status", [&status_calls](const json&) {
        if (status_calls++ == 0) {
            return Reply{200, {{"modified", {"a.gd"}}}};
        }
        return Reply{200, {{"modified", {"a.gd", "b.gd"}}}};
    });
    std::atomic<int> watches{0};
    h.fake.on_stream("WatchChanges", [&watches](const json&) {
        if (watches++ == 0) {
            StreamScript script;
            script.lines = {R"({"paths":["b.gd"]})"};
            return script;
        }
        return Harness::silent(json::object());
    });
    h.start();
    CHECK(h.wait([&](const Events& events) {
        for (const auto& e : events) {
            if (e.kind == EventKind::StatusChanged && e.paths == std::vector<std::string>{"b.gd"}) return true;
        }
        return false;
    }));
}

TEST_CASE("a dropped WatchChanges stream reconnects and re-watches the repo") {
    Harness h;
    std::atomic<int> watches{0};
    h.fake.on_stream("WatchChanges", [&watches](const json&) {
        if (watches++ == 0) {
            StreamScript script;
            script.lines = {R"({"paths":[]})"};
            return script;
        }
        return Harness::silent(json::object());
    });
    h.start();
    CHECK(h.wait([&](const Events&) {
        return h.find(EventKind::Disconnected) != nullptr && h.fake.count("WatchRepo") >= 2;
    }));
    CHECK(h.find(EventKind::Disconnected)->message == "stream ended");
    CHECK(h.wait([&](const Events&) { return h.count(EventKind::Connected) >= 2; }));
}

TEST_CASE("a malformed stream line disconnects and the session recovers") {
    Harness h;
    std::atomic<int> watches{0};
    h.fake.on_stream("WatchChanges", [&watches](const json&) {
        if (watches++ == 0) {
            StreamScript script;
            script.lines = {"{broken"};
            return script;
        }
        return Harness::silent(json::object());
    });
    h.start();
    CHECK(h.wait([&](const Events&) {
        const Event* down = h.find(EventKind::Disconnected);
        return down != nullptr && down->message == "malformed stream line";
    }));
    CHECK(h.wait([&](const Events&) { return h.fake.count("WatchRepo") >= 2; }));
}

TEST_CASE("a daemon that goes away is reported as disconnected") {
    Harness h;
    h.start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Connected) != nullptr; }));
    h.fake.stop();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Disconnected) != nullptr; }));
    CHECK_FALSE(h.session->connected());
}

TEST_CASE("activity events and summaries are forwarded") {
    Harness h;
    std::atomic<int> polls{0};
    h.fake.on_stream("WatchEvents", [&polls](const json& request) {
        if (polls++ == 0) {
            StreamScript script;
            script.lines = {
                R"({"events":[{"kind":"pushed","actor":"Bo","message":"fix","path":""},{"kind":"lock_released","actor":"Cy","path":"hero.png"}],"incoming":2,"review_requests":1,"error":""})",
            };
            return script;
        }
        return Harness::silent(json::object());
    });
    h.start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::ActivitySummary) != nullptr; }));
    const Event* pushed = h.find(EventKind::Activity, "pushed");
    REQUIRE(pushed != nullptr);
    CHECK(pushed->actor == "Bo");
    CHECK(pushed->message == "fix");
    const Event* release = h.find(EventKind::Activity, "lock_released");
    REQUIRE(release != nullptr);
    CHECK(release->paths == std::vector<std::string>{"hero.png"});
    const Event* summary = h.find(EventKind::ActivitySummary);
    CHECK(summary->done == 2);
    CHECK(summary->total == 1);
}

TEST_CASE("activity can be disabled") {
    Harness h;
    h.start();
    h.session->stop();
    h.session.reset();
    SessionOptions options = h.options();
    options.watch_activity = false;
    h.session = std::make_unique<Session>(options);
    h.session->start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Connected) != nullptr; }));
    CHECK(h.fake.count("WatchEvents") == 0);
}

TEST_CASE("stop with an operation in flight cancels it and returns promptly") {
    Harness h;
    h.fake.on_stream("Push", [](const json&) {
        StreamScript script;
        script.endless = true;
        script.delay_ms = 20;
        script.lines = {R"({"started":{"phase":"upload"}})"};
        return script;
    });
    h.start();
    auto push = h.session->submit_push("msg");
    CHECK(h.wait([&](const Events&) {
        for (const auto& e : h.seen) {
            if (e.kind == EventKind::OpStarted && e.op_id == push) return true;
        }
        return false;
    }));
    auto begin = std::chrono::steady_clock::now();
    h.session->stop();
    CHECK(std::chrono::steady_clock::now() - begin < 2s);
    CHECK(h.wait([&](const Events&) { return h.op_finished(push) != nullptr; }, 1s));
    CHECK_FALSE(h.op_finished(push)->ok);
    CHECK(h.op_finished(push)->message == "cancelled");
}

TEST_CASE("stop drops queued operations with a stopped message") {
    Harness h;
    h.fake.on_unary("Status", [](const json&) {
        std::this_thread::sleep_for(400ms);
        return Reply{200, {{"branch", "main"}}};
    });
    h.start();
    auto update = h.session->submit_update();
    h.session->stop();
    CHECK(h.wait([&](const Events&) { return h.op_finished(update) != nullptr; }, 1s));
    CHECK_FALSE(h.op_finished(update)->ok);
    CHECK(h.op_finished(update)->message == "session stopped");
}

TEST_CASE("a slow daemon does not block the cold start past its timeout") {
    Harness h;
    h.fake.on_unary("Status", [](const json&) {
        std::this_thread::sleep_for(1500ms);
        return Reply{200, {{"branch", "main"}}};
    });
    h.start();
    auto begin = std::chrono::steady_clock::now();
    CHECK_FALSE(h.session->status_blocking(200ms));
    CHECK(std::chrono::steady_clock::now() - begin < 1s);
    auto stop_begin = std::chrono::steady_clock::now();
    h.session->stop();
    CHECK(std::chrono::steady_clock::now() - stop_begin < 1s);
}

TEST_CASE("a missing daemon is reported and the caller is not blocked") {
    Harness h;
    SessionOptions options = h.options();
    options.connect.endpoint_path = h.dir + "/absent/daemon.json";
    options.connect.binary_setting = "";
    h.session = std::make_unique<Session>(options);
    h.session->start();
    CHECK_FALSE(h.session->status_blocking(100ms));
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Disconnected) != nullptr; }));
    CHECK(h.find(EventKind::Disconnected)->message.find("nipa CLI not found") != std::string::npos);
    CHECK_FALSE(h.session->connected());
}

TEST_CASE("diff requests keep their result under the key") {
    Harness h;
    h.fake.on_stream("DiffFiles", [](const json&) {
        StreamScript script;
        script.lines = {
            R"({"file":{"path":"a.gd","status":"modified","kind":"text","additions":1,"deletions":0,"hunks":[{"old_start":1,"old_lines":0,"new_start":1,"new_lines":1,"lines":[{"kind":"added","new_line":1,"text":"x"}]}]}})",
        };
        return script;
    });
    h.start();
    auto id = h.session->submit_diff("working", json{{"root", "/work/clone"}});
    CHECK(id > 0);
    CHECK(h.session->diff_blocking("working", 2s));
    auto files = h.session->diff_result("working");
    REQUIRE(files.has_value());
    REQUIRE(files->size() == 1);
    CHECK((*files)[0].new_path == "a.gd");
    CHECK((*files)[0].hunks[0].lines[0].status == '+');
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::DiffReady) != nullptr; }));
    CHECK(h.find(EventKind::DiffReady)->ok);
    CHECK_FALSE(h.session->diff_result("missing").has_value());
}

TEST_CASE("snapshot blobs are decoded from their base64 chunks") {
    Harness h;
    h.fake.on_stream("ReadBlob", [](const json&) {
        StreamScript script;
        script.lines = {R"({"info":{"path":"a.gd","size_bytes":"5"}})", R"({"data":"aGVs"})", R"({"data":"bG8="})"};
        return script;
    });
    h.start();
    h.session->submit_read_blob("snap:a.gd", "a.gd");
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::BlobReady) != nullptr; }));
    CHECK(h.find(EventKind::BlobReady)->ok);
    CHECK(h.session->blob_blocking("snap:a.gd", 1s));
    CHECK(h.session->blob_result("snap:a.gd") == std::optional<std::string>("hello"));
    CHECK_FALSE(h.session->blob_result("other").has_value());
}

TEST_CASE("a failed blob read is reported and not cached") {
    Harness h;
    h.fake.on_stream("ReadBlob", [](const json&) {
        StreamScript script;
        script.status = 404;
        return script;
    });
    h.start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Connected) != nullptr; }));
    h.session->submit_read_blob("snap:x", "x");
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::BlobReady) != nullptr; }));
    CHECK_FALSE(h.find(EventKind::BlobReady)->ok);
    CHECK(h.session->blob_known("snap:x"));
    CHECK_FALSE(h.session->blob_result("snap:x").has_value());
    CHECK(h.session->blob_blocking("snap:x", 50ms));
}

TEST_CASE("resubmitting a diff drops the previous result at once") {
    Harness h;
    h.fake.on_stream("DiffFiles", [](const json&) {
        StreamScript script;
        script.lines = {R"({"file":{"path":"a.tscn","status":"modified","kind":"text"}})"};
        script.endless = false;
        return script;
    });
    h.start();
    h.session->submit_diff("work:a", json{{"paths", {"a.tscn"}}});
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::DiffReady) != nullptr; }));
    CHECK(h.session->diff_result("work:a").has_value());
    h.session->submit_diff("work:a", json{{"paths", {"a.tscn"}}});
    CHECK_FALSE(h.session->diff_result("work:a").has_value());
}

TEST_CASE("a failed diff reports through DiffReady and never fills the cache") {
    Harness h;
    h.fake.on_stream("DiffFiles", [](const json&) {
        StreamScript script;
        script.lines = {R"({"failure":{"code":500,"message":"boom"}})"};
        return script;
    });
    h.start();
    h.session->submit_diff("commit", json{{"revisions", {"c1"}}});
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::DiffReady) != nullptr; }));
    CHECK_FALSE(h.find(EventKind::DiffReady)->ok);
    CHECK(h.find(EventKind::DiffReady)->message == "boom");
    CHECK_FALSE(h.session->diff_blocking("commit", 100ms));
}

TEST_CASE("a diff call that cannot reach the daemon reports the error") {
    Harness h;
    h.fake.on_stream("DiffFiles", [](const json&) {
        StreamScript script;
        script.status = 404;
        return script;
    });
    h.start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Connected) != nullptr; }));
    h.session->submit_diff("bad", json::object());
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::DiffReady) != nullptr; }));
    CHECK_FALSE(h.find(EventKind::DiffReady)->ok);
}

TEST_CASE("submissions after stop are ignored") {
    Harness h;
    h.start();
    h.session->stop();
    CHECK(h.session->submit_status() == 0);
    CHECK(h.session->submit_update() == 0);
    CHECK(h.session->submit_push("x") == 0);
    CHECK(h.session->submit_diff("k", json::object()) == 0);
}

TEST_CASE("stop is idempotent and the event queue stays readable") {
    Harness h;
    h.start();
    CHECK(h.wait([&](const Events&) { return h.find(EventKind::Connected) != nullptr; }));
    h.session->stop();
    h.session->stop();
    h.wait([](const Events&) { return false; }, 10ms);
}

TEST_CASE("sign-in is skipped for a live session and uses the key or password otherwise") {
    Harness h;
    std::atomic<int> signed_in{1};
    h.fake.on_unary("AccountStatus", [&signed_in](const json&) {
        return Reply{200, {{"signed_in", signed_in.load() == 1}, {"email", "ann@example.com"}}};
    });
    h.fake.on_unary("LoginSSH", [](const json&) { return Reply{200, json::object()}; });
    h.fake.on_unary("Login", [](const json&) { return Reply{200, json::object()}; });
    h.start();
    auto live = h.session->submit_sign_in("host", "", "", "", "");
    CHECK(h.wait([&](const Events&) { return h.finished(live, true); }));
    CHECK(h.fake.count("LoginSSH") == 0);

    signed_in = 0;
    auto keyed = h.session->submit_sign_in("host", "", "", "/keys/id", "pw");
    CHECK(h.wait([&](const Events&) { return h.finished(keyed, true); }));
    CHECK(h.fake.count("LoginSSH") == 1);

    auto password = h.session->submit_sign_in("host", "ann", "secret", "", "");
    CHECK(h.wait([&](const Events&) { return h.finished(password, true); }));
    CHECK(h.fake.count("Login") == 1);

    auto none = h.session->submit_sign_in("host", "", "", "", "");
    CHECK(h.wait([&](const Events&) { return h.finished(none, false); }));
    CHECK(h.op_finished(none)->message == "no credentials for host");
}

TEST_CASE("event queue drains in push order and empties") {
    nipa::session::EventQueue queue;
    Event first;
    first.name = "a";
    Event second;
    second.name = "b";
    queue.push(first);
    queue.push(second);
    CHECK(queue.size() == 2);
    auto drained = queue.drain();
    REQUIRE(drained.size() == 2);
    CHECK(drained[0].name == "a");
    CHECK(drained[1].name == "b");
    CHECK(queue.size() == 0);
    CHECK(queue.drain().empty());
}

namespace {

struct LockServer {
    std::mutex mu;
    json status = {{"user_id", "me"},
                   {"branch", "main"},
                   {"default_branch", true},
                   {"locks", {{{"id", "1"}, {"path", "art/a.png"}, {"global", true}, {"held_by", "u2"}, {"held_by_name", "Ann"}},
                              {{"id", "2"}, {"path", "art/m.png"}, {"global", true}, {"held_by", "me"}}}},
                   {"files", {{{"path", "art/n.png"}, {"state", "needed"}},
                              {{"path", "art/a.png"}, {"state", "theirs"}, {"held_by_name", "Ann"}}}}};
    int unlock_status = 200;

    void install(FakeDaemon& fake) {
        fake.on_unary("LockStatus", [this](const json&) {
            std::lock_guard<std::mutex> guard(mu);
            return Reply{200, status};
        });
        fake.on_unary("ProxyLockFile", [this](const json& body) {
            std::lock_guard<std::mutex> guard(mu);
            auto path = body["request"]["path"].get<std::string>();
            if (path == "art/denied.png") {
                return Reply{409, {{"code", "AlreadyExists"}, {"message", "denied"}}};
            }
            status["locks"].push_back({{"id", "n-" + path}, {"path", path}, {"global", true}, {"held_by", "me"}});
            return Reply{200, {{"response", json::object()}}};
        });
        fake.on_unary("ProxyUnlockFile", [this](const json& body) {
            std::lock_guard<std::mutex> guard(mu);
            if (unlock_status != 200) {
                return Reply{unlock_status, {{"code", unlock_status == 404 ? "NotFound" : "Internal"}, {"message", "boom"}}};
            }
            auto path = body["request"]["path"].get<std::string>();
            json kept = json::array();
            for (const auto& l : status["locks"]) {
                if (l["path"] != path) kept.push_back(l);
            }
            status["locks"] = kept;
            return Reply{200, {{"response", json::object()}}};
        });
    }

    void set(const json& locks) {
        std::lock_guard<std::mutex> guard(mu);
        status["locks"] = locks;
    }
};

bool has_kind(const Events& events, EventKind kind) {
    return std::any_of(events.begin(), events.end(), [kind](const Event& e) { return e.kind == kind; });
}

bool writable(const std::filesystem::path& path) {
    return (std::filesystem::status(path).permissions() & std::filesystem::perms::owner_write) !=
           std::filesystem::perms::none;
}

}  // namespace

TEST_CASE("locks load after connecting and answer lookups") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    h.start();
    CHECK(h.wait([](const Events& e) { return has_kind(e, EventKind::LocksChanged); }));
    auto policy = h.session->locks();
    CHECK(policy.loaded());
    CHECK(policy.lookup("art/a.png.import").state == nipa::locks::LockState::Theirs);
    CHECK(policy.lookup("art/m.png").state == nipa::locks::LockState::Mine);
}

TEST_CASE("lock acquires free paths, skips mine and refuses theirs") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    h.start();
    auto id = h.session->submit_lock({"art/b.png", "art/m.png", "art/a.png", "art/denied.png"});
    CHECK(h.wait([&](const Events&) { return h.op_finished(id) != nullptr; }));
    const Event* done = h.op_finished(id);
    CHECK_FALSE(done->ok);
    CHECK(done->name == "lock");
    CHECK(done->message.find("art/a.png is locked by Ann") != std::string::npos);
    CHECK(done->message.find("art/denied.png: denied") != std::string::npos);

    std::vector<std::string> locked;
    for (const auto& r : h.fake.requests()) {
        if (r.method == "ProxyLockFile") {
            locked.push_back(r.body["request"]["path"]);
            CHECK(r.body["request"]["branch"] == "");
        }
    }
    CHECK(locked == std::vector<std::string>{"art/b.png", "art/denied.png"});
    CHECK(h.session->locks().lookup("art/b.png").state == nipa::locks::LockState::Mine);

    auto ok = h.session->submit_lock({"art/c.png"});
    CHECK(h.wait([&](const Events&) { return h.op_finished(ok) != nullptr; }));
    CHECK(h.op_finished(ok)->ok);
    CHECK(h.op_finished(ok)->message == "locked 1 file(s)");
}

TEST_CASE("unlock releases my locks on the unit and tolerates a missing lock") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    h.start();
    auto id = h.session->submit_unlock({"art/m.png.import", "art/a.png"}, false);
    CHECK(h.wait([&](const Events&) { return h.op_finished(id) != nullptr; }));
    CHECK(h.op_finished(id)->ok);
    CHECK(h.op_finished(id)->message == "released 1 lock(s)");
    CHECK(h.fake.count("ProxyUnlockFile") == 1);
    CHECK(h.session->locks().lookup("art/m.png").state == nipa::locks::LockState::Free);

    server.set({{{"id", "3"}, {"path", "x.png"}, {"global", true}, {"held_by", "me"}}});
    server.unlock_status = 404;
    h.session->submit_locks();
    CHECK(h.wait([&](const Events&) { return h.session->locks().lookup("x.png").state == nipa::locks::LockState::Mine; }));
    auto missing = h.session->submit_unlock({"x.png"}, false);
    CHECK(h.wait([&](const Events&) { return h.op_finished(missing) != nullptr; }));
    CHECK(h.op_finished(missing)->ok);

    server.unlock_status = 500;
    auto failed = h.session->submit_unlock({"x.png"}, false);
    CHECK(h.wait([&](const Events&) { return h.op_finished(failed) != nullptr; }));
    CHECK_FALSE(h.op_finished(failed)->ok);
    CHECK(h.op_finished(failed)->message.find("x.png") != std::string::npos);
}

TEST_CASE("lock and unlock fail cleanly without a lock status") {
    Harness h;
    h.start();
    auto lock = h.session->submit_lock({"a.png"});
    auto unlock = h.session->submit_unlock({"a.png"}, true);
    CHECK(h.wait([&](const Events&) { return h.op_finished(lock) && h.op_finished(unlock); }));
    CHECK(h.op_finished(lock)->message == "lock status is unavailable");
    CHECK(h.op_finished(unlock)->message == "lock status is unavailable");
    CHECK_FALSE(h.session->push_check_blocking(2s).has_value());
}

TEST_CASE("push check refreshes the lock status first") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    h.start();
    auto check = h.session->push_check_blocking(2s);
    REQUIRE(check.has_value());
    CHECK(check->needed == std::vector<std::string>{"art/n.png"});
    REQUIRE(check->theirs.size() == 1);
    CHECK(check->theirs_summary() == "art/a.png (Ann)");

    h.session->stop();
    CHECK_FALSE(h.session->push_check_blocking(10ms).has_value());
    CHECK(h.session->submit_locks() == 0);
}

TEST_CASE("activity polls refresh the lock cache") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    h.fake.on_stream("WatchEvents", [](const json&) {
        StreamScript script;
        script.lines = {R"({"events":[{"kind":"lock_released","actor":"Ann","path":"art/a.png"}],"incoming":0})"};
        script.endless = true;
        script.delay_ms = 30;
        return script;
    });
    h.start();
    CHECK(h.wait([&](const Events&) { return h.fake.count("LockStatus") >= 3; }));
}

TEST_CASE("strict mode guards their locks and restores them when turned off or stopped") {
    LockServer server;
    Harness h;
    server.install(h.fake);
    std::string root = nipa::test::make_temp_dir();
    std::filesystem::create_directories(root + "/art");
    std::ofstream(root + "/art/a.png") << "png";
    std::ofstream(root + "/art/a.png.import") << "import";
    std::ofstream(root + "/art/m.png") << "png";

    auto options = h.options();
    options.root = root;
    h.session = std::make_unique<Session>(options);
    h.session->set_lock_options({"png"}, true, root + "/.godot/nipa/readonly.json");
    h.session->start();
    CHECK(h.wait([&](const Events& e) {
        return std::any_of(e.begin(), e.end(), [](const Event& event) {
            return event.kind == EventKind::LocksChanged && event.paths.size() == 2;
        });
    }));
    CHECK_FALSE(writable(root + "/art/a.png"));
    CHECK_FALSE(writable(root + "/art/a.png.import"));
    CHECK(writable(root + "/art/m.png"));

    server.set(json::array());
    h.session->submit_locks();
    CHECK(h.wait([&](const Events&) { return writable(root + "/art/a.png"); }));

    server.set({{{"id", "1"}, {"path", "art"}, {"global", true}, {"held_by", "u2"}}});
    h.session->submit_locks();
    CHECK(h.wait([&](const Events&) { return !writable(root + "/art/m.png"); }));

    h.session->set_lock_options({"png"}, false, root + "/.godot/nipa/readonly.json");
    CHECK(h.wait([&](const Events&) { return writable(root + "/art/m.png"); }));

    h.session->set_lock_options({"png"}, true, root + "/.godot/nipa/readonly.json");
    CHECK(h.wait([&](const Events&) { return !writable(root + "/art/m.png"); }));
    h.session->stop();
    CHECK(writable(root + "/art/m.png"));
    CHECK(writable(root + "/art/a.png"));
    std::filesystem::remove_all(root);
}
