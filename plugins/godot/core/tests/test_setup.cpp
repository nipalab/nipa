#include <doctest.h>

#include <atomic>
#include <chrono>
#include <filesystem>
#include <fstream>
#include <functional>
#include <memory>
#include <thread>

#include "core/session/session.h"
#include "core/setup/checker.h"
#include "core/setup/ignore.h"
#include "fake_daemon.h"

using namespace std::chrono_literals;
using namespace nipa::setup;
using nipa::test::FakeDaemon;
using nipa::test::Reply;
using nipa::test::StreamScript;
using nlohmann::json;

namespace {

std::string read_file(const std::filesystem::path& path) {
    std::ifstream in(path, std::ios::binary);
    return std::string(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
}

void write_file(const std::filesystem::path& path, const std::string& content) {
    std::ofstream out(path, std::ios::binary | std::ios::trunc);
    out << content;
}

const Row& row(const std::vector<Row>& rows, const std::string& id) {
    for (const auto& r : rows) {
        if (r.id == id) {
            return r;
        }
    }
    FAIL("missing row " << id);
    return rows.front();
}

Inputs healthy() {
    Inputs in;
    in.vcs_plugin_name = kVcsPluginName;
    in.vcs_autoload = true;
    in.ignore_content = ".godot/\n/android/\n*.tmp\nexport_credentials.cfg\n.mono/\nmono_crash.*.json\ndata_*/\n";
    in.godot_dir.server = Tracking::NotTracked;
    in.sidecars_known = true;
    in.connected = true;
    in.daemon_version = "0.2.0";
    in.min_version = "0.1.0";
    in.plugin_version = "0.1.0";
    in.binary = "/usr/bin/nipa";
    return in;
}

}  // namespace

TEST_CASE("the ignore template anchors only rooted entries under the project folder") {
    auto root = ignore_template("");
    CHECK(root == std::vector<std::string>{".godot/", "/android/", "*.tmp", "export_credentials.cfg", ".mono/",
                                           "mono_crash.*.json", "data_*/"});
    auto nested = ignore_template("/game/");
    CHECK(nested[0] == ".godot/");
    CHECK(nested[1] == "/game/android/");
    CHECK(godot_dir("") == ".godot");
    CHECK(godot_dir("game/") == "game/.godot");
}

TEST_CASE("merging the template never duplicates lines and keeps line endings") {
    auto wanted = ignore_template("");
    CHECK(missing_ignore_lines("", wanted).size() == wanted.size());

    std::string crlf = "# team rules\r\n.godot\r\n  *.tmp  \r\nbuild/";
    auto missing = missing_ignore_lines(crlf, wanted);
    CHECK(missing == std::vector<std::string>{"/android/", "export_credentials.cfg", ".mono/", "mono_crash.*.json",
                                              "data_*/"});
    auto merged = merge_ignore(crlf, wanted);
    CHECK(merged.rfind(crlf + "\r\n/android/\r\n", 0) == 0);
    CHECK(merged.substr(merged.size() - 9) == "data_*/\r\n");
    CHECK(merge_ignore(merged, wanted) == merged);
    CHECK(missing_ignore_lines(merged, wanted).empty());

    CHECK(merge_ignore("a\n", {"a", "b", "b"}) == "a\nb\n");
    CHECK(merge_ignore("", {"x/"}) == "x/\n");
    CHECK(missing_ignore_lines("# .godot/\n", {".godot/"}) == std::vector<std::string>{".godot/"});
}

TEST_CASE("ignore rules follow the client's subset") {
    auto rules = IgnoreRules::parse("# c\n.godot/\n/android/\n*.tmp\n!keep.tmp\ndocs/**/draft?.md\n**/cache\nbin/**\n/\n!\n");
    CHECK(rules.ignores(".godot", true));
    CHECK(rules.ignores("game/.godot", true));
    CHECK(rules.ignores("game/.godot/editor/x.cfg", false));
    CHECK_FALSE(rules.ignores(".godot", false));
    CHECK(rules.ignores("android/build.gradle", false));
    CHECK_FALSE(rules.ignores("game/android/build.gradle", false));
    CHECK(rules.ignores("a/b.tmp", false));
    CHECK_FALSE(rules.ignores("keep.tmp", false));
    CHECK(rules.ignores("docs/x/y/draft1.md", false));
    CHECK(rules.ignores("docs/draft2.md", false));
    CHECK_FALSE(rules.ignores("docs/draft10.md", false));
    CHECK(rules.ignores("deep/er/cache", true));
    CHECK(rules.ignores("bin/linux/lib.so", false));
    CHECK_FALSE(rules.ignores("", true));
    CHECK_FALSE(IgnoreRules::parse("").ignores("a", false));
    CHECK(IgnoreRules::parse("**\n").ignores("anything/at/all", false));
    CHECK(IgnoreRules::parse("a+b(c).txt\n").ignores("a+b(c).txt", false));
}

TEST_CASE("the .godot check respects negations and the project folder") {
    CHECK(ignores_godot_dir(".godot/\n", ""));
    CHECK(ignores_godot_dir(".godot/\n", "game"));
    CHECK(ignores_godot_dir("/game/.godot/\n", "game"));
    CHECK_FALSE(ignores_godot_dir("/.godot/\n", "game"));
    CHECK_FALSE(ignores_godot_dir(".godot/\n!.godot/\n", ""));
    CHECK_FALSE(ignores_godot_dir("", ""));
}

TEST_CASE("write_ignore creates, completes and then leaves .nipaignore alone") {
    auto dir = std::filesystem::path(nipa::test::make_temp_dir());
    CHECK_FALSE(read_ignore(dir.string()));
    auto first = write_ignore(dir.string(), "game");
    CHECK(first.ok);
    CHECK(first.changed);
    auto created = read_ignore(dir.string());
    REQUIRE(created);
    CHECK(created->find("/game/android/\n") != std::string::npos);
    auto again = write_ignore(dir.string(), "game");
    CHECK(again.ok);
    CHECK_FALSE(again.changed);

    write_file(dir / kIgnoreFile, "build/\n.godot/");
    CHECK(write_ignore(dir.string(), "").changed);
    auto merged = read_file(dir / kIgnoreFile);
    CHECK(merged.rfind("build/\n.godot/\n/android/\n", 0) == 0);
    CHECK_FALSE(write_ignore(dir.string(), "").changed);
    CHECK(read_file(dir / kIgnoreFile) == merged);
    CHECK_FALSE(std::filesystem::exists(dir / ".nipaignore.tmp"));

    auto failed = write_ignore((dir / "missing" / "dir").string(), "");
    CHECK_FALSE(failed.ok);
    CHECK(failed.error.find("cannot write") == 0);
    std::filesystem::remove_all(dir);
}

TEST_CASE("meta warnings parse and only unstaged sidecars are stageable") {
    auto warnings = parse_meta_warnings(json::parse(R"({"warnings":[
        {"path":"a.png.import","kind":"sidecar-not-staged","related":"a.png","message":"is not staged"},
        {"path":"b.png","kind":"missing-sidecar","related":"b.png.import","message":"has no .import"},
        {"path":"a.png.import","kind":"sidecar-not-staged","related":"a.png","message":"dup"},
        "junk"]})"));
    REQUIRE(warnings.size() == 3);
    CHECK(warnings[0].stageable());
    CHECK_FALSE(warnings[1].stageable());
    CHECK(stage_paths(warnings) == std::vector<std::string>{"a.png.import"});
    auto j = to_json(warnings);
    CHECK(j[0]["stageable"] == true);
    CHECK(j[1]["related"] == "b.png.import");
    CHECK(parse_meta_warnings(json::object()).empty());
    CHECK(parse_meta_warnings(json{{"warnings", 3}}).empty());
}

