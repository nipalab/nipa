#include <doctest.h>

#include "core/status/status.h"

using namespace nipa::status;

namespace {

std::vector<VcsEntry> entries_for(const Snapshot& snapshot) {
    return to_vcs_entries(snapshot);
}

bool has(const std::vector<VcsEntry>& entries, const std::string& path, ChangeType change, TreeArea area) {
    for (const auto& entry : entries) {
        if (entry.path == path && entry.change == change && entry.area == area) {
            return true;
        }
    }
    return false;
}

}  // namespace

TEST_CASE("added files are NEW and staged") {
    Snapshot s;
    s.added = {"new.png"};
    s.staged = {"new.png"};
    auto out = entries_for(s);
    REQUIRE(out.size() == 1);
    CHECK(has(out, "new.png", kNew, kStaged));
}

TEST_CASE("staged and deleted is DELETED staged") {
    Snapshot s;
    s.staged = {"gone.tscn"};
    s.deleted = {"gone.tscn"};
    CHECK(has(entries_for(s), "gone.tscn", kDeleted, kStaged));
}

TEST_CASE("staged deletion reported only in deleted is DELETED staged") {
    Snapshot s;
    s.deleted = {"removed.tscn"};
    CHECK(has(entries_for(s), "removed.tscn", kDeleted, kStaged));
}

TEST_CASE("other staged files are MODIFIED staged") {
    Snapshot s;
    s.staged = {"level.tscn"};
    CHECK(has(entries_for(s), "level.tscn", kModified, kStaged));
}

TEST_CASE("modified and not staged is MODIFIED unstaged") {
    Snapshot s;
    s.modified = {"a.gd", "b.gd"};
    s.staged = {"b.gd"};
    auto out = entries_for(s);
    CHECK(has(out, "a.gd", kModified, kUnstaged));
    CHECK(has(out, "b.gd", kModified, kStaged));
    CHECK_FALSE(has(out, "b.gd", kModified, kUnstaged));
}

TEST_CASE("untracked files are NEW unstaged") {
    Snapshot s;
    s.untracked = {"notes.txt"};
    CHECK(has(entries_for(s), "notes.txt", kNew, kUnstaged));
}

TEST_CASE("missing files are DELETED unstaged") {
    Snapshot s;
    s.missing = {"old.wav"};
    CHECK(has(entries_for(s), "old.wav", kDeleted, kUnstaged));
}

TEST_CASE("unresolved conflicts are UNMERGED and resolved ones are MODIFIED staged") {
    Snapshot s;
    s.conflicts = {"open.tscn", "fixed.tscn"};
    s.resolved = {"fixed.tscn"};
    auto out = entries_for(s);
    CHECK(has(out, "open.tscn", kUnmerged, kUnstaged));
    CHECK(has(out, "fixed.tscn", kModified, kStaged));
    CHECK_FALSE(has(out, "fixed.tscn", kUnmerged, kUnstaged));
}

TEST_CASE("resolved files are not reported as staged twice") {
    Snapshot s;
    s.staged = {"fixed.tscn"};
    s.resolved = {"fixed.tscn"};
    s.conflicts = {"fixed.tscn"};
    auto out = entries_for(s);
    CHECK(out.size() == 1);
    CHECK(has(out, "fixed.tscn", kModified, kStaged));
}

TEST_CASE("a staged file that is modified on disk is listed once as staged") {
    Snapshot s;
    s.staged = {"x.tres"};
    s.modified = {"x.tres"};
    auto out = entries_for(s);
    CHECK(out.size() == 1);
    CHECK(has(out, "x.tres", kModified, kStaged));
}

TEST_CASE("entries are sorted by path then area") {
    Snapshot s;
    s.untracked = {"b.txt", "a.txt"};
    s.staged = {"a.txt"};
    auto out = entries_for(s);
    REQUIRE(out.size() == 3);
    CHECK(out[0].path == "a.txt");
    CHECK(out[0].area == kStaged);
    CHECK(out[1].path == "a.txt");
    CHECK(out[1].area == kUnstaged);
    CHECK(out[2].path == "b.txt");
}

TEST_CASE("empty snapshot has no entries and detached state is visible") {
    Snapshot s;
    CHECK(entries_for(s).empty());
    CHECK_FALSE(s.detached());
    s.head_kind = "tag";
    s.head_name = "v1";
    CHECK(s.detached());
}

TEST_CASE("cache reports changed paths between snapshots") {
    StatusCache cache;
    CHECK_FALSE(cache.loaded());

    Snapshot first;
    first.untracked = {"a.txt", "b.txt"};
    auto initial = cache.apply(first);
    CHECK(cache.loaded());
    CHECK(initial == std::vector<std::string>{"a.txt", "b.txt"});

    Snapshot second;
    second.untracked = {"b.txt"};
    second.staged = {"c.txt"};
    auto changed = cache.apply(second);
    CHECK(changed == std::vector<std::string>{"a.txt", "c.txt"});
    CHECK(cache.entries().size() == 2);
    CHECK(cache.snapshot().staged == std::vector<std::string>{"c.txt"});

    auto unchanged = cache.apply(second);
    CHECK(unchanged.empty());
}
