#include "nipa_session.h"

#include <chrono>
#include <cstdlib>
#include <filesystem>
#include <set>

#include <godot_cpp/classes/engine.hpp>
#include <godot_cpp/classes/main_loop.hpp>
#include <godot_cpp/classes/project_settings.hpp>
#include <godot_cpp/classes/scene_tree.hpp>
#include <godot_cpp/variant/dictionary.hpp>

#include "core/daemon/connector.h"
#include "core/status/status.h"
#include "nipa_util.h"

using namespace godot;

namespace nipa::glue {
namespace {

constexpr const char* kMinDaemonVersion = "0.1.0";
constexpr int kMinPollSeconds = 15;
constexpr int kMaxPollSeconds = 3600;

bool path_exists(const std::string& path) {
    std::error_code error;
    return std::filesystem::exists(path, error);
}

// Directory of this extension library, where a bundled `nipa` binary sits.
std::string library_dir() {
    String path;
    internal::gdextension_interface_get_library_path(internal::library, path._native_ptr());
    if (path.is_empty()) {
        return {};
    }
    return from_godot(ProjectSettings::get_singleton()->globalize_path(path).get_base_dir());
}

Ref<NipaRequest> done_request(bool ok, const char* message) {
    Ref<NipaRequest> request;
    request.instantiate();
    request->complete(ok, message);
    return request;
}

}  // namespace

void NipaSession::_bind_methods() {
    ClassDB::bind_method(D_METHOD("open_project", "project_dir", "binary_path", "poll_seconds"), &NipaSession::open_project);
    ClassDB::bind_method(D_METHOD("close_project"), &NipaSession::close_project);
    ClassDB::bind_method(D_METHOD("is_open"), &NipaSession::is_open);
    ClassDB::bind_method(D_METHOD("get_clone_root"), &NipaSession::get_clone_root);
    ClassDB::bind_method(D_METHOD("get_project_prefix"), &NipaSession::get_project_prefix);
    ClassDB::bind_method(D_METHOD("to_clone_path", "project_path"), &NipaSession::to_clone_path);
    ClassDB::bind_method(D_METHOD("to_project_path", "clone_path"), &NipaSession::to_project_path);
    ClassDB::bind_method(D_METHOD("status_blocking", "timeout_ms"), &NipaSession::status_blocking);
    ClassDB::bind_method(D_METHOD("is_connected"), &NipaSession::is_connected);
    ClassDB::bind_method(D_METHOD("get_version"), &NipaSession::get_version);
    ClassDB::bind_method(D_METHOD("get_status_entries"), &NipaSession::get_status_entries);
    ClassDB::bind_method(D_METHOD("get_branch_name"), &NipaSession::get_branch_name);
    ClassDB::bind_method(D_METHOD("get_branch_names"), &NipaSession::get_branch_names);
    ClassDB::bind_method(D_METHOD("get_remote_names"), &NipaSession::get_remote_names);
    ClassDB::bind_method(D_METHOD("get_commits", "max_commits"), &NipaSession::get_commits);
    ClassDB::bind_method(D_METHOD("get_outside_project_staged"), &NipaSession::get_outside_project_staged);
    ClassDB::bind_method(D_METHOD("request_stage", "add", "unstage"), &NipaSession::request_stage);
    ClassDB::bind_method(D_METHOD("request_restore", "paths"), &NipaSession::request_restore);
    ClassDB::bind_method(D_METHOD("request_push", "message"), &NipaSession::request_push);
    ClassDB::bind_method(D_METHOD("request_update"), &NipaSession::request_update);
    ClassDB::bind_method(D_METHOD("request_switch", "branch"), &NipaSession::request_switch);
    ClassDB::bind_method(D_METHOD("request_branch_create", "name", "from_branch"), &NipaSession::request_branch_create);
    ClassDB::bind_method(D_METHOD("request_branch_delete", "name"), &NipaSession::request_branch_delete);
    ClassDB::bind_method(D_METHOD("request_sign_in", "server", "username", "password", "key_path", "passphrase"),
                         &NipaSession::request_sign_in);
    ClassDB::bind_method(D_METHOD("request_refresh"), &NipaSession::request_refresh);
    ClassDB::bind_method(D_METHOD("expand_sidecars", "clone_paths"), &NipaSession::expand_sidecars);
    ClassDB::bind_method(D_METHOD("drain_events"), &NipaSession::drain_events);

    ADD_SIGNAL(MethodInfo("connected", PropertyInfo(Variant::STRING, "version")));
    ADD_SIGNAL(MethodInfo("disconnected", PropertyInfo(Variant::STRING, "reason")));
    ADD_SIGNAL(MethodInfo("status_changed", PropertyInfo(Variant::PACKED_STRING_ARRAY, "paths")));
    ADD_SIGNAL(MethodInfo("branches_changed"));
    ADD_SIGNAL(MethodInfo("commits_changed"));
    ADD_SIGNAL(MethodInfo("op_queued", PropertyInfo(Variant::INT, "id"), PropertyInfo(Variant::STRING, "kind")));
    ADD_SIGNAL(MethodInfo("op_progress", PropertyInfo(Variant::INT, "id"), PropertyInfo(Variant::STRING, "phase"),
                          PropertyInfo(Variant::INT, "done"), PropertyInfo(Variant::INT, "total")));
    ADD_SIGNAL(MethodInfo("op_finished", PropertyInfo(Variant::INT, "id"), PropertyInfo(Variant::BOOL, "ok"),
                          PropertyInfo(Variant::STRING, "message"),
                          PropertyInfo(Variant::PACKED_STRING_ARRAY, "changed_paths")));
    ADD_SIGNAL(MethodInfo("diff_ready", PropertyInfo(Variant::STRING, "key"), PropertyInfo(Variant::BOOL, "ok"),
                          PropertyInfo(Variant::STRING, "message")));
    ADD_SIGNAL(MethodInfo("blob_ready", PropertyInfo(Variant::STRING, "key"), PropertyInfo(Variant::BOOL, "ok"),
                          PropertyInfo(Variant::STRING, "message")));
    ADD_SIGNAL(MethodInfo("activity", PropertyInfo(Variant::STRING, "kind"), PropertyInfo(Variant::STRING, "actor"),
                          PropertyInfo(Variant::STRING, "detail")));
    ADD_SIGNAL(MethodInfo("activity_summary", PropertyInfo(Variant::INT, "incoming"),
                          PropertyInfo(Variant::INT, "review_requests"), PropertyInfo(Variant::STRING, "error")));
    ADD_SIGNAL(MethodInfo("notice", PropertyInfo(Variant::STRING, "text")));
}

NipaSession::~NipaSession() {
    close_project();
}

bool NipaSession::open_project(const String& project_dir, const String& binary_path, int poll_seconds) {
    close_project();
    auto located = paths::ProjectPaths::locate(from_godot(project_dir), path_exists);
    if (!located) {
        return false;
    }
    paths_ = located;

    session::SessionOptions options;
    options.connect.binary_setting = from_godot(binary_path);
    options.connect.min_version = kMinDaemonVersion;
    options.connect.env = [](const std::string& name) {
        const char* value = std::getenv(name.c_str());
        return value ? std::string(value) : std::string();
    };
    options.connect.file_exists = path_exists;
    options.connect.library_dir = library_dir();
    options.root = located->clone_root();
    int seconds = poll_seconds < kMinPollSeconds ? kMinPollSeconds : poll_seconds;
    seconds = seconds > kMaxPollSeconds ? kMaxPollSeconds : seconds;
    options.activity_interval = std::chrono::seconds(seconds);

    session_ = std::make_unique<session::Session>(options);
    if (auto* tree = Object::cast_to<SceneTree>(Engine::get_singleton()->get_main_loop())) {
        drain_callable_ = Callable(this, "drain_events");
        tree->connect("process_frame", drain_callable_);
    }
    session_->start();
    return true;
}

void NipaSession::close_project() {
    if (!session_) {
        return;
    }
    if (auto* tree = Object::cast_to<SceneTree>(Engine::get_singleton()->get_main_loop())) {
        if (tree->is_connected("process_frame", drain_callable_)) {
            tree->disconnect("process_frame", drain_callable_);
        }
    }
    session_->stop();
    drain_events();
    session_.reset();
    paths_.reset();
    for (auto& entry : requests_) {
        entry.second->complete(false, "session closed");
    }
    requests_.clear();
}

String NipaSession::get_clone_root() const {
    return paths_ ? to_godot(paths_->clone_root()) : String();
}

String NipaSession::get_project_prefix() const {
    return paths_ ? to_godot(paths_->project_prefix()) : String();
}

String NipaSession::to_clone_path(const String& project_path) const {
    return paths_ ? to_godot(paths_->to_clone(from_godot(project_path))) : project_path;
}

String NipaSession::to_project_path(const String& clone_path) const {
    if (!paths_) {
        return String();
    }
    auto projected = paths_->to_project(from_godot(clone_path));
    return projected ? to_godot(*projected) : String();
}

bool NipaSession::status_blocking(int timeout_ms) {
    if (!session_) {
        return false;
    }
    return session_->status_blocking(std::chrono::milliseconds(timeout_ms));
}

bool NipaSession::is_connected() const {
    return session_ && session_->connected();
}

String NipaSession::get_version() const {
    return session_ ? to_godot(session_->version()) : String();
}

Array NipaSession::get_status_entries() const {
    Array out;
    if (!session_) {
        return out;
    }
    const auto cache = session_->status_cache();
    for (const auto& entry : cache.entries()) {
        Dictionary item;
        item["path"] = to_godot(entry.path);
        item["change"] = static_cast<int>(entry.change);
        item["area"] = static_cast<int>(entry.area);
        out.push_back(item);
    }
    return out;
}

String NipaSession::get_branch_name() const {
    if (!session_) {
        return String();
    }
    auto snapshot = session_->status_cache().snapshot();
    if (snapshot.detached()) {
        return to_godot("tag:" + snapshot.head_name);
    }
    return to_godot(snapshot.branch);
}

PackedStringArray NipaSession::get_branch_names() const {
    PackedStringArray out;
    if (session_) {
        for (const auto& branch : session_->branches()) {
            out.push_back(to_godot(branch.name));
        }
    }
    return out;
}

PackedStringArray NipaSession::get_remote_names() const {
    PackedStringArray out;
    if (session_) {
        for (const auto& remote : session_->remotes()) {
            out.push_back(to_godot(remote));
        }
    }
    return out;
}

Array NipaSession::get_commits(int max_commits) const {
    Array out;
    if (!session_) {
        return out;
    }
    int taken = 0;
    for (const auto& commit : session_->commits()) {
        if (taken++ >= max_commits) {
            break;
        }
        Dictionary item;
        item["id"] = to_godot(commit.commit_id);
        item["hash"] = to_godot(commit.commit_hash);
        item["message"] = to_godot(commit.message);
        item["author"] = to_godot(commit.author_name);
        item["created_at"] = to_godot(commit.created_at);
        out.push_back(item);
    }
    return out;
}

PackedStringArray NipaSession::get_outside_project_staged() const {
    PackedStringArray out;
    if (!session_ || !paths_) {
        return out;
    }
    const auto cache = session_->status_cache();
    for (const auto& entry : cache.entries()) {
        if (entry.area == status::kStaged && paths_->outside_project(entry.path)) {
            out.push_back(to_godot(entry.path));
        }
    }
    return out;
}

Ref<NipaRequest> NipaSession::request_stage(const PackedStringArray& add, const PackedStringArray& unstage) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    std::vector<std::string> add_paths;
    for (int i = 0; i < add.size(); ++i) add_paths.push_back(from_godot(add[i]));
    std::vector<std::string> unstage_paths;
    for (int i = 0; i < unstage.size(); ++i) unstage_paths.push_back(from_godot(unstage[i]));
    return track(session_->submit_stage(std::move(add_paths), std::move(unstage_paths)));
}

