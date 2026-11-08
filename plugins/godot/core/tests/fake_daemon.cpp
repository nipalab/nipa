#include "fake_daemon.h"

#include <chrono>
#include <filesystem>
#include <fstream>
#include <stdexcept>
#include <random>
#ifndef _WIN32
#endif

namespace nipa::test {
namespace {

constexpr const char* kTokenHeader = "x-nipa-daemon-token";

void write_json(httplib::Response& res, int status, const nlohmann::json& body) {
    res.status = status;
    res.set_content(body.dump(), "application/json");
}

struct StreamState {
    std::vector<std::string> lines;
    std::size_t next = 0;
    int delay_ms = 0;
    bool endless = false;
    const std::atomic<bool>* running = nullptr;
    std::chrono::steady_clock::time_point last;
};

}  // namespace

FakeDaemon::FakeDaemon(std::string token) : token_(std::move(token)) {
    server_.new_task_queue = [] { return new httplib::ThreadPool(32); };
    server_.Post(R"(/v1/(\w+))", [this](const httplib::Request& req, httplib::Response& res) {
        std::string method = req.matches[1];
        std::string token = req.get_header_value(kTokenHeader);
        nlohmann::json body = nlohmann::json::object();
        if (!req.body.empty()) {
            try {
                body = nlohmann::json::parse(req.body);
            } catch (const nlohmann::json::exception&) {
                write_json(res, 400, {{"code", "InvalidArgument"}, {"message", "decode body"}});
                return;
            }
        }
        {
            std::lock_guard<std::mutex> guard(mu_);
            requests_.push_back(Request{method, token, body});
        }
        if (token != token_) {
            write_json(res, 401, {{"code", "Unauthenticated"}, {"message", "bad token"}});
            return;
        }

        StreamHandler stream_handler;
        UnaryHandler unary_handler;
        {
            std::lock_guard<std::mutex> guard(mu_);
            if (auto it = stream_.find(method); it != stream_.end()) {
                stream_handler = it->second;
            } else if (auto found = unary_.find(method); found != unary_.end()) {
                unary_handler = found->second;
            }
        }
        if (stream_handler) {
            StreamScript script = stream_handler(body);
            if (script.status != 200) {
                write_json(res, script.status, {{"code", "NotFound"}, {"message", "stream refused"}});
                return;
            }
            auto state = std::make_shared<StreamState>();
            state->lines = script.lines;
            state->delay_ms = script.delay_ms;
            state->endless = script.endless;
            state->running = &running_;
            state->last = std::chrono::steady_clock::now();
            res.set_chunked_content_provider(
                "application/x-ndjson",
                [state](std::size_t, httplib::DataSink& sink) {
                    if (!state->running->load()) {
                        sink.done();
                        return true;
                    }
                    if (state->delay_ms > 0) {
                        std::this_thread::sleep_for(std::chrono::milliseconds(state->delay_ms));
                    }
                    if (state->endless && state->lines.empty()) {
                        return true;
                    }
                    if (state->endless) {
                        std::string line = state->lines.empty() ? "{}" : state->lines.front();
                        return sink.write((line + "\n").data(), line.size() + 1);
                    }
                    if (state->next >= state->lines.size()) {
                        sink.done();
                        return true;
                    }
                    std::string line = state->lines[state->next++] + "\n";
                    return sink.write(line.data(), line.size());
                },
                [](bool) {});
            return;
        }

        if (!unary_handler) {
            write_json(res, 501, {{"code", "Unimplemented"}, {"message", "no handler for " + method}});
            return;
        }
        Reply reply = unary_handler(body);
        if (reply.raw) {
            res.status = reply.status;
            res.set_content(*reply.raw, "application/json");
            return;
        }
        write_json(res, reply.status, reply.body);
    });
}

FakeDaemon::~FakeDaemon() {
    stop();
}

void FakeDaemon::on_unary(const std::string& method, UnaryHandler handler) {
    std::lock_guard<std::mutex> guard(mu_);
    unary_[method] = std::move(handler);
}

void FakeDaemon::on_stream(const std::string& method, StreamHandler handler) {
    std::lock_guard<std::mutex> guard(mu_);
    stream_[method] = std::move(handler);
}

int FakeDaemon::start() {
    running_ = true;
    port_ = server_.bind_to_any_port("127.0.0.1");
    thread_ = std::thread([this] { server_.listen_after_bind(); });
    return port_;
}

void FakeDaemon::stop() {
    running_ = false;
    server_.stop();
    if (thread_.joinable()) {
        thread_.join();
    }
}

std::vector<FakeDaemon::Request> FakeDaemon::requests() const {
    std::lock_guard<std::mutex> guard(mu_);
    return requests_;
}

int FakeDaemon::count(const std::string& method) const {
    std::lock_guard<std::mutex> guard(mu_);
    int total = 0;
    for (const auto& request : requests_) {
        if (request.method == method) {
            ++total;
        }
    }
    return total;
}

// Test dirs live under the build tree (ctest's working directory), never in a shared temp dir.
std::string make_temp_dir() {
    static std::atomic<unsigned> counter{0};
    std::random_device device;
    auto base = std::filesystem::current_path() / "test-tmp";
    std::filesystem::create_directories(base);
    while (true) {
        auto path = base / ("nipa-godot-" + std::to_string(device()) + "-" + std::to_string(counter++));
        if (std::filesystem::create_directory(path)) {
            std::filesystem::permissions(path, std::filesystem::perms::owner_all,
                                         std::filesystem::perm_options::replace);
            return path.string();
        }
    }
}

void write_endpoint(const std::string& path, int json_port, const std::string& token, const std::string& version) {
    std::filesystem::create_directories(std::filesystem::path(path).parent_path());
    nlohmann::json j = {{"pid", 4242}, {"port", 0}, {"token", token}, {"version", version}};
    if (json_port > 0) {
        j["json_port"] = json_port;
    }
    std::ofstream out(path, std::ios::trunc);
    out << j.dump();
}

}  // namespace nipa::test
