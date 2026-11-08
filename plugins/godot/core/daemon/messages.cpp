#include "core/daemon/messages.h"

#include <string>

namespace nipa::daemon {
namespace {

const nlohmann::json& field(const nlohmann::json& j, const char* key) {
    static const nlohmann::json kNull;
    if (!j.is_object()) {
        return kNull;
    }
    auto found = j.find(key);
    return found == j.end() ? kNull : *found;
}

}  // namespace

long long json_int(const nlohmann::json& j, const char* key, long long fallback) {
    const auto& value = field(j, key);
    if (value.is_number_integer()) {
        return value.get<long long>();
    }
    if (value.is_string()) {
        try {
            return std::stoll(value.get<std::string>());
        } catch (const std::exception&) {
            return fallback;
        }
    }
    return fallback;
}

std::string json_string(const nlohmann::json& j, const char* key) {
    const auto& value = field(j, key);
    return value.is_string() ? value.get<std::string>() : std::string();
}

std::vector<std::string> json_strings(const nlohmann::json& j, const char* key) {
    std::vector<std::string> out;
    const auto& value = field(j, key);
    if (!value.is_array()) {
        return out;
    }
    for (const auto& item : value) {
        if (item.is_string()) {
            out.push_back(item.get<std::string>());
        }
    }
    return out;
}

status::Snapshot parse_status(const nlohmann::json& j) {
    status::Snapshot s;
    s.staged = json_strings(j, "staged");
    s.deleted = json_strings(j, "deleted");
    s.modified = json_strings(j, "modified");
    s.untracked = json_strings(j, "untracked");
    s.missing = json_strings(j, "missing");
    s.conflicts = json_strings(j, "conflicts");
    s.resolved = json_strings(j, "resolved");
    s.added = json_strings(j, "added");
    s.branch = json_string(j, "branch");
    const auto& head = field(j, "head");
    s.head_kind = json_string(head, "kind");
    s.head_name = json_string(head, "name");
    s.pending = json_string(j, "pending");
    s.pending_remaining = static_cast<int>(json_int(j, "pending_remaining"));
    return s;
}

std::string pong_version(const nlohmann::json& j) {
    return json_string(j, "version");
}

std::vector<RepoInfo> parse_repos(const nlohmann::json& j) {
    std::vector<RepoInfo> out;
    const auto& repos = field(j, "repos");
    if (!repos.is_array()) {
        return out;
    }
    for (const auto& repo : repos) {
        out.push_back({json_string(repo, "root"), json_string(repo, "url"), json_string(repo, "branch")});
    }
    return out;
}

std::vector<Branch> parse_branches(const nlohmann::json& j) {
    std::vector<Branch> out;
    const auto& list = field(j, "branches");
    if (!list.is_array()) {
        return out;
    }
    for (const auto& item : list) {
        Branch b;
        b.id = json_string(item, "id");
        b.name = json_string(item, "name");
        b.commit_id = json_string(item, "commit_id");
        b.is_protected = field(item, "is_protected").is_boolean() && field(item, "is_protected").get<bool>();
        b.is_default = field(item, "is_default").is_boolean() && field(item, "is_default").get<bool>();
        out.push_back(std::move(b));
    }
    return out;
}

std::vector<Commit> parse_commits(const nlohmann::json& j) {
    std::vector<Commit> out;
    const auto& list = field(j, "commits");
    if (!list.is_array()) {
        return out;
    }
    for (const auto& item : list) {
        Commit c;
        c.commit_id = json_string(item, "commit_id");
        c.commit_hash = json_string(item, "commit_hash");
        c.parent_1_id = json_string(item, "parent_1_id");
        c.author_name = json_string(item, "author_name");
        c.author_email = json_string(item, "author_email");
        c.message = json_string(item, "message");
        c.created_at = json_string(item, "created_at");
        out.push_back(std::move(c));
    }
    return out;
}

OpEvent parse_op_event(const nlohmann::json& j) {
    OpEvent e;
    if (j.contains("queued")) {
        e.kind = OpEvent::Kind::Queued;
        e.ahead = json_int(j["queued"], "ahead");
    } else if (j.contains("started")) {
        e.kind = OpEvent::Kind::Started;
        e.phase = json_string(j["started"], "phase");
    } else if (j.contains("progress")) {
        const auto& p = j["progress"];
        e.kind = OpEvent::Kind::Progress;
        e.phase = json_string(p, "phase");
        e.done = json_int(p, "objects_done");
        e.total = json_int(p, "objects_total");
    } else if (j.contains("failure")) {
        e.kind = OpEvent::Kind::Failure;
        e.failure_code = static_cast<int>(json_int(j["failure"], "code"));
        e.message = json_string(j["failure"], "message");
    } else {
        e.kind = OpEvent::Kind::Result;
        const auto& r = field(j, "result");
        e.result = r;
        if (r.contains("push")) {
            e.result_commit_id = json_string(r["push"], "commit_id");
        } else if (r.contains("sync")) {
            e.result_commit_id = json_string(r["sync"], "commit_id");
        }
    }
    return e;
}

diff::File parse_diff_file(const nlohmann::json& j) {
    diff::File f;
    f.new_path = json_string(j, "path");
    f.status = json_string(j, "status");
    f.kind = json_string(j, "kind");
    const auto& old_side = field(j, "old");
    const auto& new_side = field(j, "new");
    f.old_path = json_string(old_side, "path");
    f.new_path = json_string(new_side, "path").empty() ? f.new_path : json_string(new_side, "path");
    f.old_size = json_int(old_side, "size_bytes");
    f.new_size = json_int(new_side, "size_bytes");
    f.additions = json_int(j, "additions");
    f.deletions = json_int(j, "deletions");
    f.omitted = field(j, "hunks_omitted").is_boolean() && field(j, "hunks_omitted").get<bool>();
    f.omitted = f.omitted || (field(j, "content_unavailable").is_boolean() && field(j, "content_unavailable").get<bool>());
    const auto& hunks = field(j, "hunks");
    if (hunks.is_array()) {
        for (const auto& h : hunks) {
            diff::Hunk hunk;
            hunk.old_start = json_int(h, "old_start");
            hunk.old_lines = json_int(h, "old_lines");
            hunk.new_start = json_int(h, "new_start");
            hunk.new_lines = json_int(h, "new_lines");
            const auto& lines = field(h, "lines");
            if (lines.is_array()) {
                for (const auto& l : lines) {
                    diff::Line line;
                    std::string kind = json_string(l, "kind");
                    line.status = kind == "added" ? '+' : kind == "removed" ? '-' : ' ';
                    line.old_no = json_int(l, "old_line", -1);
                    line.new_no = json_int(l, "new_line", -1);
                    line.content = json_string(l, "text");
                    hunk.lines.push_back(std::move(line));
                }
            }
            f.hunks.push_back(std::move(hunk));
        }
    }
    return f;
}

std::string base64_decode(const std::string& encoded) {
    static const std::string kAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    unsigned int buffer = 0;
    int bits = 0;
    for (char c : encoded) {
        if (c == '=') {
            break;
        }
        auto found = kAlphabet.find(c);
        if (found == std::string::npos) {
            break;
        }
        buffer = (buffer << 6) | static_cast<unsigned int>(found);
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            out.push_back(static_cast<char>((buffer >> bits) & 0xFF));
        }
    }
    return out;
}

