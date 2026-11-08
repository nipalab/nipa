#include <doctest.h>

#include <atomic>
#include <filesystem>
#include <thread>

#include "core/daemon/connector.h"
#include "core/daemon/spawn.h"
#include "fake_daemon.h"

using namespace std::chrono_literals;
using nipa::daemon::CancelledError;
using nipa::daemon::CancelToken;
using nipa::daemon::ConnectError;
using nipa::daemon::ConnectFailure;
using nipa::daemon::ConnectOptions;
using nipa::daemon::HostOs;
using nipa::test::FakeDaemon;
using nipa::test::Reply;
using nlohmann::json;

namespace {

ConnectOptions options_for(const std::string& endpoint_path) {
    ConnectOptions o;
    o.endpoint_path = endpoint_path;
    o.env = [](const std::string&) { return std::string(); };
    o.os = HostOs::Linux;
    o.file_exists = [](const std::string&) { return false; };
    o.spawn = [](const std::string&, const std::string&) { return false; };
    o.start_timeout = 300ms;
    o.poll_interval = 10ms;
    o.ping_timeout = 500ms;
    return o;
}

void serve_ping(FakeDaemon& fake, const std::string& version) {
    fake.on_unary("Ping", [version](const json&) { return Reply{200, {{"version", version}}}; });
}

}  // namespace

TEST_CASE("connect attaches to a running daemon without spawning") {
    auto dir = nipa::test::make_temp_dir();
    FakeDaemon fake;
    serve_ping(fake, "0.2.0");
    int port = fake.start();
    nipa::test::write_endpoint(dir + "/daemon.json", port, fake.token());

    auto options = options_for(dir + "/daemon.json");
    options.min_version = "0.2.0";
    bool spawned = false;
    options.spawn = [&spawned](const std::string&, const std::string&) {
        spawned = true;
        return true;
    };
    auto attached = nipa::daemon::connect(options);
    CHECK_FALSE(attached.started);
    CHECK(attached.version == "0.2.0");
    CHECK_FALSE(spawned);
    CHECK(attached.client != nullptr);
    fake.stop();
    std::filesystem::remove_all(dir);
}

TEST_CASE("connect spawns the daemon when none answers and waits for daemon.json") {
    auto dir = nipa::test::make_temp_dir();
    FakeDaemon fake;
    serve_ping(fake, "0.3.0");
    int port = fake.start();

    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/opt/nipa/nipa";
    std::string spawned_binary;
    std::string spawned_endpoint;
    options.spawn = [&](const std::string& binary, const std::string& endpoint) {
        spawned_binary = binary;
        spawned_endpoint = endpoint;
        nipa::test::write_endpoint(dir + "/daemon.json", port, fake.token(), "0.3.0");
        return true;
    };
    auto attached = nipa::daemon::connect(options);
    CHECK(attached.started);
    CHECK(attached.version == "0.3.0");
    CHECK(spawned_binary == "/opt/nipa/nipa");
    CHECK(spawned_endpoint == dir + "/daemon.json");
    fake.stop();
    std::filesystem::remove_all(dir);
}

TEST_CASE("connect shuts down an older daemon before spawning a new one") {
    auto dir = nipa::test::make_temp_dir();
    FakeDaemon old_daemon;
    std::atomic<bool> retired{false};
    old_daemon.on_unary("Ping", [&retired](const json&) {
        if (retired) {
            return Reply{503, {{"code", "Unavailable"}, {"message", "gone"}}};
        }
        return Reply{200, {{"version", "0.1.0"}}};
    });
    old_daemon.on_unary("Shutdown", [&retired](const json&) {
        retired = true;
        return Reply{200, json::object()};
    });
    int old_port = old_daemon.start();
    nipa::test::write_endpoint(dir + "/daemon.json", old_port, old_daemon.token(), "0.1.0");

    FakeDaemon fresh;
    serve_ping(fresh, "0.3.0");
    int fresh_port = fresh.start();

    auto options = options_for(dir + "/daemon.json");
    options.min_version = "0.2.0";
    options.binary_setting = "/opt/nipa/nipa";
    options.spawn = [&](const std::string&, const std::string&) {
        nipa::test::write_endpoint(dir + "/daemon.json", fresh_port, fresh.token(), "0.3.0");
        return true;
    };
    auto attached = nipa::daemon::connect(options);
    CHECK(attached.started);
    CHECK(attached.version == "0.3.0");
    CHECK(old_daemon.count("Shutdown") == 1);
    old_daemon.stop();
    fresh.stop();
    std::filesystem::remove_all(dir);
}

