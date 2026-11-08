#include "core/history/history.h"

namespace nipa::history {

std::vector<Tag> parse_tags(const nlohmann::json& j) {
    std::vector<Tag> out;
    auto list = j.find("tags");
    if (!j.is_object() || list == j.end() || !list->is_array()) {
        return out;
    }
    for (const auto& item : *list) {
        out.push_back({daemon::json_string(item, "name"), daemon::json_string(item, "commit_id"),
                       daemon::json_string(item, "message")});
    }
    return out;
}

nlohmann::json page_request(const std::string& root, const std::string& path, const std::string& start, int limit) {
    nlohmann::json request = {{"limit", limit}};
    if (!path.empty()) {
        request["path"] = path;
    }
    if (!start.empty()) {
        request["start_commit_id"] = start;
    }
    return {{"root", root}, {"request", request}};
}

Page parse_page(const nlohmann::json& reply, int limit) {
    Page page;
    page.commits = daemon::parse_commits(daemon::proxy_payload(reply));
    if (limit > 0 && static_cast<int>(page.commits.size()) >= limit) {
        page.next = page.commits.back().parent_1_id;
    }
    return page;
}

nlohmann::json to_json(const Page& page) {
    nlohmann::json commits = nlohmann::json::array();
    for (const auto& c : page.commits) {
        commits.push_back({{"id", c.commit_id},
                           {"hash", c.commit_hash},
                           {"parent", c.parent_1_id},
                           {"author", c.author_name},
                           {"email", c.author_email},
                           {"message", c.message},
                           {"created_at", c.created_at}});
    }
    return {{"commits", commits}, {"next", page.next}};
}

std::string commit_link(const std::string& clone_url, const std::string& commit_id) {
    std::string rest = clone_url;
    auto scheme = rest.find("://");
    if (scheme != std::string::npos) {
        rest = rest.substr(scheme + 3);
    }
    while (!rest.empty() && rest.back() == '/') {
        rest.pop_back();
    }
    int slashes = 0;
    for (char c : rest) {
        slashes += c == '/' ? 1 : 0;
    }
    if (slashes < 2 || commit_id.empty() || rest.find("//") != std::string::npos) {
        return {};
    }
    return "nipa://" + rest + "/commits/" + commit_id;
}

}  // namespace nipa::history
