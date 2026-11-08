#pragma once

#include <cstddef>
#include <atomic>
#include <functional>
#include <map>
#include <mutex>
#include <stdexcept>

namespace nipa::daemon {

class CancelToken {
public:
    bool cancelled() const noexcept { return flag_.load(); }

    void cancel();

    // Runs `fn` on cancel. When the token is already cancelled, runs it now
    // and returns 0.
    std::size_t add_callback(std::function<void()> fn);
    void remove_callback(std::size_t id);

private:
    std::atomic<bool> flag_{false};
    std::mutex mu_;
    std::size_t next_id_ = 1;
    std::map<std::size_t, std::function<void()>> callbacks_;
};

struct CancelledError : std::runtime_error {
    CancelledError();
};

}  // namespace nipa::daemon
