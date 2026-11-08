#include <doctest.h>

#include <filesystem>
#include <fstream>

#include "core/daemon/endpoint.h"
#include "fake_daemon.h"

using nipa::daemon::default_endpoint_path;
using nipa::daemon::HostOs;

namespace {

nipa::daemon::GetEnv env_of(std::map<std::string, std::string> values) {
    return [values](const std::string& name) {
        auto found = values.find(name);
        return found == values.end() ? std::string() : found->second;
    };
}

}  // namespace

TEST_CASE("default endpoint follows each OS convention") {
    CHECK(default_endpoint_path(env_of({{"XDG_CONFIG_HOME", "/xdg"}, {"HOME", "/home/u"}}), HostOs::Linux) ==
          "/xdg/nipa/daemon.json");
    CHECK(default_endpoint_path(env_of({{"HOME", "/home/u"}}), HostOs::Linux) == "/home/u/.config/nipa/daemon.json");
    CHECK(default_endpoint_path(env_of({{"HOME", "/Users/u"}}), HostOs::MacOs) ==
          "/Users/u/Library/Application Support/nipa/daemon.json");
    CHECK(default_endpoint_path(env_of({{"APPDATA", "C:\\Data"}}), HostOs::Windows) == "C:\\Data\\nipa\\daemon.json");
    CHECK(default_endpoint_path(env_of({{"USERPROFILE", "C:\\Users\\u"}}), HostOs::Windows) ==
          "C:\\Users\\u\\AppData\\Roaming\\nipa\\daemon.json");
}

TEST_CASE("host_os resolves to a known value") {
    auto os = nipa::daemon::host_os();
    CHECK((os == HostOs::Linux || os == HostOs::MacOs || os == HostOs::Windows));
}

TEST_CASE("read_endpoint parses a published record") {
    auto dir = nipa::test::make_temp_dir();
    auto path = dir + "/daemon.json";
    nipa::test::write_endpoint(path, 9001, "tok", "1.2.3");
    auto ep = nipa::daemon::read_endpoint(path);
    REQUIRE(ep.has_value());
    CHECK(ep->json_port == 9001);
    CHECK(ep->token == "tok");
    CHECK(ep->version == "1.2.3");
    CHECK(ep->pid == 4242);
    std::filesystem::remove_all(dir);
}

TEST_CASE("read_endpoint keeps a missing json_port as zero") {
    auto dir = nipa::test::make_temp_dir();
    auto path = dir + "/daemon.json";
    nipa::test::write_endpoint(path, 0, "tok", "0.1.0");
    auto ep = nipa::daemon::read_endpoint(path);
    REQUIRE(ep.has_value());
    CHECK(ep->json_port == 0);
    std::filesystem::remove_all(dir);
}

TEST_CASE("read_endpoint rejects missing and malformed files") {
    auto dir = nipa::test::make_temp_dir();
    CHECK_FALSE(nipa::daemon::read_endpoint(dir + "/absent.json").has_value());
    {
        std::ofstream(dir + "/broken.json") << "{not json";
        CHECK_FALSE(nipa::daemon::read_endpoint(dir + "/broken.json").has_value());
    }
    {
        std::ofstream(dir + "/array.json") << "[1,2]";
        CHECK_FALSE(nipa::daemon::read_endpoint(dir + "/array.json").has_value());
    }
    std::filesystem::remove_all(dir);
}
