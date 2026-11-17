#include <doctest.h>

#include "core/shelves/shelves.h"

using nlohmann::json;
namespace shelves = nipa::shelves;

TEST_CASE("a shelf list parses protojson names and int64 strings") {
    auto reply = json::parse(R"({
        "applying": "s2",
        "shelves": [{
            "id": "s1", "name": "Before update", "created_at": "1700000000", "branch": "main", "tag": "",
            "base_commit_id": "c1", "size_bytes": "42",
            "entries": [
                {"path": "a.gd", "status": "modified", "staged": true, "untracked": false, "locked": false},
                {"path": "new.png", "status": "added", "staged": false, "untracked": true, "locked": true}
            ]
        }]
    })");
    auto list = shelves::parse_list(reply);
    CHECK(list.applying == "s2");
    REQUIRE(list.shelves.size() == 1);
    const auto& shelf = list.shelves[0];
    CHECK(shelf.id == "s1");
    CHECK(shelf.name == "Before update");
    CHECK(shelf.created_at == 1700000000);
    CHECK(shelf.size_bytes == 42);
    REQUIRE(shelf.entries.size() == 2);
    CHECK(shelf.entries[0].staged);
    CHECK(shelf.entries[1].untracked);
    CHECK(shelf.entries[1].locked);

    auto out = shelves::to_json(list);
    CHECK(out["applying"] == "s2");
    CHECK(out["shelves"][0]["created_at"] == 1700000000);
    CHECK(out["shelves"][0]["entries"][1]["path"] == "new.png");
    CHECK(out["shelves"][0]["entries"][1]["status"] == "added");
}

TEST_CASE("malformed shelf replies parse to empty values") {
    CHECK(shelves::parse_list(json::array()).shelves.empty());
    CHECK(shelves::parse_list(json{{"shelves", "x"}}).shelves.empty());
    CHECK(shelves::parse_shelf(json::object()).entries.empty());
    CHECK(shelves::parse_shelf(json{{"entries", 1}}).entries.empty());
    CHECK(shelves::parse_overwritten(json::object()).empty());
    CHECK(shelves::parse_overwritten(json{{"overwritten", {"a.gd", 3, "b.tscn"}}}) ==
          std::vector<std::string>{"a.gd", "b.tscn"});
}

TEST_CASE("shelve results summarize creation, conflicts, aborts and drops") {
    CHECK(shelves::result_summary(json{{"shelve", {{"shelf", {{"name", "wip"}, {"entries", {{{"path", "a"}}, {{"path", "b"}}}}}}}}}) ==
          "shelved 2 file(s) as \"wip\"");
    CHECK(shelves::result_summary(json{{"shelve_apply", {{"conflicts", {"a.tscn"}}}}}) == "1 conflict(s) to resolve");
    CHECK(shelves::result_summary(json{{"shelve_apply", {{"aborted", true}}}}) == "shelf apply aborted");
    CHECK(shelves::result_summary(json{{"shelve_apply", {{"dropped", true}}}}) == "shelf applied and deleted");
    CHECK(shelves::result_summary(json{{"shelve_apply", {{"dropped", false}}}}) == "shelf applied");
    CHECK(shelves::result_summary(json{{"sync", {{"commit_id", "c2"}}}}).empty());
    CHECK(shelves::result_summary(json::array()).empty());
}
