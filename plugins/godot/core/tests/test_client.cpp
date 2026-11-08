#include <doctest.h>

#include <atomic>
#include <chrono>
#include <thread>

#include "core/daemon/client.h"
#include "fake_daemon.h"

using namespace std::chrono_literals;
using nipa::daemon::CancelledError;
using nipa::daemon::CancelToken;
using nipa::daemon::Client;
using nipa::daemon::DaemonError;
using nlohmann::json;

namespace {

nipa::daemon::Endpoint endpoint_for(const nipa::test::FakeDaemon& fake, std::string token = "secret") {
    nipa::daemon::Endpoint ep;
    ep.json_port = fake.port();
    ep.token = std::move(token);
    return ep;
}

}  // namespace

TEST_CASE("client rejects an endpoint without json_port") {
    nipa::daemon::Endpoint ep;
    CHECK_THROWS_AS(Client{ep}, std::invalid_argument);
}

TEST_CASE("unary call returns the decoded body and sends the token") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Ping", [](const json&) { return nipa::test::Reply{200, {{"version", "0.2.0"}, {"pid", 7}}}; });
    fake.start();
    Client client(endpoint_for(fake));
    auto reply = client.call("Ping", json::object(), 2s);
    CHECK(reply["version"] == "0.2.0");
    auto requests = fake.requests();
    REQUIRE(requests.size() == 1);
    CHECK(requests[0].token == "secret");
    fake.stop();
}

TEST_CASE("unary error body maps to DaemonError with code and status") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Status", [](const json&) {
        return nipa::test::Reply{404, {{"code", "NotFound"}, {"message", "no repo"}}};
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.call("Status", json::object(), 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 404);
        CHECK(e.code == "NotFound");
        CHECK(std::string(e.what()) == "no repo");
    }
    fake.stop();
}

TEST_CASE("plain text error body is kept as the message") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Status", [](const json&) { return nipa::test::Reply{500, json("boom")}; });
    fake.start();
    Client client(endpoint_for(fake));
    CHECK_THROWS_WITH_AS(client.call("Status", json::object(), 2s), "\"boom\"", DaemonError);
    fake.stop();
}

TEST_CASE("empty error body falls back to the HTTP status") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Status", [](const json&) { return nipa::test::Reply{502, json::object(), std::string()}; });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.call("Status", json::object(), 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 502);
        CHECK(std::string(e.what()) == "HTTP 502");
    }
    fake.stop();
}

TEST_CASE("bad token is refused with 401") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Ping", [](const json&) { return nipa::test::Reply{}; });
    fake.start();
    Client client(endpoint_for(fake, "wrong"));
    try {
        client.call("Ping", json::object(), 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 401);
        CHECK(e.code == "Unauthenticated");
    }
    fake.stop();
}

TEST_CASE("unknown method answers 501") {
    nipa::test::FakeDaemon fake;
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.call("Missing", json::object(), 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 501);
    }
    fake.stop();
}

TEST_CASE("a non-JSON success body is a malformed response") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Ping", [](const json&) { return nipa::test::Reply{200, json::object(), "not json"}; });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.call("Ping", json::object(), 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 500);
    }
    fake.stop();
}

TEST_CASE("slow daemon hits the call timeout") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Status", [](const json&) {
        std::this_thread::sleep_for(600ms);
        return nipa::test::Reply{200, json::object()};
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.call("Status", json::object(), 100ms);
        FAIL("expected timeout");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 504);
        CHECK(e.code == "DeadlineExceeded");
    }
    fake.stop();
}

TEST_CASE("unreachable daemon maps to 503") {
    nipa::test::FakeDaemon fake;
    fake.start();
    auto ep = endpoint_for(fake);
    fake.stop();
    Client client(ep);
    try {
        client.call("Ping", json::object(), 500ms);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 503);
        CHECK(e.code == "Unavailable");
    }
}

TEST_CASE("cancel before a call throws without sending") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Ping", [](const json&) { return nipa::test::Reply{200, json::object()}; });
    fake.start();
    Client client(endpoint_for(fake));
    CancelToken token;
    token.cancel();
    CHECK_THROWS_AS(client.call("Ping", json::object(), 2s, &token), CancelledError);
    CHECK(fake.count("Ping") == 0);
    fake.stop();
}

TEST_CASE("cancel from another thread aborts a call in flight") {
    nipa::test::FakeDaemon fake;
    fake.on_unary("Status", [](const json&) {
        std::this_thread::sleep_for(3s);
        return nipa::test::Reply{200, json::object()};
    });
    fake.start();
    Client client(endpoint_for(fake));
    CancelToken token;
    std::thread canceller([&token] {
        std::this_thread::sleep_for(100ms);
        token.cancel();
    });
    CHECK_THROWS_AS(client.call("Status", json::object(), 10s, &token), CancelledError);
    canceller.join();
    fake.stop();
}

TEST_CASE("stream delivers every item in order") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchChanges", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"paths":["a.txt"]})", "", R"({"paths":["b.txt"]})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    std::vector<std::string> seen;
    client.stream("WatchChanges", json::object(), [&seen](const json& item) {
        seen.push_back(item["paths"][0].get<std::string>());
        return true;
    }, 2s);
    CHECK(seen == std::vector<std::string>{"a.txt", "b.txt"});
    fake.stop();
}

TEST_CASE("stream keeps a final line without a newline") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchChanges", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"n":1})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    int count = 0;
    client.stream("WatchChanges", json::object(), [&count](const json&) {
        ++count;
        return true;
    }, 2s);
    CHECK(count == 1);
    fake.stop();
}