Ref<NipaRequest> NipaSession::request_restore(const PackedStringArray& paths) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    std::vector<std::string> list;
    for (int i = 0; i < paths.size(); ++i) list.push_back(from_godot(paths[i]));
    return track(session_->submit_restore(std::move(list)));
}

Ref<NipaRequest> NipaSession::request_push(const String& message) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_push(from_godot(message)));
}

Ref<NipaRequest> NipaSession::request_update() {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_update());
}

Ref<NipaRequest> NipaSession::request_switch(const String& branch) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_switch(from_godot(branch)));
}

Ref<NipaRequest> NipaSession::request_branch_create(const String& name, const String& from_branch) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_branch_create(from_godot(name), from_godot(from_branch)));
}

Ref<NipaRequest> NipaSession::request_branch_delete(const String& name) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_branch_delete(from_godot(name)));
}

Ref<NipaRequest> NipaSession::request_sign_in(const String& server, const String& username, const String& password,
                                              const String& key_path, const String& passphrase) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(session_->submit_sign_in(from_godot(server), from_godot(username), from_godot(password),
                                          from_godot(key_path), from_godot(passphrase)));
}

Ref<NipaRequest> NipaSession::request_refresh() {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    session_->submit_status();
    session_->submit_branches();
    session_->submit_commits();
    return done_request(true, "refreshing");
}

