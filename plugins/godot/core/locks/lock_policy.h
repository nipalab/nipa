#pragma once

#include <set>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace nipa::locks {

// greet.FileLockDetail
struct LockDetail {
    std::string id;
    std::string path;
    std::string branch;
    bool global = false;
    std::string held_by;
    std::string held_by_name;

    const std::string& holder() const { return held_by_name.empty() ? held_by : held_by_name; }
};

// daemon FileLockState: state is mine, theirs, needed or free.
struct FileState {
    std::string path;
    std::string state;
    std::string lock_path;
    std::string held_by;
    std::string held_by_name;
    bool tracked = false;

    const std::string& holder() const { return held_by_name.empty() ? held_by : held_by_name; }
};

// daemon LockStatusResponse
struct LockStatus {
    std::string user_id;
    std::string branch;
    bool default_branch = false;
    bool can_release_any = false;
    std::vector<LockDetail> locks;
    std::vector<FileState> files;
};

LockStatus parse_lock_status(const nlohmann::json& j);

enum class LockState { Free, Mine, Theirs };

const char* state_name(LockState state);

struct LockInfo {
    LockState state = LockState::Free;
    std::string holder;
    std::string lock_path;
};

struct PushCheck {
    std::vector<std::string> needed;
    std::vector<FileState> theirs;

    bool blocked() const { return !needed.empty() || !theirs.empty(); }
    // "a.png (Ann), b.wav (Bob)"
    std::string theirs_summary() const;
};

// Default lock-relevant extensions (docs/GODOT_PLUGIN.md, nipa/locking/extensions).
std::vector<std::string> default_extensions();

// True when `lock_path` is `path` or a directory prefix of it (server domain.PrefixCovers).
bool covers(const std::string& lock_path, const std::string& path);

// Port of the Unity LockPolicy over clone-relative paths. An asset and its
// `.import`/`.uid` sidecars are one lock unit.
class LockPolicy {
public:
    LockPolicy();
    LockPolicy(LockStatus status, const std::vector<std::string>& extensions);

    bool loaded() const { return loaded_; }
    const LockStatus& status() const { return status_; }
    const std::vector<LockDetail>& scoped() const { return scoped_; }

    // The branch a new lock is taken on: empty (global) on the default branch.
    std::string lock_branch() const;

    bool in_scope(const LockDetail& lock) const;
    bool is_mine(const LockDetail& lock) const;
    bool can_release(const LockDetail& lock) const;

    LockInfo lookup(const std::string& clone_path) const;
    bool held_by_me(const std::string& clone_path) const;

    // Extension in the configured list, or a changed binary LockStatus reports.
    bool lock_relevant(const std::string& clone_path) const;

    PushCheck check_push() const;

    // Paths of in-scope locks held by someone else.
    std::set<std::string> theirs_paths() const;

    // In-scope locks I may release whose asset matches one of `clone_paths`.
    std::vector<LockDetail> releasable(const std::vector<std::string>& clone_paths, bool any_holder) const;

private:
    bool loaded_ = false;
    LockStatus status_;
    std::set<std::string> extensions_;
    std::vector<LockDetail> scoped_;
};

}  // namespace nipa::locks
