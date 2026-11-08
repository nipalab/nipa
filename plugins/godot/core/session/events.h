#pragma once

#include <cstdint>
#include <deque>
#include <mutex>
#include <string>
#include <vector>

namespace nipa::session {

enum class EventKind {
    Connected,
    Disconnected,
    StatusChanged,
    BranchesChanged,
    CommitsChanged,
    RemotesChanged,
    OpQueued,
    OpStarted,
    OpProgress,
    OpFinished,
    DiffReady,
    BlobReady,
    Activity,
    ActivitySummary,
    LocksChanged,
};

// One flat record; the fields used depend on `kind` (see session.h).
struct Event {
    EventKind kind = EventKind::StatusChanged;
    std::int64_t op_id = 0;
    std::string name;     // op kind, diff key or activity kind
    std::string message;  // version, reason, phase, result text or error
    std::string actor;
    bool ok = false;
    long long done = 0;
    long long total = 0;
    std::vector<std::string> paths;
};

// Thread-safe FIFO. Workers push; the editor drains it on the main thread.
class EventQueue {
public:
    void push(Event event);
    std::vector<Event> drain();
    std::size_t size() const;

private:
    mutable std::mutex mu_;
    std::deque<Event> queue_;
};

}  // namespace nipa::session
