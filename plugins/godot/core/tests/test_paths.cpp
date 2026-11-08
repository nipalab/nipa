#include <doctest.h>

#include <set>

#include "core/paths/project_paths.h"

using nipa::paths::expand_with_sidecars;
using nipa::paths::normalize;
using nipa::paths::ProjectPaths;
using nipa::paths::sidecar_candidates;

namespace {

nipa::paths::FileExists existing(std::set<std::string> files) {
    return [files](const std::string& path) { return files.count(path) > 0; };
}

}  // namespace

TEST_CASE("project at the clone root maps paths one to one") {
    auto found = ProjectPaths::locate("/work/clone", existing({"/work/clone/.nipa/config"}));
    REQUIRE(found.has_value());
    CHECK(found->clone_root() == "/work/clone");
    CHECK(found->project_prefix().empty());
    CHECK(found->to_clone("res://scenes/main.tscn") == "scenes/main.tscn");
    CHECK(found->to_clone("scenes/main.tscn") == "scenes/main.tscn");
    CHECK(found->to_project("scenes/main.tscn") == std::optional<std::string>("scenes/main.tscn"));
    CHECK(ProjectPaths::to_res("scenes/main.tscn") == "res://scenes/main.tscn");
}

TEST_CASE("project in a subfolder of the clone maps both ways") {
    auto found = ProjectPaths::locate("/work/clone/games/demo/",
                                      existing({"/work/clone/.nipa/config"}));
    REQUIRE(found.has_value());
    CHECK(found->clone_root() == "/work/clone");
    CHECK(found->project_prefix() == "games/demo");
    CHECK(found->to_clone("res://player.gd") == "games/demo/player.gd");
    CHECK(found->to_clone("player.gd") == "games/demo/player.gd");
    CHECK(found->to_project("games/demo/player.gd") == std::optional<std::string>("player.gd"));
    CHECK(found->to_project("games/demo") == std::nullopt);
    CHECK(found->to_project("other/file.txt") == std::nullopt);
    CHECK(found->outside_project("README.md"));
    CHECK_FALSE(found->outside_project("games/demo/icon.png"));
}

TEST_CASE("locate walks up at most 32 parents") {
    std::string deep = "/a";
    for (int i = 0; i < 40; ++i) {
        deep += "/d";
    }
    CHECK_FALSE(ProjectPaths::locate(deep, existing({"/a/.nipa/config"})).has_value());

    std::string shallow = "/a/b/c";
    auto found = ProjectPaths::locate(shallow, existing({"/a/.nipa/config"}));
    REQUIRE(found.has_value());
    CHECK(found->project_prefix() == "b/c");
}

TEST_CASE("locate finds the clone from the filesystem root") {
    auto found = ProjectPaths::locate("/proj", existing({"/.nipa/config"}));
    REQUIRE(found.has_value());
    CHECK(found->clone_root() == "/");
    CHECK(found->project_prefix() == "proj");
}

TEST_CASE("locate without a clone returns nothing") {
    CHECK_FALSE(ProjectPaths::locate("/nowhere/at/all", existing({})).has_value());
    CHECK_FALSE(ProjectPaths::locate("", existing({})).has_value());
}

TEST_CASE("windows separators are normalized everywhere") {
    CHECK(normalize("games\\demo\\a.gd") == "games/demo/a.gd");
    auto found = ProjectPaths::locate("C:\\work\\clone\\games\\demo", existing({"C:/work/clone/.nipa/config"}));
    REQUIRE(found.has_value());
    CHECK(found->clone_root() == "C:/work/clone");
    CHECK(found->project_prefix() == "games/demo");
    CHECK(found->to_clone("res://scenes\\main.tscn") == "games/demo/scenes/main.tscn");
    CHECK(found->to_project("games\\demo\\scenes\\main.tscn") == std::optional<std::string>("scenes/main.tscn"));
}

TEST_CASE("sidecar candidates pair assets and scripts") {
    CHECK(sidecar_candidates("art/hero.png") == std::vector<std::string>{"art/hero.png.import", "art/hero.png.uid"});
    CHECK(sidecar_candidates("player.gd").back() == "player.gd.uid");
}

TEST_CASE("sidecars are added only when they exist") {
    std::set<std::string> on_disk{"art/hero.png.import", "scripts/a.gd.uid", "art/other.png.uid"};
    auto out = expand_with_sidecars({"art/hero.png", "scripts/a.gd", "art/hero.png", "scripts\\a.gd"}, on_disk);
    CHECK(out == std::vector<std::string>{"art/hero.png", "scripts/a.gd", "art/hero.png.import", "scripts/a.gd.uid"});
    CHECK(expand_with_sidecars({"plain.txt"}, on_disk) == std::vector<std::string>{"plain.txt"});
    CHECK(expand_with_sidecars({}, on_disk).empty());
}