TEST_CASE("an old daemon that ignores shutdown is abandoned after the start timeout") {
    auto dir = nipa::test::make_temp_dir();
    FakeDaemon old_daemon;
    serve_ping(old_daemon, "0.1.0");
    old_daemon.on_unary("Shutdown", [](const json&) { return Reply{200, json::object()}; });
    int old_port = old_daemon.start();
    nipa::test::write_endpoint(dir + "/daemon.json", old_port, old_daemon.token(), "0.1.0");

    auto options = options_for(dir + "/daemon.json");
    options.min_version = "0.2.0";
    options.binary_setting = "/opt/nipa/nipa";
    options.spawn = [](const std::string&, const std::string&) { return true; };
    CHECK_THROWS_AS(nipa::daemon::connect(options), ConnectError);
    old_daemon.stop();
    std::filesystem::remove_all(dir);
}

TEST_CASE("a daemon without json_port is reported, not replaced") {
    auto dir = nipa::test::make_temp_dir();
    nipa::test::write_endpoint(dir + "/daemon.json", 0, "tok", "0.1.0");
    auto options = options_for(dir + "/daemon.json");
    bool spawned = false;
    options.spawn = [&spawned](const std::string&, const std::string&) {
        spawned = true;
        return true;
    };
    try {
        nipa::daemon::connect(options);
        FAIL("expected ConnectError");
    } catch (const ConnectError& e) {
        CHECK(e.reason == ConnectFailure::OldDaemon);
        CHECK(std::string(e.what()).find("predates the JSON bridge") != std::string::npos);
    }
    CHECK_FALSE(spawned);
    std::filesystem::remove_all(dir);
}

TEST_CASE("missing binary is reported before spawning") {
    auto dir = nipa::test::make_temp_dir();
    auto options = options_for(dir + "/daemon.json");
    try {
        nipa::daemon::connect(options);
        FAIL("expected ConnectError");
    } catch (const ConnectError& e) {
        CHECK(e.reason == ConnectFailure::BinaryNotFound);
    }
    std::filesystem::remove_all(dir);
}

TEST_CASE("a failed spawn is reported") {
    auto dir = nipa::test::make_temp_dir();
    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/opt/nipa/nipa";
    try {
        nipa::daemon::connect(options);
        FAIL("expected ConnectError");
    } catch (const ConnectError& e) {
        CHECK(e.reason == ConnectFailure::StartFailed);
    }
    std::filesystem::remove_all(dir);
}

TEST_CASE("a daemon that never publishes times out") {
    auto dir = nipa::test::make_temp_dir();
    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/opt/nipa/nipa";
    options.spawn = [](const std::string&, const std::string&) { return true; };
    try {
        nipa::daemon::connect(options);
        FAIL("expected ConnectError");
    } catch (const ConnectError& e) {
        CHECK(e.reason == ConnectFailure::StartTimeout);
    }
    std::filesystem::remove_all(dir);
}

TEST_CASE("cancel stops the connector before and during the poll") {
    auto dir = nipa::test::make_temp_dir();
    CancelToken early;
    early.cancel();
    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/opt/nipa/nipa";
    CHECK_THROWS_AS(nipa::daemon::connect(options, &early), CancelledError);

    CancelToken late;
    options.spawn = [&late](const std::string&, const std::string&) {
        late.cancel();
        return true;
    };
    CHECK_THROWS_AS(nipa::daemon::connect(options, &late), CancelledError);
    std::filesystem::remove_all(dir);
}

TEST_CASE("a stale daemon.json falls through to spawning") {
    auto dir = nipa::test::make_temp_dir();
    nipa::test::write_endpoint(dir + "/daemon.json", 1, "tok", "0.2.0");
    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/opt/nipa/nipa";
    options.spawn = [](const std::string&, const std::string&) { return true; };
    try {
        nipa::daemon::connect(options);
        FAIL("expected ConnectError");
    } catch (const ConnectError& e) {
        CHECK(e.reason == ConnectFailure::StartTimeout);
    }
    std::filesystem::remove_all(dir);
}

