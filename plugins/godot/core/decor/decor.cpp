#include "core/decor/decor.h"

#include <algorithm>

namespace nipa::decor {
namespace {

constexpr const char* kResPrefix = "res://";

std::string join(const std::vector<std::string>& parts) {
    std::string out;
    for (const auto& part : parts) {
        out += (out.empty() ? "" : "; ") + part;
    }
    return out;
}

Decoration make(Kind kind, const std::vector<std::string>& notes) {
    if (kind == Kind::None) {
        return {};
    }
    return {kind, color_of(kind), "Nipa: " + join(notes)};
}

}  // namespace

const char* color_of(Kind kind) {
    switch (kind) {
        case Kind::Untracked:
            return "#a5d6a7";
        case Kind::Added:
            return "#7ee787";
        case Kind::Modified:
        case Kind::Folder:
            return "#e3b341";
        case Kind::Deleted:
            return "#f97583";
        case Kind::Conflicted:
            return "#ff5c5c";
        case Kind::LockedMine:
            return "#79c0ff";
        case Kind::LockedTheirs:
            return "#ff9f43";
        case Kind::None:
            break;
    }
    return "";
}

Index::Index(const status::Snapshot& snapshot, const locks::LockPolicy& locks, paths::ProjectPaths paths)
    : locks_(locks), paths_(std::move(paths)) {
    for (const auto& entry : status::to_vcs_entries(snapshot)) {
        if (files_.empty() || files_.back().path != entry.path) {
            files_.push_back(FileState{entry.path, false, entry.change, false});
        }
        auto& state = files_.back();
        if (entry.change == status::kUnmerged) {
            state.conflicted = true;
        }
        if (entry.area == status::kStaged) {
            state.staged = true;
            state.change = entry.change;
        } else if (!state.staged) {
            state.change = entry.change;
        }
    }
}

Decoration Index::lookup(const std::string& res_path) const {
    std::string path = paths::normalize(res_path);
    if (path.rfind(kResPrefix, 0) != 0) {
        return {};
    }
    std::string clone = paths_.to_clone(path);
    if (path.back() == '/') {
        return folder(clone);
    }
    return clone.empty() ? Decoration{} : file(clone);
}

std::string Index::lock_note(const locks::LockInfo& info) const {
    if (info.state == locks::LockState::Mine) {
        return "locked by you";
    }
    if (info.state == locks::LockState::Theirs) {
        return "locked by " + (info.holder.empty() ? std::string("someone else") : info.holder);
    }
    return {};
}

Decoration Index::file(const std::string& clone_path) const {
    std::vector<std::string> notes;
    Kind kind = Kind::None;
    auto found = std::lower_bound(files_.begin(), files_.end(), clone_path,
                                  [](const FileState& f, const std::string& p) { return f.path < p; });
    if (found != files_.end() && found->path == clone_path) {
        if (found->conflicted) {
            kind = Kind::Conflicted;
            notes.emplace_back("conflicted");
        } else if (found->change == status::kNew) {
            kind = found->staged ? Kind::Added : Kind::Untracked;
            notes.emplace_back(found->staged ? "new (staged)" : "new (untracked)");
        } else {
            kind = found->change == status::kDeleted ? Kind::Deleted : Kind::Modified;
            notes.push_back(std::string(kind == Kind::Deleted ? "deleted" : "modified") +
                            (found->staged ? " (staged)" : ""));
        }
    }
    auto info = locks_.loaded() ? locks_.lookup(clone_path) : locks::LockInfo{};
    auto note = lock_note(info);
    if (!note.empty()) {
        notes.push_back(note);
        if (info.state == locks::LockState::Theirs && kind != Kind::Conflicted) {
            kind = Kind::LockedTheirs;
        } else if (info.state == locks::LockState::Mine && kind == Kind::None) {
            kind = Kind::LockedMine;
        }
    }
    return make(kind, notes);
}

Decoration Index::folder(const std::string& clone_dir) const {
    std::string head = clone_dir.empty() ? std::string() : clone_dir + "/";
    int changed = 0;
    int conflicted = 0;
    for (auto it = std::lower_bound(files_.begin(), files_.end(), head,
                                    [](const FileState& f, const std::string& p) { return f.path < p; });
         it != files_.end() && it->path.compare(0, head.size(), head) == 0; ++it) {
        ++changed;
        conflicted += it->conflicted ? 1 : 0;
    }
    std::vector<std::string> notes;
    Kind kind = Kind::None;
    if (changed > 0) {
        kind = conflicted > 0 ? Kind::Conflicted : Kind::Folder;
        notes.push_back(std::to_string(changed) + " changed file(s)" +
                        (conflicted > 0 ? ", " + std::to_string(conflicted) + " conflicted" : ""));
    }
    auto info = locks_.loaded() && !clone_dir.empty() ? locks_.lookup(clone_dir) : locks::LockInfo{};
    auto note = lock_note(info);
    if (!note.empty()) {
        notes.push_back(note);
        if (info.state == locks::LockState::Theirs && kind != Kind::Conflicted) {
            kind = Kind::LockedTheirs;
        } else if (info.state == locks::LockState::Mine && kind == Kind::None) {
            kind = Kind::LockedMine;
        }
    }
    return make(kind, notes);
}

}  // namespace nipa::decor