TEST_CASE(".godot status entries split into staged, changed and untracked") {
    nipa::status::Snapshot s;
    s.staged = {"game/.godot/a.cfg", "game/x.gd"};
    s.added = {"game/.godot/b.cfg"};
    s.modified = {"game/.godot/a.cfg", "game/.godot/c.cfg", "game/.godotx/c.cfg"};
    s.missing = {"game/.godot/d.cfg"};
    s.untracked = {"game/.godot/e.cfg", ".godot/f.cfg"};
    auto state = godot_dir_from_status(s, "game");
    CHECK(state.staged == std::vector<std::string>{"game/.godot/a.cfg", "game/.godot/b.cfg"});
    CHECK(state.changed == std::vector<std::string>{"game/.godot/c.cfg", "game/.godot/d.cfg"});
    CHECK(state.untracked == std::vector<std::string>{"game/.godot/e.cfg"});
    CHECK(state.server == Tracking::Unknown);

    CHECK(tracking_from_manifest(200, "") == Tracking::Tracked);
    CHECK(tracking_from_manifest(404, "path \".godot\" not found in branch \"main\"") == Tracking::NotTracked);
    CHECK(tracking_from_manifest(404, "branch \"x\" not found") == Tracking::Unknown);
    CHECK(tracking_from_manifest(500, "boom") == Tracking::Unknown);
}

