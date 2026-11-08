#include <doctest.h>


#include "core/conflicts/merge_tool.h"

namespace c = nipa::conflicts;
using Words = std::vector<std::string>;

TEST_CASE("tool_paths names the temp files after the conflicted file") {
    auto p = c::tool_paths("/tmp/m", "scripts/player.gd");
    CHECK(p.base == "/tmp/m/player.BASE.gd");
    CHECK(p.local == "/tmp/m/player.LOCAL.gd");
    CHECK(p.remote == "/tmp/m/player.REMOTE.gd");
    CHECK(p.merged == "/tmp/m/player.MERGED.gd");
    CHECK(c::tool_paths("d", "Makefile").base == "d/Makefile.BASE");
    CHECK(c::tool_paths("d", ".gitignore").merged == "d/.gitignore.MERGED");
}

TEST_CASE("split_command follows shell quoting") {
    CHECK(*c::split_command("  meld  $LOCAL   $MERGED ") == Words{"meld", "$LOCAL", "$MERGED"});
    CHECK(*c::split_command(R"('/Applications/My Tool' "a \"b\" \\c \d" x\ y '')") ==
          Words{"/Applications/My Tool", R"(a "b" \c \d)", "x y", ""});
    CHECK(c::split_command("")->empty());
    CHECK(*c::split_command("trailing\\") == Words{"trailing\\"});
    CHECK_FALSE(c::split_command("'open"));
    CHECK_FALSE(c::split_command("\"open"));
}

TEST_CASE("build_merge_command substitutes the placeholders per argument") {
    c::ToolPaths paths{"/t/a b.BASE.gd", "/t/l.gd", "/t/r.gd", "/t/m.gd"};
    CHECK(c::build_merge_command("kdiff3 $BASE $LOCAL $REMOTE -o $MERGED", paths) ==
          Words{"kdiff3", "/t/a b.BASE.gd", "/t/l.gd", "/t/r.gd", "-o", "/t/m.gd"});
    CHECK(c::build_merge_command("tool --out=${MERGED} \"$LOCAL\"", paths) == Words{"tool", "--out=/t/m.gd", "/t/l.gd"});
    CHECK(c::build_merge_command("meld", paths) == Words{"meld", "/t/a b.BASE.gd", "/t/l.gd", "/t/r.gd", "/t/m.gd"});
    CHECK(c::build_merge_command("   ", paths).empty());
    CHECK(c::build_merge_command("tool 'open", paths).empty());
}

TEST_CASE("command_line quotes only what needs it") {
    CHECK(c::quote_argument("plain-arg_1.gd") == "plain-arg_1.gd");
    CHECK(c::quote_argument("") == "''");
    CHECK(c::quote_argument("a b") == "'a b'");
    CHECK(c::quote_argument("it's") == "'it'\\''s'");
    CHECK(c::command_line({"meld", "/t/a b", "$x"}) == "meld '/t/a b' '$x'");
}

TEST_CASE("has_conflict_markers looks at line starts") {
    CHECK(c::has_conflict_markers("a\n<<<<<<< ours\nb\n"));
    CHECK(c::has_conflict_markers(">>>>>>> theirs"));
    CHECK_FALSE(c::has_conflict_markers("text = \"<<<<<<< not at start\"\n"));
    CHECK_FALSE(c::has_conflict_markers(""));
    CHECK_FALSE(c::has_conflict_markers("=======\n"));
}
