#include "core/session/session.h"
#include "core/shelves/shelves.h"

namespace nipa::session {

using json = nlohmann::json;

std::int64_t Session::submit_sync_plan(std::string branch, std::string tag) {
    return enqueue("sync-plan", false, [this, branch = std::move(branch), tag = std::move(tag)](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("SyncPlan", {{"root", options_.root}, {"branch", branch}, {"tag", tag}},
                                options_.unary_timeout, &cancel_);
        });
        auto overwritten = shelves::parse_overwritten(reply);
        Outcome out{true, std::to_string(overwritten.size()) + " local change(s) would be replaced"};
        out.data = {{"overwritten", overwritten}};
        return out;
    });
}

std::int64_t Session::submit_shelve(std::string name, std::vector<std::string> paths) {
    return enqueue("shelve", false, [this, name = std::move(name), paths = std::move(paths)](std::int64_t id) {
        json body = {{"root", options_.root}, {"name", name}, {"paths", paths}, {"include_untracked", !paths.empty()}};
        return run_stream_op(id, "shelve", "ShelveCreate", body);
    });
}

std::int64_t Session::submit_shelve_apply(std::string shelf_id, bool keep) {
    return enqueue("shelve-apply", false, [this, shelf_id = std::move(shelf_id), keep](std::int64_t id) {
        return run_stream_op(id, "shelve-apply", "ShelveApply",
                             {{"root", options_.root}, {"id", shelf_id}, {"keep", keep}});
    });
}

std::int64_t Session::submit_shelve_abort() {
    return enqueue("shelve-abort", false, [this](std::int64_t id) {
        return run_stream_op(id, "shelve-abort", "ShelveApply", {{"root", options_.root}, {"abort", true}});
    });
}

std::int64_t Session::submit_shelves() {
    return enqueue("shelves", false, [this](std::int64_t) {
        json reply;
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("ShelveList", {{"root", options_.root}}, options_.unary_timeout, &cancel_);
        });
        auto list = shelves::parse_list(reply);
        Outcome out{true, std::to_string(list.shelves.size()) + " shelf(s)"};
        out.data = shelves::to_json(list);
        return out;
    });
}

std::int64_t Session::submit_shelve_drop(std::string shelf_id) {
    return enqueue("shelve-drop", false, [this, shelf_id = std::move(shelf_id)](std::int64_t) {
        run_on_link([&](const daemon::Client& client) {
            client.call("ShelveDrop", {{"root", options_.root}, {"id", shelf_id}}, options_.unary_timeout, &cancel_);
        });
        return Outcome{true, "shelf deleted"};
    });
}

}  // namespace nipa::session
