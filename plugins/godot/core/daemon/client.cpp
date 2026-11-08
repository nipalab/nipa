#include "core/daemon/client.h"

#include <optional>

#include <httplib.h>

namespace nipa::daemon {
namespace {

constexpr const char* kTokenHeader = "x-nipa-daemon-token";
constexpr const char* kJsonType = "application/json";
constexpr std::chrono::milliseconds kConnectTimeout{2000};

std::string trim(const std::string& text) {
    std::size_t begin = text.find_first_not_of(" \t\r\n");
    if (begin == std::string::npos) {
        return {};
    }
    std::size_t end = text.find_last_not_of(" \t\r\n");
    return text.substr(begin, end - begin + 1);
}

DaemonError error_from_body(int status, const std::string& body) {
    std::string message;
    std::string code;
    try {
        auto j = nlohmann::json::parse(body);
        if (j.is_object()) {
            code = j.value("code", std::string());
            message = j.value("message", std::string());
        }
    } catch (const nlohmann::json::exception&) {
    }
    if (message.empty()) {
        message = trim(body).empty() ? "HTTP " + std::to_string(status) : trim(body);
    }
    return DaemonError(status, code, message);
}

DaemonError error_from_envelope(const nlohmann::json& error) {
    std::string code = error.value("code", std::string());
    std::string message = error.value("message", std::string());
    if (message.empty()) {
        message = code.empty() ? "stream failed" : code;
    }
    return DaemonError(http_status_for_code(code), code, message);
}

std::optional<DaemonError> transport_error(httplib::Error error, const std::string& method) {
    if (error == httplib::Error::Connection) {
        return DaemonError(503, "Unavailable", "daemon unreachable");
    }
    return DaemonError(504, "DeadlineExceeded", method + " timed out");
}

httplib::Client make_client(const Endpoint& ep, std::chrono::milliseconds timeout) {
    httplib::Client cli("127.0.0.1", ep.json_port);
    cli.set_connection_timeout(kConnectTimeout);
    cli.set_read_timeout(timeout);
    cli.set_write_timeout(timeout);
    return cli;
}

}  // namespace

DaemonError::DaemonError(int status, std::string code_name, const std::string& message)
    : std::runtime_error(message), http_status(status), code(std::move(code_name)) {}

int http_status_for_code(const std::string& code) {
    if (code == "InvalidArgument" || code == "OutOfRange") return 400;
    if (code == "Unauthenticated") return 401;
    if (code == "PermissionDenied") return 403;
    if (code == "NotFound") return 404;
    if (code == "AlreadyExists" || code == "Aborted" || code == "FailedPrecondition") return 409;
    if (code == "ResourceExhausted") return 429;
    if (code == "Canceled") return 499;
    if (code == "Unimplemented") return 501;
    if (code == "Unavailable") return 503;
    if (code == "DeadlineExceeded") return 504;
    return 500;
}

Client::Client(Endpoint endpoint) : endpoint_(std::move(endpoint)) {
    if (endpoint_.json_port <= 0) {
        throw std::invalid_argument("daemon does not publish a json_port; update the nipa CLI");
    }
}

nlohmann::json Client::call(const std::string& method, const nlohmann::json& body,
                            std::chrono::milliseconds timeout, CancelToken* cancel) const {
    if (cancel && cancel->cancelled()) {
        throw CancelledError();
    }
    httplib::Client cli = make_client(endpoint_, timeout);
    httplib::Headers headers{{kTokenHeader, endpoint_.token}};
    std::size_t id = cancel ? cancel->add_callback([&cli] { cli.stop(); }) : 0;
    auto res = cli.Post("/v1/" + method, headers, body.dump(), kJsonType);
    if (cancel) {
        cancel->remove_callback(id);
        if (cancel->cancelled()) {
            throw CancelledError();
        }
    }
    if (!res) {
        throw *transport_error(res.error(), method);
    }
    if (res->status < 200 || res->status >= 300) {
        throw error_from_body(res->status, res->body);
    }
    try {
        return nlohmann::json::parse(res->body);
    } catch (const nlohmann::json::exception&) {
        throw DaemonError(500, "Internal", method + " returned a malformed response");
    }
}

void Client::stream(const std::string& method, const nlohmann::json& body, const Item& on_item,
                    std::chrono::milliseconds read_timeout, CancelToken* cancel) const {
    if (cancel && cancel->cancelled()) {
        throw CancelledError();
    }
    httplib::Client cli = make_client(endpoint_, read_timeout);

    int status = 200;
    std::string error_body;
    std::string buffer;
    bool stopped_by_consumer = false;
    std::optional<DaemonError> failure;

    auto handle_line = [&](std::string line) -> bool {
        line = trim(line);
        if (line.empty()) {
            return true;
        }
        nlohmann::json item;
        try {
            item = nlohmann::json::parse(line);
        } catch (const nlohmann::json::exception&) {
            failure = DaemonError(500, "Internal", "malformed stream line");
            return false;
        }
        if (item.is_object() && item.contains("error") && item["error"].is_object()) {
            failure = error_from_envelope(item["error"]);
            return false;
        }
        if (!on_item(item)) {
            stopped_by_consumer = true;
            return false;
        }
        return true;
    };

    httplib::Request req;
    req.method = "POST";
    req.path = "/v1/" + method;
    req.headers = httplib::Headers{{kTokenHeader, endpoint_.token}, {"Content-Type", kJsonType}};
    req.body = body.dump();
    req.response_handler = [&status](const httplib::Response& response) {
        status = response.status;
        return true;
    };
    req.content_receiver = [&](const char* data, std::size_t length, std::size_t, std::size_t) {
        if (status < 200 || status >= 300) {
            error_body.append(data, length);
            return true;
        }
        if (cancel && cancel->cancelled()) {
            return false;
        }
        buffer.append(data, length);
        std::size_t newline;
        while ((newline = buffer.find('\n')) != std::string::npos) {
            std::string line = buffer.substr(0, newline);
            buffer.erase(0, newline + 1);
            if (!handle_line(std::move(line))) {
                return false;
            }
        }
        return true;
    };

    std::size_t id = cancel ? cancel->add_callback([&cli] { cli.stop(); }) : 0;
    httplib::Response res;
    httplib::Error error = httplib::Error::Success;
    bool sent = cli.send(req, res, error);
    if (cancel) {
        cancel->remove_callback(id);
    }
    if (cancel && cancel->cancelled()) {
        throw CancelledError();
    }
    if (failure) {
        throw *failure;
    }
    if (status < 200 || status >= 300) {
        throw error_from_body(status, error_body);
    }
    if (!sent) {
        if (stopped_by_consumer) {
            return;
        }
        throw *transport_error(error, method);
    }
    if (!buffer.empty() && !stopped_by_consumer) {
        handle_line(buffer);
        if (failure) {
            throw *failure;
        }
    }
}

}  // namespace nipa::daemon
