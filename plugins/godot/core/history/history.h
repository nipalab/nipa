#pragma once

#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "core/daemon/messages.h"

namespace nipa::history {

struct Tag {
    std::string name;
    std::string commit_id;
    std::string message;
};

std::vector<Tag> parse_tags(const nlohmann::json& j);

// One page of a (path-filtered) commit log. `next` is the commit to start the
// following page from; empty on the last page.
struct Page {
    std::vector<daemon::Commit> commits;
    std::string next;
};

// ProxyCommitLog request body for a page; empty path = whole project.
nlohmann::json page_request(const std::string& root, const std::string& path, const std::string& start, int limit);
Page parse_page(const nlohmann::json& reply, int limit);
nlohmann::json to_json(const Page& page);

// nipa://<server>/<org>/<project>/commits/<id> for a clone URL such as
// https://host/org/project or host/org/project; empty when the URL is not one.
std::string commit_link(const std::string& clone_url, const std::string& commit_id);

}  // namespace nipa::history
