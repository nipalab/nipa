#include <algorithm>
#include <filesystem>
#include <fstream>
#include <optional>
#include <stdexcept>

#include "core/conflicts/merge_tool.h"
#include "core/preview/preview.h"
#include "core/session/session.h"

namespace nipa::session {
namespace {

using json = nlohmann::json;

void write_file(const std::string& path, const std::string& content) {
    std::ofstream out(path, std::ios::binary | std::ios::trunc);
    out.write(content.data(), static_cast<std::streamsize>(content.size()));
    if (!out) {
        throw std::runtime_error("cannot write " + path);
    }
}

json revision_ref(const std::string& revision, const std::string& path) {
    return {{"source", "revision"}, {"revision", revision}, {"path", path}};
}

}  // namespace

std::vector<history::Tag> Session::tags() const {
    std::lock_guard<std::mutex> guard(mu_);
    return tags_;
}

conflicts::Set Session::conflicts() const {
    std::lock_guard<std::mutex> guard(mu_);
    return conflicts_;
}

std::int64_t Session::submit_tags() {
    return enqueue("tags", true, [this](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("ProxyTagList", {{"root", options_.root}, {"request", {{"limit", 100}}}},
                                options_.unary_timeout, &cancel_);
        });
        auto list = history::parse_tags(daemon::proxy_payload(reply));
        {
            std::lock_guard<std::mutex> guard(mu_);
            tags_ = std::move(list);
        }
        Event e;
        e.kind = EventKind::TagsChanged;
        events_.push(std::move(e));
        return Outcome{};
    });
}

std::int64_t Session::submit_merge(std::string source_branch) {
    return enqueue("merge", false, [this, source_branch = std::move(source_branch)](std::int64_t id) {
        return run_stream_op(id, "merge", "Merge", {{"root", options_.root}, {"source_branch", source_branch}});
    });
}

std::int64_t Session::submit_merge_abort() {
    return enqueue("merge-abort", false, [this](std::int64_t id) {
        return run_stream_op(id, "merge-abort", "Merge", {{"root", options_.root}, {"abort", true}});
    });
}

std::int64_t Session::submit_revert(RevertAction action, std::string target) {
    return enqueue("revert", false, [this, action, target = std::move(target)](std::int64_t id) {
        json body = {{"root", options_.root}};
        switch (action) {
            case RevertAction::Start:
                body["target"] = target;
                break;
            case RevertAction::Continue:
                body["continue"] = true;
                break;
            case RevertAction::Skip:
                body["skip"] = true;
                break;
            case RevertAction::Abort:
                body["abort"] = true;
                break;
        }
        return run_stream_op(id, "revert", "Revert", body);
    });
}

conflicts::Set Session::load_conflicts() {
    json reply;
    run_on_link([&](const daemon::Client& client) {
        reply = client.call("Conflicts", {{"root", options_.root}}, options_.unary_timeout, &cancel_);
    });
    auto set = conflicts::parse_conflicts(reply);
    bool changed = false;
    {
        std::lock_guard<std::mutex> guard(mu_);
        changed = conflicts::to_json(set) != conflicts::to_json(conflicts_);
        conflicts_ = set;
    }
    if (changed) {
        Event e;
        e.kind = EventKind::ConflictsChanged;
        events_.push(std::move(e));
    }
    return set;
}

void Session::refresh_conflicts_async() {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (conflicts_pending_ || stopping_) {
            return;
        }
        conflicts_pending_ = true;
    }
    enqueue("conflicts-refresh", true, [this](std::int64_t) {
        {
            std::lock_guard<std::mutex> guard(mu_);
            conflicts_pending_ = false;
        }
        load_conflicts();
        return Outcome{};
    });
}

std::int64_t Session::submit_conflicts() {
    return enqueue("conflicts", false, [this](std::int64_t) {
        Outcome out;
        out.data = conflicts::to_json(load_conflicts());
        return out;
    });
}

