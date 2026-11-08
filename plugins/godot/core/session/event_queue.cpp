#include "core/session/events.h"

namespace nipa::session {

void EventQueue::push(Event event) {
    std::lock_guard<std::mutex> guard(mu_);
    queue_.push_back(std::move(event));
}

std::vector<Event> EventQueue::drain() {
    std::lock_guard<std::mutex> guard(mu_);
    std::vector<Event> out(queue_.begin(), queue_.end());
    queue_.clear();
    return out;
}

std::size_t EventQueue::size() const {
    std::lock_guard<std::mutex> guard(mu_);
    return queue_.size();
}

}  // namespace nipa::session