TEST_CASE("consumer can stop a stream early") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchChanges", [](const json&) {
        nipa::test::StreamScript script;
        script.endless = true;
        script.lines = {R"({"n":1})"};
        script.delay_ms = 5;
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    int count = 0;
    client.stream("WatchChanges", json::object(), [&count](const json&) {
        ++count;
        return count < 3;
    }, 2s);
    CHECK(count == 3);
    fake.stop();
}

TEST_CASE("stream cancelled mid-way throws CancelledError") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchEvents", [](const json&) {
        nipa::test::StreamScript script;
        script.endless = true;
        script.lines = {R"({"n":1})"};
        script.delay_ms = 10;
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    CancelToken token;
    std::atomic<int> seen{0};
    std::thread canceller([&] {
        while (seen.load() < 2) {
            std::this_thread::sleep_for(5ms);
        }
        token.cancel();
    });
    CHECK_THROWS_AS(client.stream("WatchEvents", json::object(), [&seen](const json&) {
        ++seen;
        return true;
    }, 10s, &token), CancelledError);
    canceller.join();
    fake.stop();
}

TEST_CASE("cancel before a stream throws without connecting") {
    nipa::test::FakeDaemon fake;
    fake.start();
    Client client(endpoint_for(fake));
    CancelToken token;
    token.cancel();
    CHECK_THROWS_AS(client.stream("WatchChanges", json::object(), [](const json&) { return true; }, 2s, &token),
                    CancelledError);
    fake.stop();
}

TEST_CASE("a malformed NDJSON line fails the stream") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("Update", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"started":{"phase":"x"}})", "{broken"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    int items = 0;
    try {
        client.stream("Update", json::object(), [&items](const json&) {
            ++items;
            return true;
        }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 500);
        CHECK(items == 1);
    }
    fake.stop();
}

TEST_CASE("an error line inside the stream maps to DaemonError") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("Push", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"error":{"code":"FailedPrecondition","message":"nothing to commit"}})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("Push", json::object(), [](const json&) { return true; }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 409);
        CHECK(e.code == "FailedPrecondition");
        CHECK(std::string(e.what()) == "nothing to commit");
    }
    fake.stop();
}

TEST_CASE("an error line without a message uses the code") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("Push", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"error":{"code":"Unavailable"}})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("Push", json::object(), [](const json&) { return true; }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 503);
        CHECK(std::string(e.what()) == "Unavailable");
    }
    fake.stop();
}

TEST_CASE("an error line without a code or message is a generic stream failure") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("Push", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"error":{}})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("Push", json::object(), [](const json&) { return true; }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 500);
        CHECK(std::string(e.what()) == "stream failed");
    }
    fake.stop();
}

TEST_CASE("a stream refused with a status throws before any item") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchRepo", [](const json&) {
        nipa::test::StreamScript script;
        script.status = 404;
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("WatchRepo", json::object(), [](const json&) { return true; }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 404);
    }
    fake.stop();
}

TEST_CASE("a silent stream hits the read timeout") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchChanges", [](const json&) {
        nipa::test::StreamScript script;
        script.endless = true;
        script.lines = {R"({"n":1})"};
        script.delay_ms = 1000;
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("WatchChanges", json::object(), [](const json&) { return false; }, 100ms);
        FAIL("expected timeout");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 504);
    }
    fake.stop();
}

TEST_CASE("a string error field inside a stream item is data, not an error") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchEvents", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"incoming":1,"error":"poll failed"})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    std::string error;
    client.stream("WatchEvents", json::object(), [&error](const json& item) {
        error = item.value("error", std::string());
        return true;
    }, 2s);
    CHECK(error == "poll failed");
    fake.stop();
}

TEST_CASE("an error line with non-string fields still maps to DaemonError") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("Push", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"error":{"code":7,"message":["x"]}})"};
        return script;
    });
    fake.start();
    Client client(endpoint_for(fake));
    try {
        client.stream("Push", json::object(), [](const json&) { return true; }, 2s);
        FAIL("expected DaemonError");
    } catch (const DaemonError& e) {
        CHECK(e.http_status == 500);
        CHECK(std::string(e.what()) == "stream failed");
    }
    fake.stop();
}

TEST_CASE("a throwing consumer leaves no cancel callback behind") {
    nipa::test::FakeDaemon fake;
    fake.on_stream("WatchChanges", [](const json&) {
        nipa::test::StreamScript script;
        script.lines = {R"({"paths":[]})"};
        return script;
    });
    fake.on_unary("Ping", [](const json&) { return nipa::test::Reply{200, json::object()}; });
    fake.start();
    Client client(endpoint_for(fake));
    CancelToken token;
    CHECK_THROWS_AS(client.stream("WatchChanges", json::object(), [](const json&) -> bool {
        throw std::runtime_error("consumer failed");
    }, 2s, &token), std::runtime_error);
    CHECK_NOTHROW(client.call("Ping", json::object(), 2s, &token));
    token.cancel();
    CHECK(token.cancelled());
    fake.stop();
}

TEST_CASE("status codes follow the gRPC mapping") {
    using nipa::daemon::http_status_for_code;
    CHECK(http_status_for_code("InvalidArgument") == 400);
    CHECK(http_status_for_code("OutOfRange") == 400);
    CHECK(http_status_for_code("Unauthenticated") == 401);
    CHECK(http_status_for_code("PermissionDenied") == 403);
    CHECK(http_status_for_code("NotFound") == 404);
    CHECK(http_status_for_code("Aborted") == 409);
    CHECK(http_status_for_code("ResourceExhausted") == 429);
    CHECK(http_status_for_code("Canceled") == 499);
    CHECK(http_status_for_code("Unimplemented") == 501);
    CHECK(http_status_for_code("Unavailable") == 503);
    CHECK(http_status_for_code("DeadlineExceeded") == 504);
    CHECK(http_status_for_code("Internal") == 500);
}
