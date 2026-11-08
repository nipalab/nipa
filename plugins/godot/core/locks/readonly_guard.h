#pragma once

#include <map>
#include <set>
#include <string>
#include <vector>

namespace nipa::locks {

// Strict read-only mode: removes the write permission from files covered by
// someone else's lock so Godot itself fails to save them, and restores the
// original permission once the lock is gone. Only files it made read-only are
// ever restored. The guarded set is persisted to `state_path` (empty = memory
// only) so a crashed editor can give the permission back on the next start.
class ReadOnlyGuard {
public:
    struct Result {
        std::vector<std::string> guarded;
        std::vector<std::string> released;
        std::vector<std::string> failed;

        bool changed() const { return !guarded.empty() || !released.empty(); }
    };

    ReadOnlyGuard(std::string clone_root, std::string state_path);

    void load();

    // `lock_paths` are clone-relative lock paths: a file (with its sidecars) or a directory.
    Result apply(const std::set<std::string>& lock_paths);
    Result release_all();

    std::set<std::string> guarded() const;

private:
    std::set<std::string> expand(const std::set<std::string>& lock_paths) const;
    void save() const;

    std::string clone_root_;
    std::string state_path_;
    std::map<std::string, unsigned> guarded_;
};

}  // namespace nipa::locks
