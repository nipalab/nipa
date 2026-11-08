#include "core/daemon/cancel.h"

namespace nipa::daemon {

CancelledError::CancelledError() : std::runtime_error("cancelled") {}

void CancelToken::cancel() {
    // Callbacks run under the lock so remove_callback() cannot return while one is still using its target.
    std::lock_guard<std::mutex> guard(mu_);
    if (flag_.exchange(true)) {
        return;
    }
    for (auto& entry : callbacks_) {
        entry.second();
    }
}

std::size_t CancelToken::add_callback(std::function<void()> fn) {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (!flag_.load()) {
            std::size_t id = next_id_++;
            callbacks_.emplace(id, std::move(fn));
            return id;
        }
    }
    fn();
    return 0;
}

void CancelToken::remove_callback(std::size_t id) {
    std::lock_guard<std::mutex> guard(mu_);
    callbacks_.erase(id);
}

}  // namespace nipa::daemon
