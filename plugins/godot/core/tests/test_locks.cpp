#include <doctest.h>

#include "core/locks/lock_policy.h"
#include "core/paths/project_paths.h"

using nipa::locks::covers;
using nipa::locks::default_extensions;
using nipa::locks::LockDetail;
using nipa::locks::LockPolicy;
using nipa::locks::LockState;
using nipa::locks::LockStatus;
using nipa::locks::parse_lock_status;
using nipa::locks::state_name;
using nlohmann::json;

namespace {

LockDetail lock(const std::string& path, const std::string& held_by, bool global = true, const std::string& branch = "") {
    LockDetail l;
    l.id = "id-" + path;
    l.path = path;
    l.global = global;
    l.branch = branch;
    l.held_by = held_by;
    l.held_by_name = held_by == "me" ? "Me" : "Ann";
    return l;
}

LockStatus status(std::vector<LockDetail> locks, bool default_branch = true) {
    LockStatus s;
    s.user_id = "me";
    s.branch = default_branch ? "main" : "feature";
    s.default_branch = default_branch;
    s.locks = std::move(locks);
    return s;
}

}  // namespace

TEST_CASE("sidecars belong to their asset") {
    using nipa::paths::asset_of;
    using nipa::paths::lock_unit;
    CHECK(asset_of("art/a.png.import") == "art/a.png");
    CHECK(asset_of("scripts/p.gd.uid") == "scripts/p.gd");
    CHECK(asset_of("art/a.png") == "art/a.png");
    CHECK(asset_of("dir/.import") == "dir/.import");
    CHECK(asset_of("dir/plain.import") == "dir/plain.import");
    CHECK(asset_of("art\\a.png.import") == "art/a.png");
    CHECK(lock_unit("art/a.png.uid") == std::vector<std::string>{"art/a.png", "art/a.png.import", "art/a.png.uid"});
}

TEST_CASE("a lock covers its path and everything below a directory") {
    CHECK(covers("art/a.png", "art/a.png"));
    CHECK(covers("art", "art/a.png"));
    CHECK(covers("/art/", "art/sub/a.png"));
    CHECK_FALSE(covers("art", "artwork/a.png"));
    CHECK_FALSE(covers("", "a.png"));
    CHECK_FALSE(covers("art/a.png", "art"));
}

TEST_CASE("lock status parses the protojson reply") {
    json reply = {
        {"user_id", "me"},
        {"branch", "main"},
        {"default_branch", true},
        {"can_release_any", true},
        {"locks", {{{"id", "1"}, {"path", "/art/a.png"}, {"global", true}, {"held_by", "u2"}, {"held_by_name", "Ann"}}}},
        {"files", {{{"path", "art/a.png"}, {"state", "theirs"}, {"lock_path", "art/a.png"}, {"held_by", "u2"}, {"tracked", true}}}},
    };
    auto s = parse_lock_status(reply);
    CHECK(s.user_id == "me");
    CHECK(s.default_branch);
    CHECK(s.can_release_any);
    REQUIRE(s.locks.size() == 1);
    CHECK(s.locks[0].path == "art/a.png");
    CHECK(s.locks[0].holder() == "Ann");
    REQUIRE(s.files.size() == 1);
    CHECK(s.files[0].tracked);
    CHECK(s.files[0].holder() == "u2");
    CHECK(parse_lock_status(json::array()).locks.empty());
    CHECK(parse_lock_status(json{{"locks", "x"}, {"files", 3}}).files.empty());
}

TEST_CASE("lookup reports mine, theirs or free for the whole lock unit") {
    LockPolicy policy(status({lock("art/a.png", "u2"), lock("art/b.png.import", "me"), lock("levels", "u2")}),
                      default_extensions());
    CHECK(policy.loaded());

    auto theirs = policy.lookup("art/a.png.import");
    CHECK(theirs.state == LockState::Theirs);
    CHECK(theirs.holder == "Ann");
    CHECK(theirs.lock_path == "art/a.png");

    auto mine = policy.lookup("art/b.png");
    CHECK(mine.state == LockState::Mine);
    CHECK(mine.lock_path == "art/b.png.import");

    CHECK(policy.lookup("levels/one.tscn").state == LockState::Theirs);
    CHECK(policy.lookup("other.png").state == LockState::Free);
    CHECK(policy.held_by_me("art/b.png.import"));
    CHECK_FALSE(policy.held_by_me("art/a.png"));
    CHECK(std::string(state_name(LockState::Mine)) == "mine");
    CHECK(std::string(state_name(LockState::Theirs)) == "theirs");
    CHECK(std::string(state_name(LockState::Free)) == "free");
}

