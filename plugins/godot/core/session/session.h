#pragma once

#include <chrono>
#include <cstdint>
#include <condition_variable>
#include <deque>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <vector>

#include "core/daemon/cancel.h"
#include "core/daemon/client.h"
#include "core/daemon/connector.h"
#include "core/daemon/messages.h"
#include "core/diff/diff.h"
#include "core/session/events.h"
#include "core/status/status.h"

namespace nipa::session {

struct SessionOptions {
    daemon::ConnectOptions connect;
    std::string root;  // clone root, passed to WatchRepo
    std::chrono::milliseconds unary_timeout{30000};
    std::chrono::milliseconds stream_timeout{std::chrono::hours(24)};
    std::chrono::milliseconds reconnect_min{1000};
    std::chrono::milliseconds reconnect_max{30000};
    std::chrono::seconds activity_interval{60};
    bool watch_activity = true;
};

// Owns the daemon connection, one worker thread for requests and operations
// (FIFO), and two stream threads (WatchChanges and WatchEvents). Public methods
// never wait on the network, except status_blocking() and diff_blocking(),
// which return after their timeout.
//
// Event fields: OpQueued/OpStarted/OpFinished carry op_id and name (kind);
// OpStarted and OpProgress put the phase in message; OpProgress sets done/total;
// OpFinished sets ok, message and paths (empty = refresh everything).
// StatusChanged lists paths; Connected carries the version in message.
class Session {
public:
    explicit Session(SessionOptions options);
    ~Session();

    Session(const Session&) = delete;
    Session& operator=(const Session&) = delete;

    void start();
    void stop();

    EventQueue& events() { return events_; }

    bool connected() const;
    std::string version() const;
    status::StatusCache status_cache() const;
    std::vector<daemon::Branch> branches() const;
    std::vector<daemon::Commit> commits() const;
    std::vector<std::string> remotes() const;

    // Waits up to `timeout` for the first status. False on timeout or stop.
    bool status_blocking(std::chrono::milliseconds timeout);

    // The returned ids match the op_id of the events they produce; 0 when the session is stopped.
    std::int64_t submit_status();
    std::int64_t submit_branches();
    std::int64_t submit_commits();
    std::int64_t submit_stage(std::vector<std::string> add, std::vector<std::string> unstage);
    std::int64_t submit_restore(std::vector<std::string> paths);
    std::int64_t submit_update();
    std::int64_t submit_switch(std::string branch, std::string tag = {});
    std::int64_t submit_push(std::string message);
    std::int64_t submit_branch_create(std::string name, std::string from_branch);
    std::int64_t submit_branch_delete(std::string name);

    // Signs in to `server` unless a session exists: an SSH key when given, else the password.
    std::int64_t submit_sign_in(std::string server, std::string username, std::string password,
                                std::string key_path, std::string passphrase);

    // `body` is a DiffFiles request; the result is kept under `key` (DiffReady).
    std::int64_t submit_diff(const std::string& key, nlohmann::json body);
    std::optional<std::vector<diff::File>> diff_result(const std::string& key) const;
    bool diff_blocking(const std::string& key, std::chrono::milliseconds timeout);

    // Reads the snapshot (last synced) content of `path`, kept under `key` (BlobReady).
    std::int64_t submit_read_blob(const std::string& key, const std::string& path);
    std::optional<std::string> blob_result(const std::string& key) const;
    bool blob_known(const std::string& key) const;
    bool blob_blocking(const std::string& key, std::chrono::milliseconds timeout);

private:
    struct Link {
        std::shared_ptr<daemon::Client> client;
        std::uint64_t generation = 0;
    };
    struct Outcome {
        bool ok = true;
        std::string message;
        std::vector<std::string> paths;
    };
    struct Task {
        std::int64_t id;
        std::string name;
        bool quiet;
        std::function<Outcome(std::int64_t)> run;
    };

    std::int64_t enqueue(std::string name, bool quiet, std::function<Outcome(std::int64_t)> run);
    void worker_loop();
    void watch_loop(const std::function<void(const daemon::Client&)>& body);
    void watch_changes_body(const daemon::Client& client);
    void watch_activity_body(const daemon::Client& client);
    void wait_backoff(std::chrono::milliseconds& backoff);
    bool stopping() const;

    Link ensure_connected();
    void run_on_link(const std::function<void(const daemon::Client&)>& fn);
    void drop_link(std::uint64_t generation, const std::string& reason);
    void apply_status(const nlohmann::json& reply);
    void refresh_after_op(const std::string& name);
    Outcome run_stream_op(std::int64_t id, const std::string& name, const std::string& method,
                          const nlohmann::json& body);

    SessionOptions options_;
    daemon::CancelToken cancel_;
    EventQueue events_;

    mutable std::mutex mu_;
    std::condition_variable task_cv_;
    std::condition_variable wait_cv_;
    std::condition_variable cache_cv_;
    std::deque<Task> tasks_;
    std::int64_t next_id_ = 1;
    bool stopping_ = false;
    bool stopped_ = false;
    bool status_pending_ = false;
    std::uint64_t generation_ = 0;

    Link link_;
    std::string version_;
    status::StatusCache status_;
    std::vector<daemon::Branch> branches_;
    std::vector<daemon::Commit> commits_;
    std::vector<std::string> remotes_;
    struct BlobEntry {
        bool ok = false;
        std::string content;
    };
    std::map<std::string, std::vector<diff::File>> diffs_;
    std::map<std::string, std::uint64_t> diff_generation_;
    std::map<std::string, BlobEntry> blobs_;

    std::mutex connect_mu_;
    std::thread worker_;
    std::thread changes_thread_;
    std::thread activity_thread_;
};

}  // namespace nipa::session