TEST_CASE("a healthy project has only ok rows") {
    auto rows = evaluate(healthy());
    REQUIRE(rows.size() == 5);
    for (const auto& r : rows) {
        CHECK_MESSAGE(r.level == Level::Ok, r.id);
        CHECK(r.fix.empty());
    }
    CHECK(row(rows, "version").title == "nipa daemon 0.2.0");
    CHECK(row(rows, "version").detail.find("CLI: /usr/bin/nipa") != std::string::npos);
    auto j = to_json(rows);
    CHECK(j.size() == 5);
    CHECK(j[0]["level"] == "ok");
    CHECK(j[0]["paths"].is_array());
}

TEST_CASE("the VCS row offers to select NipaVCSInterface") {
    auto in = healthy();
    in.vcs_plugin_name = "";
    in.vcs_autoload = false;
    auto r = row(evaluate(in), "vcs");
    CHECK(r.level == Level::Error);
    CHECK(r.fix == "set_vcs");
    CHECK(r.detail.find("No version control plugin") != std::string::npos);
    CHECK(r.detail.find("Autoload on startup is off") != std::string::npos);
    in.vcs_plugin_name = "GitPlugin";
    in.vcs_autoload = true;
    r = row(evaluate(in), "vcs");
    CHECK(r.detail.find("\"GitPlugin\"") != std::string::npos);
}

TEST_CASE("the ignore row covers missing files, missing .godot and missing patterns") {
    auto in = healthy();
    in.ignore_content.reset();
    auto r = row(evaluate(in), "ignore");
    CHECK(r.level == Level::Warning);
    CHECK(r.fix_label == "Create .nipaignore");

    in.ignore_content = "*.tmp\n";
    r = row(evaluate(in), "ignore");
    CHECK(r.detail == "Does not ignore .godot/.");
    CHECK(r.fix == "write_ignore");

    in.ignore_content = ".godot\n";
    r = row(evaluate(in), "ignore");
    CHECK(r.detail.rfind("6 recommended pattern(s) missing: /android/", 0) == 0);
}

TEST_CASE("the .godot row reports staged, tracked, untracked and unknown states") {
    auto in = healthy();
    in.godot_dir.staged = {".godot/a.cfg"};
    auto r = row(evaluate(in), "godot-dir");
    CHECK(r.level == Level::Error);
    CHECK(r.fix == "unstage_godot");
    CHECK(r.paths == std::vector<std::string>{".godot/a.cfg"});

    in.godot_dir.staged.clear();
    in.godot_dir.server = Tracking::Tracked;
    r = row(evaluate(in), "godot-dir");
    CHECK(r.level == Level::Error);
    CHECK(r.fix.empty());
    CHECK(r.detail.find("nipa add .godot") != std::string::npos);

    in.godot_dir.server = Tracking::NotTracked;
    in.godot_dir.changed = {".godot/x"};
    CHECK(row(evaluate(in), "godot-dir").level == Level::Error);

    in.godot_dir.changed.clear();
    in.godot_dir.untracked = {".godot/x"};
    r = row(evaluate(in), "godot-dir");
    CHECK(r.level == Level::Warning);
    CHECK(r.fix == "write_ignore");

    in.godot_dir.untracked.clear();
    in.godot_dir.server = Tracking::Unknown;
    in.godot_dir.error = "offline";
    r = row(evaluate(in), "godot-dir");
    CHECK(r.level == Level::Unknown);
    CHECK(r.detail == "Could not check the server: offline");
    in.godot_dir.error.clear();
    CHECK(row(evaluate(in), "godot-dir").detail == "Could not check the server.");
}

TEST_CASE("the sidecar row lists warnings and stages only unstaged sidecars") {
    auto in = healthy();
    in.sidecars_known = false;
    CHECK(row(evaluate(in), "sidecars").level == Level::Unknown);

    in.sidecars_known = true;
    for (int i = 0; i < 12; ++i) {
        in.sidecars.push_back({"f" + std::to_string(i) + ".png", "missing-sidecar", "", "has no .import"});
    }
    auto r = row(evaluate(in), "sidecars");
    CHECK(r.level == Level::Warning);
    CHECK(r.fix.empty());
    CHECK(r.detail.find("and 2 more") != std::string::npos);

    in.sidecars.push_back({"a.gd.uid", "sidecar-not-staged", "a.gd", "is not staged"});
    r = row(evaluate(in), "sidecars");
    CHECK(r.fix == "stage_sidecars");
    CHECK(r.paths == std::vector<std::string>{"a.gd.uid"});
}