PackedStringArray NipaSession::expand_sidecars(const PackedStringArray& clone_paths) const {
    PackedStringArray out;
    if (!paths_) {
        return out;
    }
    std::vector<std::string> requested;
    std::set<std::string> existing;
    for (int i = 0; i < clone_paths.size(); ++i) {
        std::string path = paths::normalize(from_godot(clone_paths[i]));
        requested.push_back(path);
        for (const auto& candidate : paths::sidecar_candidates(path)) {
            if (path_exists(paths_->clone_root() + "/" + candidate)) {
                existing.insert(candidate);
            }
        }
    }
    for (const auto& path : paths::expand_with_sidecars(requested, existing)) {
        out.push_back(to_godot(path));
    }
    return out;
}

void NipaSession::drain_events() {
    if (!session_) {
        return;
    }
    for (const auto& event : session_->events().drain()) {
        dispatch(event);
    }
}

Ref<NipaRequest> NipaSession::track(std::int64_t id) {
    if (id == 0) {
        return done_request(false, "session is stopping");
    }
    Ref<NipaRequest> request;
    request.instantiate();
    requests_[id] = request;
    return request;
}

void NipaSession::dispatch(const session::Event& event) {
    using session::EventKind;
    switch (event.kind) {
        case EventKind::Connected:
            emit_signal("connected", to_godot(event.message));
            break;
        case EventKind::Disconnected:
            emit_signal("disconnected", to_godot(event.message));
            break;
        case EventKind::StatusChanged:
            emit_signal("status_changed", [&] {
                PackedStringArray paths;
                for (const auto& p : event.paths) paths.push_back(to_godot(p));
                return paths;
            }());
            break;
        case EventKind::BranchesChanged:
            emit_signal("branches_changed");
            break;
        case EventKind::CommitsChanged:
        case EventKind::RemotesChanged:
            emit_signal("commits_changed");
            break;
        case EventKind::OpQueued:
            emit_signal("op_queued", static_cast<int64_t>(event.op_id), to_godot(event.name));
            break;
        case EventKind::OpStarted:
            break;
        case EventKind::OpProgress:
            emit_signal("op_progress", static_cast<int64_t>(event.op_id), to_godot(event.message),
                        static_cast<int64_t>(event.done), static_cast<int64_t>(event.total));
            break;
        case EventKind::OpFinished: {
            PackedStringArray paths;
            for (const auto& p : event.paths) paths.push_back(to_godot(p));
            emit_signal("op_finished", static_cast<int64_t>(event.op_id), event.ok, to_godot(event.message), paths);
            auto found = requests_.find(event.op_id);
            if (found != requests_.end()) {
                Ref<NipaRequest> request = found->second;
                requests_.erase(found);
                request->complete(event.ok, to_godot(event.message));
            }
            break;
        }
        case EventKind::DiffReady:
            emit_signal("diff_ready", to_godot(event.name), event.ok, to_godot(event.message));
            break;
        case EventKind::BlobReady:
            emit_signal("blob_ready", to_godot(event.name), event.ok, to_godot(event.message));
            break;
        case EventKind::Activity:
            emit_signal("activity", to_godot(event.name), to_godot(event.actor), to_godot(event.message));
            break;
        case EventKind::ActivitySummary:
            emit_signal("activity_summary", static_cast<int64_t>(event.done), static_cast<int64_t>(event.total),
                        to_godot(event.message));
            break;
    }
}

}  // namespace nipa::glue
