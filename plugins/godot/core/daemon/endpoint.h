#pragma once

#include <functional>
#include <optional>
#include <string>

namespace nipa::daemon {

struct Endpoint {
    int pid = 0;
    int port = 0;
    int json_port = 0;
    std::string token;
    std::string version;
};

enum class HostOs { Windows, MacOs, Linux };

using GetEnv = std::function<std::string(const std::string&)>;

HostOs host_os();

// Where `nipa serve` publishes daemon.json: %AppData%\nipa on Windows,
// ~/Library/Application Support/nipa on macOS, $XDG_CONFIG_HOME/nipa or
// ~/.config/nipa elsewhere.
std::string default_endpoint_path(const GetEnv& env, HostOs os);

// Missing or malformed files yield nullopt; a missing json_port is kept as 0.
std::optional<Endpoint> read_endpoint(const std::string& path);

}  // namespace nipa::daemon
