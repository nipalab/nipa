#include "nipa_session.h"

#include <chrono>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <set>

#include <godot_cpp/classes/engine.hpp>
#include <godot_cpp/classes/json.hpp>
#include <godot_cpp/classes/main_loop.hpp>
#include <godot_cpp/classes/project_settings.hpp>
#include <godot_cpp/classes/scene_tree.hpp>
#include <godot_cpp/variant/dictionary.hpp>

#include "core/conflicts/merge_tool.h"
#include "core/daemon/connector.h"
#include "core/decor/decor.h"
#include "core/setup/ignore.h"
#include "core/history/history.h"
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

Dictionary to_dictionary(const nlohmann::json& data) {
    if (!data.is_object()) {
        return Dictionary();
    }
    Variant parsed = JSON::parse_string(to_godot(data.dump()));
    return parsed.get_type() == Variant::DICTIONARY ? Dictionary(parsed) : Dictionary();
}

PackedByteArray to_bytes(const std::string& data) {
    PackedByteArray out;
    out.resize(static_cast<int64_t>(data.size()));
    if (!data.empty()) {
        std::memcpy(out.ptrw(), data.data(), data.size());
    }
    return out;
}

std::string from_bytes(const PackedByteArray& bytes) {
    return std::string(reinterpret_cast<const char*>(bytes.ptr()), static_cast<std::size_t>(bytes.size()));
}

nlohmann::json blob_ref(const Dictionary& ref) {
    if (ref.is_empty()) {
        return nullptr;
    }
    return {{"source", from_godot(ref.get("source", "working"))},
            {"revision", from_godot(ref.get("revision", ""))},
            {"path", from_godot(ref.get("path", ""))}};
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
    ClassDB::bind_method(D_METHOD("configure_locks", "extensions", "strict_read_only"), &NipaSession::configure_locks);
    ClassDB::bind_method(D_METHOD("get_lock_info", "clone_path"), &NipaSession::get_lock_info);
    ClassDB::bind_method(D_METHOD("get_locks"), &NipaSession::get_locks);
    ClassDB::bind_method(D_METHOD("request_lock", "clone_paths"), &NipaSession::request_lock);
    ClassDB::bind_method(D_METHOD("request_unlock", "clone_paths", "any_holder"), &NipaSession::request_unlock);
    ClassDB::bind_method(D_METHOD("request_lock_refresh"), &NipaSession::request_lock_refresh);
    ClassDB::bind_method(D_METHOD("get_tag_names"), &NipaSession::get_tag_names);
    ClassDB::bind_method(D_METHOD("get_head"), &NipaSession::get_head);
    ClassDB::bind_method(D_METHOD("get_conflicts"), &NipaSession::get_conflicts);
    ClassDB::bind_method(D_METHOD("get_commit_link", "commit_id"), &NipaSession::get_commit_link);
    ClassDB::bind_method(D_METHOD("request_switch_tag", "tag"), &NipaSession::request_switch_tag);
    ClassDB::bind_method(D_METHOD("request_merge", "source_branch"), &NipaSession::request_merge);
    ClassDB::bind_method(D_METHOD("request_merge_abort"), &NipaSession::request_merge_abort);
    ClassDB::bind_method(D_METHOD("request_revert", "target"), &NipaSession::request_revert);
    ClassDB::bind_method(D_METHOD("request_revert_continue"), &NipaSession::request_revert_continue);
    ClassDB::bind_method(D_METHOD("request_revert_skip"), &NipaSession::request_revert_skip);
    ClassDB::bind_method(D_METHOD("request_revert_abort"), &NipaSession::request_revert_abort);
    ClassDB::bind_method(D_METHOD("request_conflicts"), &NipaSession::request_conflicts);
    ClassDB::bind_method(D_METHOD("request_resolve", "clone_path", "choice", "content"), &NipaSession::request_resolve);
    ClassDB::bind_method(D_METHOD("request_merge_godot", "clone_path"), &NipaSession::request_merge_godot);
    ClassDB::bind_method(D_METHOD("request_prepare_merge_tool", "clone_path"), &NipaSession::request_prepare_merge_tool);
    ClassDB::bind_method(D_METHOD("build_merge_command", "command", "paths"), &NipaSession::build_merge_command);
    ClassDB::bind_method(D_METHOD("has_conflict_markers", "content"), &NipaSession::has_conflict_markers);
    ClassDB::bind_method(D_METHOD("request_history", "clone_path", "start", "limit"), &NipaSession::request_history);
    ClassDB::bind_method(D_METHOD("request_preview", "old_ref", "new_ref", "threshold", "max_size"),
                         &NipaSession::request_preview);
    ClassDB::bind_method(D_METHOD("request_setup_check"), &NipaSession::request_setup_check);
    ClassDB::bind_method(D_METHOD("apply_ignore_template"), &NipaSession::apply_ignore_template);
    ClassDB::bind_method(D_METHOD("get_sidecar_warnings"), &NipaSession::get_sidecar_warnings);
    ClassDB::bind_method(D_METHOD("get_decorations", "res_paths"), &NipaSession::get_decorations);
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
    ADD_SIGNAL(MethodInfo("locks_changed", PropertyInfo(Variant::PACKED_STRING_ARRAY, "guarded_paths")));
    ADD_SIGNAL(MethodInfo("conflicts_changed"));
    ADD_SIGNAL(MethodInfo("tags_changed"));
    ADD_SIGNAL(MethodInfo("warnings_changed"));
    ADD_SIGNAL(MethodInfo("commit_locks_needed", PropertyInfo(Variant::STRING, "message"),
                          PropertyInfo(Variant::PACKED_STRING_ARRAY, "clone_paths")));
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
    project_dir_ = paths::normalize(from_godot(project_dir));

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
    apply_lock_options();
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
    session_->submit_tags();
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
                Dictionary result = to_dictionary(event.data);
                for (const auto& [name, data] : event.bytes) {
                    result[to_godot(name)] = to_bytes(data);
                }
                request->complete(event.ok, to_godot(event.message), result);
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
        case EventKind::ConflictsChanged:
            emit_signal("conflicts_changed");
            break;
        case EventKind::TagsChanged:
            emit_signal("tags_changed");
            break;
        case EventKind::WarningsChanged:
            emit_signal("warnings_changed");
            break;
        case EventKind::LocksChanged: {
            PackedStringArray paths;
            for (const auto& p : event.paths) paths.push_back(to_godot(p));
            emit_signal("locks_changed", paths);
            break;
        }
    }
}

