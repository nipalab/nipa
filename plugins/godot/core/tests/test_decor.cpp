#include <doctest.h>

#include "core/decor/decor.h"

using nipa::decor::color_of;
using nipa::decor::Index;
using nipa::decor::Kind;
using nipa::locks::LockDetail;
using nipa::locks::LockPolicy;
using nipa::locks::LockStatus;
using nipa::paths::ProjectPaths;

namespace {

LockDetail lock(const std::string& path, const std::string& held_by, const std::string& name) {
    LockDetail l;
    l.id = "id-" + path;
    l.path = path;
    l.global = true;
    l.held_by = held_by;
    l.held_by_name = name;
    return l;
}

LockPolicy policy(std::vector<LockDetail> locks) {
    LockStatus s;
    s.user_id = "me";
    s.branch = "main";
    s.default_branch = true;
    s.locks = std::move(locks);
    return LockPolicy(s, {"png"});
}

nipa::status::Snapshot snapshot() {
    nipa::status::Snapshot s;
    s.staged = {"game/art/new.png", "game/art/edit.png", "game/gone.gd"};
    s.added = {"game/art/new.png"};
    s.deleted = {"game/gone.gd"};
    s.modified = {"game/art/edit.png", "game/main.tscn", "outside.txt"};
    s.untracked = {"game/notes.txt"};
    s.conflicts = {"game/levels/l1.tscn"};
    return s;
}

}  // namespace

TEST_CASE("files get their status color and tooltip") {
    Index index(snapshot(), LockPolicy(), ProjectPaths("/w/clone", "game"));
    auto added = index.lookup("res://art/new.png");
    CHECK(added.kind == Kind::Added);
    CHECK(added.color == "#7ee787");
    CHECK(added.tooltip == "Nipa: new (staged)");
    CHECK(index.lookup("res://notes.txt").kind == Kind::Untracked);
    CHECK(index.lookup("res://notes.txt").tooltip == "Nipa: new (untracked)");
    CHECK(index.lookup("res://art/edit.png").tooltip == "Nipa: modified (staged)");
    CHECK(index.lookup("res://main.tscn").tooltip == "Nipa: modified");
    CHECK(index.lookup("res://gone.gd").kind == Kind::Deleted);
    CHECK(index.lookup("res://gone.gd").tooltip == "Nipa: deleted (staged)");
    CHECK(index.lookup("res://levels/l1.tscn").kind == Kind::Conflicted);
    CHECK(index.lookup("res://clean.gd").empty());
    CHECK(index.lookup("res://clean.gd").color.empty());
    CHECK(index.lookup("user://x").empty());
    CHECK(index.lookup("Favorites").empty());
}

TEST_CASE("folders summarize the changes below them") {
    Index index(snapshot(), LockPolicy(), ProjectPaths("/w/clone", "game"));
    auto art = index.lookup("res://art/");
    CHECK(art.kind == Kind::Folder);
    CHECK(art.color == color_of(Kind::Modified));
    CHECK(art.tooltip == "Nipa: 2 changed file(s)");
    auto root = index.lookup("res://");
    CHECK(root.kind == Kind::Conflicted);
    CHECK(root.tooltip == "Nipa: 6 changed file(s), 1 conflicted");
    CHECK(index.lookup("res://empty/").empty());
    CHECK(index.lookup("res://ar/").empty());

    Index at_root(snapshot(), LockPolicy(), ProjectPaths("/w/clone", ""));
    CHECK(at_root.lookup("res://").tooltip == "Nipa: 7 changed file(s), 1 conflicted");
    CHECK(at_root.lookup("res://outside.txt").kind == Kind::Modified);
}

TEST_CASE("locks color files and folders") {
    auto locks = policy({lock("game/art/hero.png", "ann", "Ann"), lock("game/main.tscn", "me", "Me"),
                         lock("game/levels", "bob", ""), lock("game/mine.png", "me", "Me")});
    Index index(snapshot(), locks, ProjectPaths("/w/clone", "game"));
    auto hero = index.lookup("res://art/hero.png");
    CHECK(hero.kind == Kind::LockedTheirs);
    CHECK(hero.tooltip == "Nipa: locked by Ann");
    CHECK(index.lookup("res://art/hero.png.import").kind == Kind::LockedTheirs);
    auto main = index.lookup("res://main.tscn");
    CHECK(main.kind == Kind::Modified);
    CHECK(main.tooltip == "Nipa: modified; locked by you");
    CHECK(index.lookup("res://mine.png").kind == Kind::LockedMine);
    auto level = index.lookup("res://levels/l1.tscn");
    CHECK(level.kind == Kind::Conflicted);
    CHECK(level.tooltip == "Nipa: conflicted; locked by bob");
    auto folder = index.lookup("res://levels/");
    CHECK(folder.kind == Kind::Conflicted);
    CHECK(folder.tooltip == "Nipa: 1 changed file(s), 1 conflicted; locked by bob");
    CHECK(index.lookup("res://levels/other/").kind == Kind::LockedTheirs);

    Index mine_dir(nipa::status::Snapshot{}, policy({lock("game/art", "me", "Me")}), ProjectPaths("/w/clone", "game"));
    CHECK(mine_dir.lookup("res://art/").kind == Kind::LockedMine);
    CHECK(mine_dir.lookup("res://").empty());
}

TEST_CASE("every kind has a color except none") {
    for (auto kind : {Kind::Untracked, Kind::Added, Kind::Modified, Kind::Deleted, Kind::Conflicted, Kind::LockedMine,
                      Kind::LockedTheirs, Kind::Folder}) {
        CHECK(std::string(color_of(kind)).size() == 7);
    }
    CHECK(std::string(color_of(Kind::None)).empty());
}
