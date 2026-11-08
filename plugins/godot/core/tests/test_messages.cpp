#include <doctest.h>

#include "core/daemon/messages.h"

using namespace nipa::daemon;
using nlohmann::json;

TEST_CASE("int64 fields are read from numbers and protojson strings") {
    json j = {{"a", 42}, {"b", "77"}, {"c", "not a number"}, {"d", true}};
    CHECK(json_int(j, "a") == 42);
    CHECK(json_int(j, "b") == 77);
    CHECK(json_int(j, "c", 5) == 5);
    CHECK(json_int(j, "d", 9) == 9);
    CHECK(json_int(j, "missing") == 0);
    CHECK(json_int(json("text"), "a", 3) == 3);
}

TEST_CASE("string and string list fields tolerate absence and wrong types") {
    json j = {{"s", "x"}, {"list", {"a", 1, "b"}}, {"n", 3}};
    CHECK(json_string(j, "s") == "x");
    CHECK(json_string(j, "n").empty());
    CHECK(json_strings(j, "list") == std::vector<std::string>{"a", "b"});
    CHECK(json_strings(j, "n").empty());
    CHECK(json_strings(json("nope"), "list").empty());
}

TEST_CASE("status parsing reads every list and the detached head") {
    json j = {
        {"staged", {"a.tscn"}},
        {"deleted", {"d.txt"}},
        {"modified", {"m.gd"}},
        {"untracked", {"u.txt"}},
        {"missing", {"x.png"}},
        {"conflicts", {"c.tscn"}},
        {"resolved", {"r.tscn"}},
        {"added", {"a.tscn"}},
        {"branch", "main"},
        {"head", {{"kind", "tag"}, {"name", "v1.0"}}},
        {"pending", "merge"},
        {"pending_remaining", "2"},
    };
    auto s = parse_status(j);
    CHECK(s.staged == std::vector<std::string>{"a.tscn"});
    CHECK(s.deleted.size() == 1);
    CHECK(s.modified.size() == 1);
    CHECK(s.untracked.size() == 1);
    CHECK(s.missing.size() == 1);
    CHECK(s.conflicts.size() == 1);
    CHECK(s.resolved.size() == 1);
    CHECK(s.added.size() == 1);
    CHECK(s.branch == "main");
    CHECK(s.head_kind == "tag");
    CHECK(s.head_name == "v1.0");
    CHECK(s.pending == "merge");
    CHECK(s.pending_remaining == 2);
}

TEST_CASE("status parsing accepts a null head and missing lists") {
    auto s = parse_status(json{{"head", nullptr}, {"branch", "main"}});
    CHECK(s.staged.empty());
    CHECK_FALSE(s.detached());
    CHECK(s.branch == "main");
}

TEST_CASE("ping, repos, branches and commits parse protojson shapes") {
    CHECK(pong_version({{"version", "0.2.0"}, {"pid", 1}}) == "0.2.0");

    auto repos = parse_repos({{"repos", {{{"root", "/c"}, {"url", "h/o/p"}, {"branch", "main"}}}}});
    REQUIRE(repos.size() == 1);
    CHECK(repos[0].url == "h/o/p");
    CHECK(parse_repos(json::object()).empty());

    auto branches = parse_branches({{"branches",
                                     {{{"id", "1"}, {"name", "main"}, {"commit_id", "abc"}, {"is_protected", true},
                                       {"is_default", true}},
                                      {{"name", "dev"}, {"commit_id", nullptr}}}}});
    REQUIRE(branches.size() == 2);
    CHECK(branches[0].is_protected);
    CHECK(branches[0].is_default);
    CHECK(branches[1].commit_id.empty());
    CHECK_FALSE(branches[1].is_protected);
    CHECK(parse_branches(json("x")).empty());

    auto commits = parse_commits({{"commits", {{{"commit_id", "c1"}, {"commit_hash", "h1"}, {"parent_1_id", ""},
                                                {"author_name", "Ann"}, {"author_email", "a@x"}, {"message", "m"},
                                                {"created_at", "2026-01-01T00:00:00Z"}}}}});
    REQUIRE(commits.size() == 1);
    CHECK(commits[0].author_name == "Ann");
    CHECK(commits[0].created_at == "2026-01-01T00:00:00Z");
    CHECK(parse_commits(json::object()).empty());
}

