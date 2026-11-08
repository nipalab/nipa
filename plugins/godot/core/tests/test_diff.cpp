#include <doctest.h>

#include <string>

#include "core/diff/diff.h"

using namespace nipa::diff;

namespace {

// Replays a diff: keeps context and removals for the old side, context and additions for the new side.
std::pair<std::string, std::string> replay(const std::vector<Hunk>& hunks) {
    std::string old_side;
    std::string new_side;
    for (const auto& hunk : hunks) {
        for (const auto& line : hunk.lines) {
            if (line.status != '+') old_side += line.content + "\n";
            if (line.status != '-') new_side += line.content + "\n";
        }
    }
    return {old_side, new_side};
}

std::string join(const std::vector<std::string>& lines) {
    std::string out;
    for (const auto& line : lines) {
        out += line + "\n";
    }
    return out;
}

}  // namespace

TEST_CASE("split_lines handles empty, CRLF and missing final newline") {
    CHECK(split_lines("").empty());
    CHECK(split_lines("a\n") == std::vector<std::string>{"a"});
    CHECK(split_lines("a\r\nb") == std::vector<std::string>{"a", "b"});
    CHECK(split_lines("\n\n") == std::vector<std::string>{"", ""});
}

TEST_CASE("identical texts produce no hunks") {
    CHECK(line_diff("one\ntwo\n", "one\ntwo\n").empty());
    CHECK(line_diff("", "").empty());
}

TEST_CASE("an empty old side is all additions") {
    auto hunks = line_diff("", "x\ny\n");
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].old_lines == 0);
    CHECK(hunks[0].new_lines == 2);
    CHECK(hunks[0].old_start == 0);
    CHECK(hunks[0].new_start == 1);
    for (const auto& line : hunks[0].lines) {
        CHECK(line.status == '+');
        CHECK(line.old_no == -1);
    }
}

TEST_CASE("an empty new side is all removals") {
    auto hunks = line_diff("x\ny\n", "");
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].old_lines == 2);
    CHECK(hunks[0].new_lines == 0);
    CHECK(hunks[0].new_start == 0);
    CHECK(hunks[0].lines[0].status == '-');
    CHECK(hunks[0].lines[0].new_no == -1);
}

TEST_CASE("every line changed produces one hunk with numbered lines") {
    auto hunks = line_diff("a\nb\n", "c\nd\n");
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].old_start == 1);
    CHECK(hunks[0].new_start == 1);
    CHECK(hunks[0].old_lines == 2);
    CHECK(hunks[0].new_lines == 2);
    CHECK(hunks[0].lines.size() == 4);
}

TEST_CASE("changes far apart form separate hunks with context") {
    std::string old_text;
    for (int i = 1; i <= 30; ++i) {
        old_text += "line " + std::to_string(i) + "\n";
    }
    std::string new_text = old_text;
    new_text.replace(new_text.find("line 3\n"), 6, "changed 3");
    new_text.replace(new_text.find("line 25\n"), 7, "changed 25");

    auto hunks = line_diff(old_text, new_text);
    REQUIRE(hunks.size() == 2);
    CHECK(hunks[0].old_start == 1);
    CHECK(hunks[1].old_start > hunks[0].old_start);

    auto narrow = line_diff(old_text, new_text, 0);
    CHECK(narrow.size() == 2);
    CHECK(narrow[0].lines.size() == 2);
}

TEST_CASE("nearby changes merge into one hunk") {
    auto hunks = line_diff("a\nb\nc\nd\n", "A\nb\nC\nd\n", 3);
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].lines.size() == 6);
}

TEST_CASE("CRLF and LF lines compare equal") {
    CHECK(line_diff("a\r\nb\r\n", "a\nb\n").empty());
    auto hunks = line_diff("a\r\n", "b\r\n");
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].lines[0].content == "a");
}

TEST_CASE("a missing final newline still diffs the last line") {
    auto hunks = line_diff("a\nb", "a\nc");
    REQUIRE(hunks.size() == 1);
    CHECK(hunks[0].lines.back().content == "c");
}

TEST_CASE("generated edits replay to the original texts") {
    const char* words[] = {"alpha", "beta", "gamma", "delta", "eps"};
    for (int round = 0; round < 200; ++round) {
        std::vector<std::string> a;
        std::vector<std::string> b;
        for (int i = 0; i < 12; ++i) a.push_back(words[(round * 31 + i * i + i * 7) % 5]);
        for (int i = 0; i < 12; ++i) b.push_back(words[(round * 17 + i * 3 + 1) % 5]);
        auto full = replay(line_diff(join(a), join(b), 1000));
        CHECK(full.first == join(a));
        CHECK(full.second == join(b));
        auto old_lines = split_lines(join(a));
        auto new_lines = split_lines(join(b));
        for (const auto& hunk : line_diff(join(a), join(b), 1 + round % 3)) {
            for (const auto& line : hunk.lines) {
                if (line.old_no > 0) CHECK(old_lines[static_cast<std::size_t>(line.old_no - 1)] == line.content);
                if (line.new_no > 0) CHECK(new_lines[static_cast<std::size_t>(line.new_no - 1)] == line.content);
            }
        }
    }
}

TEST_CASE("very large rewrites fall back to a replace and still replay") {
    std::string old_text;
    std::string new_text;
    for (int i = 0; i < 2500; ++i) {
        old_text += "old " + std::to_string(i) + "\n";
        new_text += "new " + std::to_string(i) + "\n";
    }
    auto full = replay(line_diff(old_text, new_text, 0));
    CHECK(full.first == old_text);
    CHECK(full.second == new_text);
}

TEST_CASE("placeholder hunk names the kind and sizes") {
    File file;
    file.kind = "image";
    file.old_size = 100;
    file.new_size = 250;
    file.omitted = true;
    CHECK(file.placeholder());
    auto hunk = placeholder_hunk(file);
    REQUIRE(hunk.lines.size() == 1);
    CHECK(hunk.lines[0].content.find("Binary image: 100 \xE2\x86\x92 250") == 0);
}

TEST_CASE("text files are not placeholders unless omitted") {
    File file;
    file.kind = "text";
    CHECK_FALSE(file.placeholder());
    file.kind = "opaque";
    CHECK(file.placeholder());
}
