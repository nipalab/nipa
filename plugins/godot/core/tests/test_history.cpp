#include <doctest.h>

#include "core/daemon/messages.h"
#include "core/history/history.h"
#include "core/preview/preview.h"

using nlohmann::json;
namespace h = nipa::history;
namespace d = nipa::daemon;

TEST_CASE("parse_tags and history pages") {
    auto tags = h::parse_tags({{"tags", {{{"name", "v1"}, {"commit_id", "c1"}, {"message", "first"}}, {{"name", "v2"}}}}});
    REQUIRE(tags.size() == 2);
    CHECK(tags[0].commit_id == "c1");
    CHECK(tags[0].message == "first");
    CHECK(h::parse_tags(json::object()).empty());
    CHECK(h::parse_tags(json::array()).empty());

    auto body = h::page_request("/r", "art/a.png", "c9", 30);
    CHECK(body == json{{"root", "/r"}, {"request", {{"limit", 30}, {"path", "art/a.png"}, {"start_commit_id", "c9"}}}});
    CHECK_FALSE(h::page_request("/r", "", "", 5)["request"].contains("path"));

    json reply = {{"response", {{"commits", {{{"commit_id", "c2"}, {"parent_1_id", "c1"}, {"author_name", "Ann"}}}}}}};
    auto full = h::parse_page(reply, 1);
    CHECK(full.next == "c1");
    CHECK(h::parse_page(reply, 2).next.empty());
    CHECK(h::parse_page(reply, 0).next.empty());
    auto out = h::to_json(full);
    CHECK(out["commits"][0]["author"] == "Ann");
    CHECK(out["next"] == "c1");
}

TEST_CASE("commit_link builds a nipa:// deep link from the clone URL") {
    CHECK(h::commit_link("https://nipa.example.com/acme/game", "c1") == "nipa://nipa.example.com/acme/game/commits/c1");
    CHECK(h::commit_link("localhost:6745/acme/game/", "c1") == "nipa://localhost:6745/acme/game/commits/c1");
    CHECK(h::commit_link("host/org", "c1").empty());
    CHECK(h::commit_link("host/org/proj", "").empty());
    CHECK(h::commit_link("host//org/proj", "c1").empty());
}

TEST_CASE("base64 round trip and result summaries") {
    for (const std::string& s : std::vector<std::string>{"", "f", "fo", "foo", "foob", "fooba", "foobar", std::string("\0\xff\x10", 3)}) {
        CHECK(d::base64_decode(d::base64_encode(s)) == s);
    }
    CHECK(d::base64_encode("foobar") == "Zm9vYmFy");
    CHECK(d::base64_encode("fo") == "Zm8=");
    CHECK(d::result_summary({{"merge", {{"conflicts", {"a"}}}}}) == "1 conflict(s) to resolve");
    CHECK(d::result_summary({{"merge", {{"aborted", true}}}}) == "merge aborted");
    CHECK(d::result_summary({{"merge", {{"up_to_date", true}}}}) == "already up to date");
    CHECK(d::result_summary({{"merge", {{"fast_forwarded", true}}}}) == "fast-forwarded");
    CHECK(d::result_summary({{"merge", json::object()}}) == "merged");
    CHECK(d::result_summary({{"revert", {{"no_change", true}}}}) == "nothing to revert");
    CHECK(d::result_summary({{"revert", {{"skipped", true}}}}) == "skipped");
    CHECK(d::result_summary({{"revert", {{"aborted", true}}}}) == "revert aborted");
    CHECK(d::result_summary({{"revert", {{"committed", true}}}}) == "reverted");
    CHECK(d::result_summary({{"sync", json::object()}}).empty());
    auto e = d::parse_op_event(json::parse(R"({"result":{"merge":{"merge_committed":true}}})"));
    CHECK(e.result["merge"]["merge_committed"] == true);
}

TEST_CASE("preview assembler joins PNG chunks once per side") {
    nipa::preview::Assembler a;
    CHECK(a.add(json::array()));
    CHECK(a.add({{"image", {{"side", "new"}, {"width", 8}, {"height", "4"}, {"hdr", true}}}}));
    CHECK(a.add({{"data", {{"side", "new"}, {"png", d::base64_encode("ab")}}}}));
    CHECK(a.add({{"data", {{"side", "new"}, {"png", d::base64_encode("cd")}}}}));
    CHECK(a.add({{"unknown", 1}}));
    CHECK(a.png("new") == "abcd");
    CHECK(a.png("old").empty());
    CHECK(a.summary()["new"]["height"] == 4);
    CHECK(a.summary()["new"]["hdr"] == true);
    CHECK_FALSE(a.summary().contains("diff"));
    CHECK_FALSE(a.add({{"failure", {{"code", 415}, {"message", "unsupported"}}}}));
    CHECK(a.error() == "unsupported");
    CHECK(a.failure_code() == 415);

    auto body = nipa::preview::request("/r", nullptr, {{"source", "working"}}, 4, 256);
    CHECK_FALSE(body.contains("old"));
    CHECK(body["new"]["source"] == "working");
    CHECK(body["threshold"] == 4);
}