TEST_CASE("the default endpoint location is used when none is configured") {
    auto home = nipa::test::make_temp_dir();
    FakeDaemon fake;
    serve_ping(fake, "0.2.0");
    int port = fake.start();
    nipa::test::write_endpoint(home + "/.config/nipa/daemon.json", port, fake.token());

    auto options = options_for("");
    options.env = [&home](const std::string& name) { return name == "HOME" ? home : std::string(); };
    auto attached = nipa::daemon::connect(options);
    CHECK(attached.version == "0.2.0");
    fake.stop();
    std::filesystem::remove_all(home);
}

TEST_CASE("binary lookup order is setting, NIPA_BIN, library directory, then PATH") {
    auto options = options_for("/unused");
    options.library_dir = "/games/addons/nipa/bin";
    options.env = [](const std::string& name) {
        if (name == "NIPA_BIN") return std::string("/env/nipa");
        if (name == "PATH") return std::string("/usr/bin:/bin");
        return std::string();
    };
    options.file_exists = [](const std::string& path) {
        return path == "/games/addons/nipa/bin/nipa" || path == "/bin/nipa";
    };

    options.binary_setting = "/setting/nipa";
    CHECK(nipa::daemon::resolve_binary(options) == "/setting/nipa");
    options.binary_setting.clear();
    CHECK(nipa::daemon::resolve_binary(options) == "/env/nipa");
    options.env = [](const std::string& name) { return name == "PATH" ? std::string("/usr/bin:/bin") : std::string(); };
    CHECK(nipa::daemon::resolve_binary(options) == "/games/addons/nipa/bin/nipa");
    options.library_dir.clear();
    CHECK(nipa::daemon::resolve_binary(options) == "/bin/nipa");
    options.file_exists = [](const std::string&) { return false; };
    CHECK_FALSE(nipa::daemon::resolve_binary(options).has_value());
}

TEST_CASE("Windows lookup uses nipa.exe and semicolon-separated PATH") {
    auto options = options_for("C:\\unused");
    options.os = HostOs::Windows;
    options.library_dir = "C:\\game\\addons\\nipa\\bin\\";
    options.file_exists = [](const std::string& path) { return path == "C:\\game\\addons\\nipa\\bin\\nipa.exe"; };
    CHECK(nipa::daemon::resolve_binary(options) == "C:\\game\\addons\\nipa\\bin\\nipa.exe");

    options.library_dir.clear();
    options.env = [](const std::string& name) {
        return name == "PATH" ? std::string("C:\\empty;;D:\\tools") : std::string();
    };
    options.file_exists = [](const std::string& path) { return path == "D:\\tools\\nipa.exe"; };
    CHECK(nipa::daemon::resolve_binary(options) == "D:\\tools\\nipa.exe");
}

TEST_CASE("PATH lookup handles an empty PATH and a directory with a trailing separator") {
    auto options = options_for("/unused");
    options.file_exists = [](const std::string& path) { return path == "/opt/bin/nipa"; };
    CHECK_FALSE(nipa::daemon::resolve_binary(options).has_value());
    options.env = [](const std::string& name) { return name == "PATH" ? std::string("/opt/bin/") : std::string(); };
    CHECK(nipa::daemon::resolve_binary(options) == "/opt/bin/nipa");
}

TEST_CASE("the default spawner starts a detached process") {
    CHECK(nipa::daemon::spawn_detached("/bin/true", ""));
    CHECK(nipa::daemon::spawn_detached("/bin/true", "/tmp/daemon.json"));
}

TEST_CASE("connect with the default spawner reports a daemon that never starts") {
    auto dir = nipa::test::make_temp_dir();
    auto options = options_for(dir + "/daemon.json");
    options.binary_setting = "/bin/true";
    options.spawn = nullptr;
    options.start_timeout = 100ms;
    CHECK_THROWS_AS(nipa::daemon::connect(options), ConnectError);
    std::filesystem::remove_all(dir);
}