std::int64_t Session::submit_resolve(std::string path, std::string choice, std::string content) {
    return enqueue("resolve", false,
                   [this, path = std::move(path), choice = std::move(choice), content = std::move(content)](std::int64_t) {
                       json body = {{"root", options_.root}, {"path", path}, {"choice", choice}};
                       if (choice == "content") {
                           body["content"] = daemon::base64_encode(content);
                       }
                       json reply;
                       run_on_link([&](const daemon::Client& client) {
                           reply = client.call("ResolveConflict", body, options_.unary_timeout, &cancel_);
                       });
                       apply_status(reply);
                       load_conflicts();
                       return Outcome{true, "resolved " + path, {path}};
                   });
}

std::int64_t Session::submit_merge_godot(std::string path) {
    return enqueue("merge-godot", false, [this, path = std::move(path)](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("MergeGodotResource", {{"root", options_.root}, {"path", path}}, options_.unary_timeout,
                                &cancel_);
        });
        auto merge = conflicts::parse_godot_merge(reply);
        Outcome out{true, merge.clean ? "clean" : std::to_string(merge.conflicts.size()) + " conflict(s)"};
        out.data = conflicts::to_json(merge);
        out.bytes["merged"] = std::move(merge.merged);
        return out;
    });
}

std::string Session::read_blob(const json& ref) {
    std::string content;
    std::optional<std::string> failure;
    run_on_link([&](const daemon::Client& client) {
        client.stream(
            "ReadBlob", {{"root", options_.root}, {"blob", ref}},
            [&](const json& item) {
                if (item.contains("failure")) {
                    failure = daemon::json_string(item["failure"], "message");
                    return false;
                }
                if (item.contains("data")) {
                    content += daemon::base64_decode(daemon::json_string(item, "data"));
                }
                return true;
            },
            options_.stream_timeout, &cancel_);
    });
    if (failure) {
        throw std::runtime_error(*failure);
    }
    return content;
}

std::int64_t Session::submit_prepare_merge_tool(std::string path, std::string dir) {
    return enqueue("merge-tool", false, [this, path = std::move(path), dir = std::move(dir)](std::int64_t) {
        auto set = load_conflicts();
        auto found = std::find_if(set.files.begin(), set.files.end(), [&](const auto& f) { return f.path == path; });
        if (!set.pending() || found == set.files.end()) {
            return Outcome{false, path + " has no conflict to resolve"};
        }
        std::string base;
        if (!set.base.id.empty()) {
            try {
                base = read_blob(revision_ref(set.base.id, path));
            } catch (const std::runtime_error&) {
                base.clear();
            }
        }
        std::string local = read_blob(revision_ref(set.ours.id, path));
        std::string remote = read_blob(revision_ref(set.theirs.id, path));
        std::string working = read_blob({{"source", "working"}, {"path", path}});
        std::filesystem::create_directories(dir);
        auto paths = conflicts::tool_paths(dir, path);
        write_file(paths.base, base);
        write_file(paths.local, local);
        write_file(paths.remote, remote);
        write_file(paths.merged, working);
        Outcome out{true, path};
        out.data = {{"base", paths.base}, {"local", paths.local}, {"remote", paths.remote}, {"merged", paths.merged}};
        return out;
    });
}

std::int64_t Session::submit_history(std::string path, std::string start, int limit) {
    return enqueue("history", false, [this, path = std::move(path), start = std::move(start), limit](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("ProxyCommitLog", history::page_request(options_.root, path, start, limit),
                                options_.unary_timeout, &cancel_);
        });
        Outcome out{true, path};
        out.data = history::to_json(history::parse_page(reply, limit));
        return out;
    });
}

std::int64_t Session::submit_preview(json old_ref, json new_ref, int threshold, int max_size) {
    return enqueue("preview", false, [this, old_ref = std::move(old_ref), new_ref = std::move(new_ref), threshold,
                                      max_size](std::int64_t) {
        preview::Assembler assembler;
        run_on_link([&](const daemon::Client& client) {
            client.stream(
                "Preview", preview::request(options_.root, old_ref, new_ref, threshold, max_size),
                [&](const json& item) { return assembler.add(item); }, options_.stream_timeout, &cancel_);
        });
        if (!assembler.error().empty()) {
            return Outcome{false, assembler.error()};
        }
        Outcome out{true, "preview"};
        out.data = assembler.summary();
        out.bytes["old_png"] = assembler.png("old");
        out.bytes["new_png"] = assembler.png("new");
        return out;
    });
}

}  // namespace nipa::session
