#pragma once

#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "core/diff/diff.h"
#include "core/status/status.h"

namespace nipa::daemon {

struct RepoInfo {
    std::string root;
    std::string url;
    std::string branch;
};

struct Branch {
    std::string id;
    std::string name;
    std::string commit_id;
    bool is_protected = false;
    bool is_default = false;
};

struct Commit {
    std::string commit_id;
    std::string commit_hash;
    std::string parent_1_id;
    std::string author_name;
    std::string author_email;
    std::string message;
    std::string created_at;
};

struct OpEvent {
    enum class Kind { Queued, Started, Progress, Result, Failure };

    Kind kind = Kind::Queued;
    long long ahead = 0;
    std::string phase;
    long long done = 0;
    long long total = 0;
    int failure_code = 0;
    std::string message;
    std::string result_commit_id;
    nlohmann::json result;  // the raw OpResult of a Result event
};

// Protojson writes int64 as a JSON string; both forms are accepted.
long long json_int(const nlohmann::json& j, const char* key, long long fallback = 0);
std::string json_string(const nlohmann::json& j, const char* key);
std::vector<std::string> json_strings(const nlohmann::json& j, const char* key);

status::Snapshot parse_status(const nlohmann::json& j);
std::string pong_version(const nlohmann::json& j);
std::vector<RepoInfo> parse_repos(const nlohmann::json& j);
std::vector<Branch> parse_branches(const nlohmann::json& j);
std::vector<Commit> parse_commits(const nlohmann::json& j);
OpEvent parse_op_event(const nlohmann::json& j);
diff::File parse_diff_file(const nlohmann::json& j);

// Standard base64 (protojson `bytes`); invalid characters end the input.
std::string base64_decode(const std::string& encoded);
std::string base64_encode(const std::string& data);

// Short text for a merge or revert OpResult, e.g. "2 conflict(s) to resolve".
std::string result_summary(const nlohmann::json& result);

// Strips the reply wrapper of the Proxy* responses (`{"response": {...}}`).
nlohmann::json proxy_payload(const nlohmann::json& j);

// Maps a daemon stream error line such as `{"error": {...}}` into the daemon error text.
std::string stream_error_message(const nlohmann::json& j);

}  // namespace nipa::daemon