TEST_CASE("the version row checks the connection and the minimum") {
    auto in = healthy();
    in.connected = false;
    in.connect_error = "nipa CLI not found";
    in.binary.clear();
    auto r = row(evaluate(in), "version");
    CHECK(r.level == Level::Error);
    CHECK(r.detail.find("nipa CLI not found\nCLI: not found") == 0);

    in.connected = true;
    in.daemon_version = "0.0.9";
    r = row(evaluate(in), "version");
    CHECK(r.level == Level::Error);
    CHECK(r.title == "nipa daemon is too old");

    in.daemon_version = "dev-abc";
    in.min_version.clear();
    r = row(evaluate(in), "version");
    CHECK(r.level == Level::Ok);
    CHECK(r.detail.find("minimum -") != std::string::npos);
    CHECK(std::string(level_name(Level::Unknown)) == "unknown");
    CHECK(std::string(level_name(Level::Warning)) == "warning");
    CHECK(std::string(level_name(Level::Error)) == "error");
}

namespace {

struct SetupHarness {
    std::string dir = nipa::test::make_temp_dir();
    std::string clone = dir + "/clone";
    FakeDaemon fake;
    std::unique_ptr<nipa::session::Session> session;
    std::vector<nipa::session::Event> seen;

    SetupHarness() {
        std::filesystem::create_directories(clone);
        fake.on_unary("Ping", [](const json&) { return Reply{200, {{"version", "0.2.0"}}}; });
        fake.on_unary("WatchRepo", [](const json&) { return Reply{200, json::object()}; });
        fake.on_unary("Status", [](const json&) {
            return Reply{200, {{"branch", "main"}, {"staged", {"a.png"}}, {"untracked", {".godot/x.cfg"}}}};
        });
        fake.on_stream("WatchChanges", silent);
        fake.on_stream("WatchEvents", silent);
        fake.start();
        nipa::test::write_endpoint(dir + "/daemon.json", fake.port(), fake.token(), "0.2.0");
    }

    ~SetupHarness() {
        if (session) {
            session->stop();
        }
        fake.stop();
        std::filesystem::remove_all(dir);
    }

    static StreamScript silent(const json&) {
        StreamScript script;
        script.endless = true;
        script.delay_ms = 20;
        return script;
    }

    void start(const std::string& endpoint) {
        nipa::session::SessionOptions o;
        o.connect.endpoint_path = endpoint;
        o.connect.min_version = "0.1.0";
        o.connect.binary_setting = "/opt/nipa";
        o.connect.env = [](const std::string&) { return std::string(); };
        o.connect.os = nipa::daemon::HostOs::Linux;
        o.connect.file_exists = [](const std::string&) { return false; };
        o.connect.spawn = [](const std::string&, const std::string&) { return false; };
        o.connect.start_timeout = 200ms;
        o.connect.poll_interval = 10ms;
        o.connect.ping_timeout = 1s;
        o.root = clone;
        o.unary_timeout = 2s;
        o.reconnect_min = 20ms;
        o.reconnect_max = 50ms;
        o.watch_activity = false;
        session = std::make_unique<nipa::session::Session>(o);
        session->start();
    }

    bool wait(const std::function<bool()>& done) {
        auto deadline = std::chrono::steady_clock::now() + 3s;
        while (std::chrono::steady_clock::now() < deadline) {
            for (auto& e : session->events().drain()) {
                seen.push_back(std::move(e));
            }
            if (done()) {
                return true;
            }
            std::this_thread::sleep_for(5ms);
        }
        return false;
    }

    const nipa::session::Event* finished(std::int64_t id) const {
        for (const auto& e : seen) {
            if (e.kind == nipa::session::EventKind::OpFinished && e.op_id == id) {
                return &e;
            }
        }
        return nullptr;
    }

    bool saw(nipa::session::EventKind kind) const {
        for (const auto& e : seen) {
            if (e.kind == kind) {
                return true;
            }
        }
        return false;
    }

    static json row_of(const json& data, const std::string& id) {
        for (const auto& r : data["rows"]) {
            if (r["id"] == id) {
                return r;
            }
        }
        return json::object();
    }
};

}  // namespace

