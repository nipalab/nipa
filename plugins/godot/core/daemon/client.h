#pragma once

#include <chrono>
#include <functional>
#include <stdexcept>
#include <string>

#include <nlohmann/json.hpp>

#include "core/daemon/cancel.h"
#include "core/daemon/endpoint.h"

namespace nipa::daemon {

// HTTP-style status (mirrors the bridge's gRPC code mapping) plus the gRPC code name.
struct DaemonError : std::runtime_error {
    DaemonError(int status, std::string code, const std::string& message);

    int http_status;
    std::string code;
};

class Client {
public:
    using Item = std::function<bool(const nlohmann::json&)>;

    explicit Client(Endpoint endpoint);

    const Endpoint& endpoint() const { return endpoint_; }

    // POST /v1/<method>. Throws DaemonError or CancelledError.
    nlohmann::json call(const std::string& method, const nlohmann::json& body,
                        std::chrono::milliseconds timeout, CancelToken* cancel = nullptr) const;

    // NDJSON stream. `on_item` returns false to stop reading early (a normal
    // return). Throws DaemonError on an error line, a malformed line, a
    // transport failure or a read timeout, and CancelledError on cancel.
    void stream(const std::string& method, const nlohmann::json& body, const Item& on_item,
                std::chrono::milliseconds read_timeout, CancelToken* cancel = nullptr) const;

private:
    Endpoint endpoint_;
};

int http_status_for_code(const std::string& grpc_code);

}  // namespace nipa::daemon