TEST_CASE("base64 decoding handles padding and stops at invalid input") {
    CHECK(nipa::daemon::base64_decode("aGVsbG8=") == "hello");
    CHECK(nipa::daemon::base64_decode("aGk=") == "hi");
    CHECK(nipa::daemon::base64_decode("") == "");
    CHECK(nipa::daemon::base64_decode("aGk*bad") == "hi");
}

TEST_CASE("proxy payload unwraps the response and tolerates missing ones") {
    CHECK(proxy_payload({{"response", {{"branches", json::array()}}}}).contains("branches"));
    CHECK(proxy_payload(json::object()).is_object());
    CHECK(proxy_payload({{"response", "x"}}).empty());
}

TEST_CASE("op events parse every kind") {
    auto queued = parse_op_event({{"queued", {{"ahead", "2"}}}});
    CHECK(queued.kind == OpEvent::Kind::Queued);
    CHECK(queued.ahead == 2);

    auto started = parse_op_event({{"started", {{"phase", "download"}}}});
    CHECK(started.kind == OpEvent::Kind::Started);
    CHECK(started.phase == "download");

    auto progress = parse_op_event({{"progress", {{"phase", "upload"}, {"objects_done", "3"}, {"objects_total", 9}}}});
    CHECK(progress.kind == OpEvent::Kind::Progress);
    CHECK(progress.done == 3);
    CHECK(progress.total == 9);

    auto failure = parse_op_event({{"failure", {{"code", 409}, {"message", "conflict"}}}});
    CHECK(failure.kind == OpEvent::Kind::Failure);
    CHECK(failure.failure_code == 409);
    CHECK(failure.message == "conflict");

    auto push = parse_op_event({{"result", {{"push", {{"commit_id", "c9"}}}}}});
    CHECK(push.kind == OpEvent::Kind::Result);
    CHECK(push.result_commit_id == "c9");

    auto sync = parse_op_event({{"result", {{"sync", {{"commit_id", "c2"}}}}}});
    CHECK(sync.result_commit_id == "c2");

    auto merge = parse_op_event({{"result", {{"merge", {{"up_to_date", true}}}}}});
    CHECK(merge.kind == OpEvent::Kind::Result);
    CHECK(merge.result_commit_id.empty());
}

TEST_CASE("diff files parse sides, counts, hunks and omissions") {
    json j = {
        {"path", "level.tscn"},
        {"status", "modified"},
        {"kind", "text"},
        {"old", {{"path", "level.tscn"}, {"size_bytes", "10"}}},
        {"new", {{"path", ""}, {"size_bytes", 12}}},
        {"additions", "1"},
        {"deletions", 0},
        {"hunks_omitted", true},
        {"content_unavailable", false},
        {"hunks", {{{"old_start", "1"}, {"old_lines", 1}, {"new_start", 1}, {"new_lines", 2},
                    {"lines", {{{"kind", "removed"}, {"old_line", "1"}, {"text", "a"}},
                               {{"kind", "added"}, {"new_line", "1"}, {"text", "b"}},
                               {{"kind", "context"}, {"old_line", 2}, {"new_line", 2}, {"text", "c"}}}}}}},
    };
    auto f = parse_diff_file(j);
    CHECK(f.new_path == "level.tscn");
    CHECK(f.old_path == "level.tscn");
    CHECK(f.old_size == 10);
    CHECK(f.new_size == 12);
    CHECK(f.additions == 1);
    CHECK(f.omitted);
    REQUIRE(f.hunks.size() == 1);
    CHECK(f.hunks[0].old_start == 1);
    REQUIRE(f.hunks[0].lines.size() == 3);
    CHECK(f.hunks[0].lines[0].status == '-');
    CHECK(f.hunks[0].lines[0].old_no == 1);
    CHECK(f.hunks[0].lines[0].new_no == -1);
    CHECK(f.hunks[0].lines[1].status == '+');
    CHECK(f.hunks[0].lines[2].status == ' ');

    auto minimal = parse_diff_file(json::object());
    CHECK(minimal.hunks.empty());
    CHECK_FALSE(minimal.omitted);

    auto unavailable = parse_diff_file({{"content_unavailable", true}});
    CHECK(unavailable.omitted);
}