TEST_CASE("status changes refresh the sidecar warnings") {
    SetupHarness h;
    h.fake.on_unary("MetaWarnings", [](const json&) {
        return Reply{200, json::parse(R"({"warnings":[{"path":"a.png.import","kind":"sidecar-not-staged","related":"a.png","message":"m"}]})")};
    });
    h.start(h.dir + "/daemon.json");
    CHECK(h.wait([&] { return h.saw(nipa::session::EventKind::WarningsChanged); }));
    REQUIRE(h.session->sidecar_warnings().size() == 1);
    CHECK(h.session->sidecar_warnings()[0].path == "a.png.import");
    for (const auto& request : h.fake.requests()) {
        if (request.method == "MetaWarnings") {
            CHECK(request.body["engine"] == "godot");
            CHECK(request.body["root"] == h.clone);
        }
    }
}

TEST_CASE("the setup check gathers every input from the daemon and the clone") {
    SetupHarness h;
    write_file(std::filesystem::path(h.clone) / kIgnoreFile, "*.tmp\n");
    h.fake.on_unary("MetaWarnings", [](const json&) { return Reply{200, {{"warnings", json::array()}}}; });
    h.fake.on_unary("ProxyTreeManifest", [](const json&) {
        return Reply{404, {{"code", "NotFound"}, {"message", "path \".godot\" not found in branch \"main\""}}};
    });
    h.start(h.dir + "/daemon.json");
    Inputs base;
    base.vcs_plugin_name = kVcsPluginName;
    base.vcs_autoload = true;
    auto id = h.session->submit_setup_check(base);
    REQUIRE(id > 0);
    REQUIRE(h.wait([&] { return h.finished(id) != nullptr; }));
    const auto& data = h.finished(id)->data;
    CHECK(h.finished(id)->ok);
    CHECK(SetupHarness::row_of(data, "vcs")["level"] == "ok");
    CHECK(SetupHarness::row_of(data, "ignore")["detail"] == "Does not ignore .godot/.");
    CHECK(SetupHarness::row_of(data, "godot-dir")["level"] == "warning");
    CHECK(SetupHarness::row_of(data, "sidecars")["level"] == "ok");
    CHECK(SetupHarness::row_of(data, "version")["title"] == "nipa daemon 0.2.0");
    CHECK(SetupHarness::row_of(data, "version")["detail"].get<std::string>().find("CLI: /opt/nipa") !=
          std::string::npos);
    CHECK(data["warnings"].empty());
    for (const auto& request : h.fake.requests()) {
        if (request.method == "ProxyTreeManifest") {
            CHECK(request.body["request"]["path"] == ".godot");
            CHECK(request.body["request"]["branch"] == "main");
            CHECK(request.body["request"]["recursive"] == false);
        }
    }
}

TEST_CASE("the setup check reports a versioned .godot and an unreachable server") {
    SetupHarness h;
    std::atomic<int> calls{0};
    h.fake.on_unary("ProxyTreeManifest", [&calls](const json&) {
        if (calls++ == 0) {
            return Reply{200, {{"response", {{"root_tree", json::object()}}}}};
        }
        return Reply{500, {{"code", "Internal"}, {"message", "server down"}}};
    });
    h.start(h.dir + "/daemon.json");
    auto first = h.session->submit_setup_check(Inputs{});
    REQUIRE(h.wait([&] { return h.finished(first) != nullptr; }));
    auto data = h.finished(first)->data;
    CHECK(SetupHarness::row_of(data, "godot-dir")["level"] == "error");
    CHECK(SetupHarness::row_of(data, "sidecars")["level"] == "unknown");
    CHECK(SetupHarness::row_of(data, "vcs")["fix"] == "set_vcs");
    CHECK(SetupHarness::row_of(data, "ignore")["fix_label"] == "Create .nipaignore");

    h.fake.on_unary("Status", [](const json&) { return Reply{200, {{"branch", "main"}}}; });
    auto second = h.session->submit_setup_check(Inputs{});
    REQUIRE(h.wait([&] { return h.finished(second) != nullptr; }));
    auto godot = SetupHarness::row_of(h.finished(second)->data, "godot-dir");
    CHECK(godot["level"] == "unknown");
    CHECK(godot["detail"].get<std::string>().find("server down") != std::string::npos);
}

TEST_CASE("the setup check still answers without a daemon") {
    SetupHarness h;
    h.start(h.dir + "/missing.json");
    auto id = h.session->submit_setup_check(Inputs{});
    REQUIRE(h.wait([&] { return h.finished(id) != nullptr; }));
    auto version = SetupHarness::row_of(h.finished(id)->data, "version");
    CHECK(version["level"] == "error");
    CHECK(version["title"] == "nipa daemon not connected");
    CHECK(SetupHarness::row_of(h.finished(id)->data, "godot-dir")["level"] == "unknown");
}
