#pragma once

#include <chrono>
#include <functional>
#include <memory>
#include <optional>
#include <stdexcept>
#include <string>

#include "core/daemon/cancel.h"
#include "core/daemon/client.h"

namespace nipa::daemon {

enum class ConnectFailure { OldDaemon, BinaryNotFound, StartFailed, StartTimeout };

struct ConnectError : std::runtime_error {
    ConnectError(ConnectFailure reason, const std::string& message);

    ConnectFailure reason;
};

struct ConnectOptions {
    std::string endpoint_path;   // empty = default location
    std::string binary_setting;  // the editor setting, first in the lookup order
    std::string min_version;     // older daemons are shut down and respawned
    std::string library_dir;     // directory of the extension library
    std::chrono::milliseconds start_timeout{10000};
    std::chrono::milliseconds poll_interval{100};
    std::chrono::milliseconds ping_timeout{2000};

    GetEnv env;
    HostOs os = host_os();
    std::function<bool(const std::string&)> file_exists;
    std::function<bool(const std::string& binary, const std::string& endpoint_path)> spawn;
};

struct Attached {
    std::shared_ptr<Client> client;
    std::string version;
    bool started = false;
};

// Attaches to a running daemon or starts one. Order: attach and Ping; shut an
// older daemon down; resolve the binary (setting, NIPA_BIN, next to the
// library, PATH); spawn; poll daemon.json until a Ping answers.
Attached connect(const ConnectOptions& options, CancelToken* cancel = nullptr);

// Lookup order for the nipa binary, nullopt when none exists.
std::optional<std::string> resolve_binary(const ConnectOptions& options);

}  // namespace nipa::daemon
