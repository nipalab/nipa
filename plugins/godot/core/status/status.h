#pragma once

#include <string>
#include <vector>

namespace nipa::status {

// Godot's EditorVCSInterface::ChangeType values.
enum ChangeType { kNew = 0, kModified = 1, kRenamed = 2, kDeleted = 3, kTypechange = 4, kUnmerged = 5 };

// Godot's EditorVCSInterface::TreeArea values.
enum TreeArea { kCommit = 0, kStaged = 1, kUnstaged = 2 };

// Mirrors daemon StatusResponse, paths are clone-relative.
struct Snapshot {
    std::vector<std::string> staged;
    std::vector<std::string> deleted;
    std::vector<std::string> modified;
    std::vector<std::string> untracked;
    std::vector<std::string> missing;
    std::vector<std::string> conflicts;
    std::vector<std::string> resolved;
    std::vector<std::string> added;
    std::string branch;
    std::string head_kind;
    std::string head_name;
    std::string pending;
    int pending_remaining = 0;

    bool detached() const { return !head_kind.empty(); }
};

struct VcsEntry {
    std::string path;
    ChangeType change;
    TreeArea area;

    bool operator==(const VcsEntry& other) const {
        return path == other.path && change == other.change && area == other.area;
    }
};

// One entry per (path, area), sorted by path then area. Mapping per
// docs/GODOT_PLUGIN.md; a conflict that is not resolved is UNMERGED.
std::vector<VcsEntry> to_vcs_entries(const Snapshot& snapshot);

class StatusCache {
public:
    // Replaces the snapshot and returns the clone-relative paths whose entry changed.
    std::vector<std::string> apply(Snapshot snapshot);

    bool loaded() const { return loaded_; }
    const Snapshot& snapshot() const { return snapshot_; }
    const std::vector<VcsEntry>& entries() const { return entries_; }

private:
    bool loaded_ = false;
    Snapshot snapshot_;
    std::vector<VcsEntry> entries_;
};

}  // namespace nipa::status