TEST_CASE("theirs wins over mine inside one unit") {
    LockPolicy policy(status({lock("a.png", "me"), lock("a.png.import", "u2")}), {});
    CHECK(policy.lookup("a.png").state == LockState::Theirs);
}

TEST_CASE("scope: global locks apply on the default branch, branch locks on their branch") {
    auto locks = std::vector<LockDetail>{lock("g.png", "u2"), lock("f.png", "u2", false, "feature")};
    LockPolicy on_main(status(locks, true), {});
    CHECK(on_main.lookup("g.png").state == LockState::Theirs);
    CHECK(on_main.lookup("f.png").state == LockState::Free);
    CHECK(on_main.lock_branch().empty());

    LockPolicy on_feature(status(locks, false), {});
    CHECK(on_feature.lookup("g.png").state == LockState::Free);
    CHECK(on_feature.lookup("f.png").state == LockState::Theirs);
    CHECK(on_feature.lock_branch() == "feature");
    CHECK(on_feature.theirs_paths() == std::set<std::string>{"f.png"});
}

TEST_CASE("no user id means nothing is mine") {
    auto s = status({lock("a.png", "me")});
    s.user_id.clear();
    LockPolicy policy(s, {});
    CHECK(policy.lookup("a.png").state == LockState::Theirs);
}

TEST_CASE("lock relevance follows the extension list and changed binaries") {
    auto s = status({});
    nipa::locks::FileState bin;
    bin.path = "data/blob.bin";
    bin.state = "needed";
    s.files.push_back(bin);
    LockPolicy policy(s, {".TSCN", " png ", "", "."});
    CHECK(policy.lock_relevant("levels/a.tscn"));
    CHECK(policy.lock_relevant("art/a.PNG.import"));
    CHECK(policy.lock_relevant("data/blob.bin"));
    CHECK_FALSE(policy.lock_relevant("scripts/a.gd"));
    CHECK_FALSE(policy.lock_relevant(".hidden"));
    CHECK_FALSE(policy.lock_relevant("dir.png/file"));

    LockPolicy defaults;
    CHECK_FALSE(defaults.loaded());
    CHECK(defaults.lock_relevant("a.glb"));
    CHECK(defaults.lookup("a.glb").state == LockState::Free);
}

TEST_CASE("push check splits needed and theirs") {
    auto s = status({});
    for (auto [path, state] : std::vector<std::pair<std::string, std::string>>{
             {"a.png", "needed"}, {"a.png", "needed"}, {"b.png", "theirs"}, {"c.png", "mine"}, {"d.png", "free"}}) {
        nipa::locks::FileState f;
        f.path = path;
        f.state = state;
        f.held_by_name = "Ann";
        s.files.push_back(f);
    }
    auto check = LockPolicy(s, {}).check_push();
    CHECK(check.blocked());
    CHECK(check.needed == std::vector<std::string>{"a.png"});
    REQUIRE(check.theirs.size() == 1);
    CHECK(check.theirs_summary() == "b.png (Ann)");

    nipa::locks::PushCheck two;
    two.theirs = {s.files[2], s.files[2]};
    CHECK(two.theirs_summary() == "b.png (Ann), b.png (Ann)");
    CHECK_FALSE(nipa::locks::PushCheck{}.blocked());
}

TEST_CASE("releasable picks my locks on the unit, or anyone's for an admin") {
    auto s = status({lock("a.png", "me"), lock("a.png.import", "me"), lock("b.png", "u2"), lock("c.png", "me")});
    LockPolicy policy(s, {});
    auto mine = policy.releasable({"a.png.uid", "b.png"}, false);
    REQUIRE(mine.size() == 2);
    CHECK(mine[0].path == "a.png");
    CHECK(mine[1].path == "a.png.import");
    CHECK(policy.releasable({"b.png"}, true).empty());
    CHECK_FALSE(policy.can_release(s.locks[2]));

    s.can_release_any = true;
    LockPolicy admin(s, {});
    CHECK(admin.releasable({"b.png"}, true).size() == 1);
    CHECK(admin.can_release(s.locks[2]));
}
