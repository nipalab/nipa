#pragma once

#include <atomic>
#include <functional>
#include <optional>
#include <map>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include <httplib.h>
#include <nlohmann/json.hpp>

namespace nipa::test {

struct Reply {
    int status = 200;
    nlohmann::json body;
    std::optional<std::string> raw;  // sent verbatim instead of the body
};

// Lines are written one per chunk. `endless` repeats the first line every `delay_ms`
// until the client goes away or the daemon stops; with no lines it stays silent.
struct StreamScript {
    int status = 200;
    std::vector<std::string> lines;
    int delay_ms = 0;
    bool endless = false;
};

class FakeDaemon {
public:
    struct Request {
        std::string method;
        std::string token;
        nlohmann::json body;
    };
    using UnaryHandler = std::function<Reply(const nlohmann::json& request)>;
    using StreamHandler = std::function<StreamScript(const nlohmann::json& request)>;

    explicit FakeDaemon(std::string token = "secret");
    ~FakeDaemon();

    FakeDaemon(const FakeDaemon&) = delete;
    FakeDaemon& operator=(const FakeDaemon&) = delete;

    void on_unary(const std::string& method, UnaryHandler handler);
    void on_stream(const std::string& method, StreamHandler handler);

    // Binds 127.0.0.1 on a free port and serves in the background.
    int start();
    void stop();

    int port() const { return port_; }
    const std::string& token() const { return token_; }
    std::vector<Request> requests() const;
    int count(const std::string& method) const;

private:
    httplib::Server server_;
    std::thread thread_;
    std::string token_;
    int port_ = 0;
    std::atomic<bool> running_{false};
    mutable std::mutex mu_;
    std::map<std::string, UnaryHandler> unary_;
    std::map<std::string, StreamHandler> stream_;
    std::vector<Request> requests_;
};

// Fresh empty directory under the system temp dir.
std::string make_temp_dir();

// Writes a daemon.json like `nipa serve` does. `json_port` 0 omits the bridge.
void write_endpoint(const std::string& path, int json_port, const std::string& token,
                    const std::string& version = "0.2.0");

}  // namespace nipa::test
