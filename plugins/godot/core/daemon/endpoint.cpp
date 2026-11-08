#include "core/daemon/endpoint.h"

#include <fstream>
#include <sstream>

#include <nlohmann/json.hpp>

namespace nipa::daemon {

HostOs host_os() {
#if defined(_WIN32)
    return HostOs::Windows;
#elif defined(__APPLE__)
    return HostOs::MacOs;
#else
    return HostOs::Linux;
#endif
}

std::string default_endpoint_path(const GetEnv& env, HostOs os) {
    const char* sep = os == HostOs::Windows ? "\\" : "/";
    std::string home = env("HOME");
    if (home.empty()) {
        home = env("USERPROFILE");
    }
    std::string dir;
    switch (os) {
        case HostOs::Windows:
            dir = env("APPDATA");
            if (dir.empty()) {
                dir = home + sep + "AppData" + sep + "Roaming";
            }
            break;
        case HostOs::MacOs:
            dir = home + "/Library/Application Support";
            break;
        case HostOs::Linux:
            dir = env("XDG_CONFIG_HOME");
            if (dir.empty()) {
                dir = home + "/.config";
            }
            break;
    }
    return dir + sep + "nipa" + sep + "daemon.json";
}

std::optional<Endpoint> read_endpoint(const std::string& path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) {
        return std::nullopt;
    }
    std::stringstream buffer;
    buffer << in.rdbuf();
    try {
        auto j = nlohmann::json::parse(buffer.str());
        if (!j.is_object()) {
            return std::nullopt;
        }
        Endpoint ep;
        ep.pid = j.value("pid", 0);
        ep.port = j.value("port", 0);
        ep.json_port = j.value("json_port", 0);
        ep.token = j.value("token", std::string());
        ep.version = j.value("version", std::string());
        return ep;
    } catch (const nlohmann::json::exception&) {
        return std::nullopt;
    }
}

}  // namespace nipa::daemon
