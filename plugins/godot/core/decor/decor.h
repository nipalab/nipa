#pragma once

#include <string>
#include <vector>

#include "core/locks/lock_policy.h"
#include "core/paths/project_paths.h"
#include "core/status/status.h"

namespace nipa::decor {

enum class Kind { None, Untracked, Added, Modified, Deleted, Conflicted, LockedMine, LockedTheirs, Folder };

// "#rrggbb" for a kind; empty for None.
const char* color_of(Kind kind);

struct Decoration {
    Kind kind = Kind::None;
    std::string color;
    std::string tooltip;

    bool empty() const { return kind == Kind::None; }
};

// FileSystem dock decorations from the status and lock caches. Folders (paths
// ending in '/', like the dock's item metadata) summarize the changes below them.
class Index {
public:
    Index(const status::Snapshot& snapshot, const locks::LockPolicy& locks, paths::ProjectPaths paths);

    // `res_path`: res://file or res://folder/; anything else has no decoration.
    Decoration lookup(const std::string& res_path) const;

private:
    struct FileState {
        std::string path;
        bool conflicted = false;
        status::ChangeType change = status::kModified;
        bool staged = false;
    };

    Decoration file(const std::string& clone_path) const;
    Decoration folder(const std::string& clone_dir) const;
    std::string lock_note(const locks::LockInfo& info) const;

    std::vector<FileState> files_;  // sorted by path
    locks::LockPolicy locks_;
    paths::ProjectPaths paths_;
};

}  // namespace nipa::decor
