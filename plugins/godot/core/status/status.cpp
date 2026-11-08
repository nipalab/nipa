#include "core/status/status.h"

#include <algorithm>
#include <map>
#include <set>
#include <tuple>

namespace nipa::status {
namespace {

void add(std::map<std::pair<std::string, int>, VcsEntry>& out, const std::string& path, ChangeType change,
         TreeArea area) {
    out.emplace(std::make_pair(path, static_cast<int>(area)), VcsEntry{path, change, area});
}

}  // namespace

std::vector<VcsEntry> to_vcs_entries(const Snapshot& snapshot) {
    std::set<std::string> resolved(snapshot.resolved.begin(), snapshot.resolved.end());
    std::set<std::string> staged(snapshot.staged.begin(), snapshot.staged.end());
    std::set<std::string> deleted(snapshot.deleted.begin(), snapshot.deleted.end());
    std::set<std::string> added(snapshot.added.begin(), snapshot.added.end());

    std::map<std::pair<std::string, int>, VcsEntry> out;
    for (const auto& path : snapshot.conflicts) {
        if (!resolved.count(path)) {
            add(out, path, kUnmerged, kUnstaged);
        }
    }
    for (const auto& path : snapshot.resolved) {
        add(out, path, kModified, kStaged);
    }
    for (const auto& path : snapshot.added) {
        add(out, path, kNew, kStaged);
    }
    for (const auto& path : snapshot.staged) {
        if (added.count(path) || resolved.count(path)) {
            continue;
        }
        add(out, path, deleted.count(path) ? kDeleted : kModified, kStaged);
    }
    for (const auto& path : snapshot.modified) {
        if (!staged.count(path)) {
            add(out, path, kModified, kUnstaged);
        }
    }
    for (const auto& path : snapshot.untracked) {
        add(out, path, kNew, kUnstaged);
    }
    for (const auto& path : snapshot.missing) {
        add(out, path, kDeleted, kUnstaged);
    }

    std::vector<VcsEntry> result;
    result.reserve(out.size());
    for (auto& entry : out) {
        result.push_back(entry.second);
    }
    return result;
}

std::vector<std::string> StatusCache::apply(Snapshot snapshot) {
    auto next = to_vcs_entries(snapshot);
    std::map<std::pair<std::string, int>, VcsEntry> before;
    for (const auto& entry : entries_) {
        before.emplace(std::make_pair(entry.path, static_cast<int>(entry.area)), entry);
    }
    std::map<std::pair<std::string, int>, VcsEntry> after;
    for (const auto& entry : next) {
        after.emplace(std::make_pair(entry.path, static_cast<int>(entry.area)), entry);
    }

    std::set<std::string> changed;
    for (const auto& kv : before) {
        auto found = after.find(kv.first);
        if (found == after.end() || !(found->second == kv.second)) {
            changed.insert(kv.second.path);
        }
    }
    for (const auto& kv : after) {
        auto found = before.find(kv.first);
        if (found == before.end() || !(found->second == kv.second)) {
            changed.insert(kv.second.path);
        }
    }
    if (!loaded_) {
        for (const auto& entry : next) {
            changed.insert(entry.path);
        }
    }

    loaded_ = true;
    snapshot_ = std::move(snapshot);
    entries_ = std::move(next);
    return std::vector<std::string>(changed.begin(), changed.end());
}

}  // namespace nipa::status