void NipaSession::configure_locks(const PackedStringArray& extensions, bool strict_read_only) {
    lock_extensions_.clear();
    for (int i = 0; i < extensions.size(); ++i) {
        lock_extensions_.push_back(from_godot(extensions[i]));
    }
    strict_read_only_ = strict_read_only;
    lock_options_set_ = true;
    apply_lock_options();
}

void NipaSession::apply_lock_options() {
    if (!session_ || !lock_options_set_) {
        return;
    }
    session_->set_lock_options(lock_extensions_, strict_read_only_, project_dir_ + "/.godot/nipa/readonly.json");
}

Dictionary NipaSession::get_lock_info(const String& clone_path) const {
    Dictionary out;
    out["state"] = "free";
    out["holder"] = "";
    out["lock_path"] = "";
    out["relevant"] = false;
    if (!session_) {
        return out;
    }
    auto policy = session_->locks();
    std::string path = from_godot(clone_path);
    auto info = policy.lookup(path);
    out["state"] = locks::state_name(info.state);
    out["holder"] = to_godot(info.holder);
    out["lock_path"] = to_godot(info.lock_path);
    out["relevant"] = policy.lock_relevant(path);
    return out;
}

Array NipaSession::get_locks() const {
    Array out;
    if (!session_) {
        return out;
    }
    auto policy = session_->locks();
    for (const auto& l : policy.scoped()) {
        Dictionary item;
        item["id"] = to_godot(l.id);
        item["path"] = to_godot(l.path);
        item["project_path"] = to_project_path(to_godot(l.path));
        item["branch"] = to_godot(l.branch);
        item["global"] = l.global;
        item["holder"] = to_godot(l.holder());
        item["mine"] = policy.is_mine(l);
        item["can_release"] = policy.can_release(l);
        out.push_back(item);
    }
    return out;
}