std::string base64_encode(const std::string& data) {
    static const char* kAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    out.reserve((data.size() + 2) / 3 * 4);
    std::size_t i = 0;
    for (; i + 2 < data.size(); i += 3) {
        unsigned int n = (static_cast<unsigned char>(data[i]) << 16) | (static_cast<unsigned char>(data[i + 1]) << 8) |
                         static_cast<unsigned char>(data[i + 2]);
        out += kAlphabet[(n >> 18) & 63];
        out += kAlphabet[(n >> 12) & 63];
        out += kAlphabet[(n >> 6) & 63];
        out += kAlphabet[n & 63];
    }
    if (i < data.size()) {
        unsigned int n = static_cast<unsigned char>(data[i]) << 16;
        bool two = i + 1 < data.size();
        if (two) {
            n |= static_cast<unsigned char>(data[i + 1]) << 8;
        }
        out += kAlphabet[(n >> 18) & 63];
        out += kAlphabet[(n >> 12) & 63];
        out += two ? kAlphabet[(n >> 6) & 63] : '=';
        out += '=';
    }
    return out;
}

std::string result_summary(const nlohmann::json& result) {
    for (const char* kind : {"merge", "revert"}) {
        const auto& r = field(result, kind);
        if (!r.is_object()) {
            continue;
        }
        auto conflicts = json_strings(r, "conflicts");
        auto is = [&](const char* key) { return field(r, key).is_boolean() && field(r, key).get<bool>(); };
        if (!conflicts.empty()) {
            return std::to_string(conflicts.size()) + " conflict(s) to resolve";
        }
        if (is("aborted")) {
            return std::string(kind) + " aborted";
        }
        if (is("up_to_date")) {
            return "already up to date";
        }
        if (is("fast_forwarded")) {
            return "fast-forwarded";
        }
        if (is("no_change")) {
            return "nothing to revert";
        }
        if (is("skipped")) {
            return "skipped";
        }
        return std::string(kind == std::string("merge") ? "merged" : "reverted");
    }
    return {};
}

nlohmann::json proxy_payload(const nlohmann::json& j) {
    const auto& inner = field(j, "response");
    return inner.is_object() ? inner : nlohmann::json::object();
}

std::string stream_error_message(const nlohmann::json& j) {
    const auto& error = field(j, "error");
    return json_string(error, "message");
}

}  // namespace nipa::daemon
