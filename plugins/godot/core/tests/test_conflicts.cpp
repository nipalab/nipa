#include <doctest.h>

#include "core/conflicts/conflicts.h"
#include "core/daemon/messages.h"

using nipa::conflicts::Action;
using nipa::conflicts::File;
using nlohmann::json;
namespace c = nipa::conflicts;

TEST_CASE("parse_conflicts reads the pending operation and its files") {
    auto set = c::parse_conflicts({{"op", "revert"},
                                   {"base", {{"id", "c1"}, {"label", "fix"}}},
                                   {"ours", {{"id", "c3"}, {"label", "main"}}},
                                   {"theirs", {{"label", "before fix"}}},
                                   {"files",
                                    {{{"path", "a.tscn"}, {"kind", "text"}, {"resolved", true}},
                                     {{"path", "b.png"}, {"kind", "binary"}, {"binary", true}, {"viewer", "image"}}}}});
    CHECK(set.pending());
    CHECK(set.base.id == "c1");
    CHECK(set.ours.label == "main");
    CHECK(set.theirs.id.empty());
    REQUIRE(set.files.size() == 2);
    CHECK(set.files[0].resolved);
    CHECK(set.files[1].binary);
    CHECK(set.open_count() == 1);
    CHECK_FALSE(set.can_finish());
    set.files[1].resolved = true;
    CHECK(set.can_finish());

    auto empty = c::parse_conflicts(json::object());
    CHECK_FALSE(empty.pending());
    CHECK_FALSE(empty.can_finish());
    CHECK(c::parse_conflicts(json::array()).files.empty());
    CHECK(c::parse_conflicts({{"op", "merge"}, {"base", "x"}, {"files", "x"}}).base.id.empty());
}

TEST_CASE("actions per conflicted file") {
    auto names = [](const File& f) {
        std::vector<std::string> out;
        for (auto a : c::actions_for(f)) out.push_back(c::action_name(a));
        return out;
    };
    CHECK(names({"level.tscn", "text", false, false, "scene"}) == std::vector<std::string>{"mine", "theirs", "merge"});
    CHECK(names({"res/Theme.TRES", "add_add", false, false, "scene"}) == std::vector<std::string>{"mine", "theirs", "merge"});
    CHECK(names({"player.gd", "text", false, false, "text"}) == std::vector<std::string>{"mine", "theirs", "external"});
    CHECK(names({"hero.png", "binary", true, false, "image"}) == std::vector<std::string>{"mine", "theirs", "compare"});
    CHECK(names({"hero.png", "modify_delete", true, false, "image"}) == std::vector<std::string>{"mine", "theirs"});
    CHECK(names({"a.tscn", "delete_modify", false, false, "scene"}) == std::vector<std::string>{"mine", "theirs"});
    CHECK(std::string(c::action_name(static_cast<Action>(99))).empty());
    CHECK_FALSE(c::godot_mergeable("dir.tscn/file"));
    CHECK_FALSE(c::godot_mergeable("noext"));
    CHECK(c::godot_mergeable("a/b.tres"));
}

TEST_CASE("parse_godot_merge decodes the merged bytes and labels the conflicts") {
    auto merge = c::parse_godot_merge({{"merged", nipa::daemon::base64_encode("[gd_scene]\n")},
                                       {"clean", true},
                                       {"conflicts",
                                        {{{"section", "node name=\"A\""}, {"key", "position"}, {"message", "m"}},
                                         {{"section", "node name=\"B\""}, {"message", "removed"}},
                                         {{"key", "config_version"}}}}});
    CHECK(merge.merged == "[gd_scene]\n");
    CHECK(merge.clean);
    REQUIRE(merge.conflicts.size() == 3);
    CHECK(merge.conflicts[0].label() == "node name=\"A\" \xc2\xb7 position");
    CHECK(merge.conflicts[1].label() == "node name=\"B\"");
    CHECK(merge.conflicts[2].label() == "config_version");
    auto out = c::to_json(merge);
    CHECK(out["conflicts"][1]["message"] == "removed");
    CHECK_FALSE(c::parse_godot_merge("x").clean);

    c::Set set;
    set.op = "merge";
    set.files.push_back({"a.tscn", "text", false, false, "scene"});
    auto j = c::to_json(set);
    CHECK(j["open"] == 1);
    CHECK(j["can_finish"] == false);
    CHECK(j["files"][0]["actions"] == json::array({"mine", "theirs", "merge"}));
}