Ref<NipaRequest> NipaSession::request_lock(const PackedStringArray& clone_paths) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    std::vector<std::string> list;
    PackedStringArray expanded = expand_sidecars(clone_paths);
    for (int i = 0; i < expanded.size(); ++i) list.push_back(from_godot(expanded[i]));
    return track(session_->submit_lock(std::move(list)));
}

Ref<NipaRequest> NipaSession::request_unlock(const PackedStringArray& clone_paths, bool any_holder) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    std::vector<std::string> list;
    for (int i = 0; i < clone_paths.size(); ++i) list.push_back(from_godot(clone_paths[i]));
    return track(session_->submit_unlock(std::move(list), any_holder));
}

Ref<NipaRequest> NipaSession::request_lock_refresh() {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    session_->submit_locks();
    return done_request(true, "refreshing locks");
}

Ref<NipaRequest> NipaSession::track_or_closed(const std::function<std::int64_t(session::Session&)>& submit) {
    if (!session_) {
        return done_request(false, "session is not open");
    }
    return track(submit(*session_));
}

PackedStringArray NipaSession::get_tag_names() const {
    PackedStringArray out;
    if (session_) {
        for (const auto& tag : session_->tags()) {
            out.push_back(to_godot(tag.name));
        }
    }
    return out;
}

Dictionary NipaSession::get_head() const {
    Dictionary out;
    status::Snapshot snapshot;
    if (session_) {
        snapshot = session_->status_cache().snapshot();
    }
    out["branch"] = to_godot(snapshot.branch);
    out["detached"] = snapshot.detached();
    out["head_kind"] = to_godot(snapshot.head_kind);
    out["head_name"] = to_godot(snapshot.head_name);
    out["pending"] = to_godot(snapshot.pending);
    out["pending_remaining"] = snapshot.pending_remaining;
    return out;
}

Dictionary NipaSession::get_conflicts() const {
    return session_ ? to_dictionary(conflicts::to_json(session_->conflicts())) : Dictionary();
}

String NipaSession::get_commit_link(const String& commit_id) const {
    if (!session_ || session_->remotes().empty()) {
        return String();
    }
    return to_godot(history::commit_link(session_->remotes().front(), from_godot(commit_id)));
}

Ref<NipaRequest> NipaSession::request_switch_tag(const String& tag) {
    return track_or_closed([&](session::Session& s) { return s.submit_switch({}, from_godot(tag)); });
}

Ref<NipaRequest> NipaSession::request_merge(const String& source_branch) {
    return track_or_closed([&](session::Session& s) { return s.submit_merge(from_godot(source_branch)); });
}

Ref<NipaRequest> NipaSession::request_merge_abort() {
    return track_or_closed([](session::Session& s) { return s.submit_merge_abort(); });
}

Ref<NipaRequest> NipaSession::request_revert(const String& target) {
    return track_or_closed(
        [&](session::Session& s) { return s.submit_revert(session::Session::RevertAction::Start, from_godot(target)); });
}

Ref<NipaRequest> NipaSession::request_revert_continue() {
    return track_or_closed([](session::Session& s) { return s.submit_revert(session::Session::RevertAction::Continue); });
}

Ref<NipaRequest> NipaSession::request_revert_skip() {
    return track_or_closed([](session::Session& s) { return s.submit_revert(session::Session::RevertAction::Skip); });
}

Ref<NipaRequest> NipaSession::request_revert_abort() {
    return track_or_closed([](session::Session& s) { return s.submit_revert(session::Session::RevertAction::Abort); });
}

Ref<NipaRequest> NipaSession::request_conflicts() {
    return track_or_closed([](session::Session& s) { return s.submit_conflicts(); });
}

Ref<NipaRequest> NipaSession::request_resolve(const String& clone_path, const String& choice,
                                              const PackedByteArray& content) {
    return track_or_closed([&](session::Session& s) {
        return s.submit_resolve(from_godot(clone_path), from_godot(choice), from_bytes(content));
    });
}

