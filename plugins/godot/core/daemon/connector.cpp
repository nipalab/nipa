#include "core/daemon/connector.h"

#include <thread>

#include "core/daemon/spawn.h"
#include "core/version.h"

namespace nipa::daemon {
namespace {

std::string join_path(const std::string& dir, const std::string& name, HostOs os) {
    if (dir.empty()) {
        return name;
    }
    char last = dir.back();
    if (last == '/' || last == '\\') {
        return dir + name;
    }
    return dir + (os == HostOs::Windows ? "\\" : "/") + name;
}

std::string binary_name(HostOs os) {
    return os == HostOs::Windows ? "nipa.exe" : "nipa";
}

std::optional<std::string> search_path(const ConnectOptions& options) {
    std::string path = options.env("PATH");
    if (path.empty()) {
        return std::nullopt;
    }
    char separator = options.os == HostOs::Windows ? ';' : ':';
    std::size_t start = 0;
    while (start <= path.size()) {
        std::size_t end = path.find(separator, start);
        if (end == std::string::npos) {
            end = path.size();
        }
        std::string dir = path.substr(start, end - start);
        if (!dir.empty()) {
            std::string candidate = join_path(dir, binary_name(options.os), options.os);
            if (options.file_exists(candidate)) {
                return candidate;
            }
        }
        start = end + 1;
    }
    return std::nullopt;
}

std::string endpoint_path_for(const ConnectOptions& options) {
    if (!options.endpoint_path.empty()) {
        return options.endpoint_path;
    }
    return default_endpoint_path(options.env, options.os);
}

// Attaches to the daemon behind `path` when it answers Ping. A daemon that
// predates the JSON bridge is an error, not something to replace silently.
std::optional<Attached> try_attach(const std::string& path, const ConnectOptions& options,
                                   CancelToken* cancel) {
    auto endpoint = read_endpoint(path);
    if (!endpoint) {
        return std::nullopt;
    }
    if (endpoint->json_port <= 0) {
        throw ConnectError(ConnectFailure::OldDaemon,
                           "The running nipa daemon (pid " + std::to_string(endpoint->pid) +
                               ", version " + endpoint->version +
                               ") predates the JSON bridge. Stop it and update the nipa CLI, then reconnect.");
    }
    auto client = std::make_shared<Client>(*endpoint);
    try {
        auto pong = client->call("Ping", nlohmann::json::object(), options.ping_timeout, cancel);
        return Attached{client, pong.value("version", std::string()), false};
    } catch (const DaemonError&) {
        return std::nullopt;
    }
}

void throw_if_cancelled(CancelToken* cancel) {
    if (cancel && cancel->cancelled()) {
        throw CancelledError();
    }
}

void sleep_checked(std::chrono::milliseconds duration, CancelToken* cancel) {
    auto deadline = std::chrono::steady_clock::now() + duration;
    while (std::chrono::steady_clock::now() < deadline) {
        throw_if_cancelled(cancel);
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    throw_if_cancelled(cancel);
}

// Asks an old daemon to exit and waits until its Ping stops answering.
void retire(const Attached& old, const ConnectOptions& options, CancelToken* cancel) {
    try {
        old.client->call("Shutdown", nlohmann::json::object(), options.ping_timeout, cancel);
    } catch (const DaemonError&) {
    }
    auto deadline = std::chrono::steady_clock::now() + options.start_timeout;
    while (std::chrono::steady_clock::now() < deadline) {
        try {
            old.client->call("Ping", nlohmann::json::object(), options.ping_timeout, cancel);
        } catch (const DaemonError&) {
            return;
        }
        sleep_checked(options.poll_interval, cancel);
    }
}

}  // namespace

ConnectError::ConnectError(ConnectFailure failure, const std::string& message)
    : std::runtime_error(message), reason(failure) {}

std::optional<std::string> resolve_binary(const ConnectOptions& options) {
    if (!options.binary_setting.empty()) {
        return options.binary_setting;
    }
    std::string from_env = options.env("NIPA_BIN");
    if (!from_env.empty()) {
        return from_env;
    }
    if (!options.library_dir.empty()) {
        std::string beside = join_path(options.library_dir, binary_name(options.os), options.os);
        if (options.file_exists(beside)) {
            return beside;
        }
    }
    return search_path(options);
}

Attached connect(const ConnectOptions& options, CancelToken* cancel) {
    std::string path = endpoint_path_for(options);
    throw_if_cancelled(cancel);

    auto existing = try_attach(path, options, cancel);
    if (existing && !version_older(existing->version, options.min_version)) {
        return *existing;
    }
    if (existing) {
        retire(*existing, options, cancel);
    }

    auto binary = resolve_binary(options);
    if (!binary) {
        throw ConnectError(ConnectFailure::BinaryNotFound,
                           "nipa CLI not found: set the binary path in the Nipa settings, NIPA_BIN or add it to PATH");
    }
    bool started = options.spawn ? options.spawn(*binary, options.endpoint_path)
                                 : spawn_detached(*binary, options.endpoint_path);
    if (!started) {
        throw ConnectError(ConnectFailure::StartFailed, "could not start " + *binary + " serve");
    }

    auto deadline = std::chrono::steady_clock::now() + options.start_timeout;
    while (true) {
        auto attached = try_attach(path, options, cancel);
        if (attached && !version_older(attached->version, options.min_version)) {
            attached->started = true;
            return *attached;
        }
        if (std::chrono::steady_clock::now() >= deadline) {
            throw ConnectError(ConnectFailure::StartTimeout,
                               "nipa daemon did not start within " +
                                   std::to_string(options.start_timeout.count() / 1000.0) + "s");
        }
        sleep_checked(options.poll_interval, cancel);
    }
}

}  // namespace nipa::daemon