Ref<NipaRequest> NipaSession::request_merge_godot(const String& clone_path) {
    return track_or_closed([&](session::Session& s) { return s.submit_merge_godot(from_godot(clone_path)); });
}

Ref<NipaRequest> NipaSession::request_prepare_merge_tool(const String& clone_path) {
    return track_or_closed([&](session::Session& s) {
        return s.submit_prepare_merge_tool(from_godot(clone_path), project_dir_ + "/.godot/nipa/merge");
    });
}

PackedStringArray NipaSession::build_merge_command(const String& command, const Dictionary& paths) const {
    PackedStringArray out;
    conflicts::ToolPaths tool{from_godot(paths.get("base", "")), from_godot(paths.get("local", "")),
                              from_godot(paths.get("remote", "")), from_godot(paths.get("merged", ""))};
    for (const auto& word : conflicts::build_merge_command(from_godot(command), tool)) {
        out.push_back(to_godot(word));
    }
    return out;
}

bool NipaSession::has_conflict_markers(const PackedByteArray& content) const {
    return conflicts::has_conflict_markers(from_bytes(content));
}

Ref<NipaRequest> NipaSession::request_history(const String& clone_path, const String& start, int limit) {
    return track_or_closed(
        [&](session::Session& s) { return s.submit_history(from_godot(clone_path), from_godot(start), limit); });
}

Ref<NipaRequest> NipaSession::request_preview(const Dictionary& old_ref, const Dictionary& new_ref, int threshold,
                                              int max_size) {
    return track_or_closed(
        [&](session::Session& s) { return s.submit_preview(blob_ref(old_ref), blob_ref(new_ref), threshold, max_size); });
}

Ref<NipaRequest> NipaSession::request_setup_check() {
    if (!session_ || !paths_) {
        return done_request(false, "session is not open");
    }
    auto* settings = ProjectSettings::get_singleton();
    setup::Inputs base;
    base.vcs_plugin_name = from_godot(String(settings->get_setting("editor/version_control/plugin_name", "")));
    base.vcs_autoload = bool(settings->get_setting("editor/version_control/autoload_on_startup", false));
    base.project_prefix = paths_->project_prefix();
    return track(session_->submit_setup_check(std::move(base)));
}

Dictionary NipaSession::apply_ignore_template() {
    Dictionary out;
    out["ok"] = false;
    out["changed"] = false;
    out["message"] = "";
    if (!paths_) {
        out["message"] = "session is not open";
        return out;
    }
    auto result = setup::write_ignore(paths_->clone_root(), paths_->project_prefix());
    out["ok"] = result.ok;
    out["changed"] = result.changed;
    if (!result.ok) {
        out["message"] = to_godot(result.error);
    } else {
        out["message"] = result.changed ? ".nipaignore updated." : ".nipaignore already has the Godot patterns.";
    }
    if (session_) {
        session_->submit_status();
    }
    return out;
}

Array NipaSession::get_sidecar_warnings() const {
    Array out;
    if (!session_) {
        return out;
    }
    for (const auto& w : session_->sidecar_warnings()) {
        Dictionary item;
        item["path"] = to_godot(w.path);
        item["kind"] = to_godot(w.kind);
        item["related"] = to_godot(w.related);
        item["message"] = to_godot(w.message);
        item["stageable"] = w.stageable();
        out.push_back(item);
    }
    return out;
}

Dictionary NipaSession::get_decorations(const PackedStringArray& res_paths) const {
    Dictionary out;
    if (!session_ || !paths_) {
        return out;
    }
    decor::Index index(session_->status_cache().snapshot(), session_->locks(), *paths_);
    for (int i = 0; i < res_paths.size(); ++i) {
        auto decoration = index.lookup(from_godot(res_paths[i]));
        if (decoration.empty()) {
            continue;
        }
        Dictionary item;
        item["color"] = to_godot(decoration.color);
        item["tooltip"] = to_godot(decoration.tooltip);
        out[res_paths[i]] = item;
    }
    return out;
}

}  // namespace nipa::glue
