#include "nipa_vcs_interface.h"

#include <filesystem>
#include <fstream>
#include <sstream>

#include <godot_cpp/classes/engine.hpp>
#include <godot_cpp/variant/dictionary.hpp>

#include <nlohmann/json.hpp>

#include "core/diff/diff.h"
#include "core/paths/project_paths.h"
#include "core/status/status.h"
#include "nipa_util.h"

using namespace godot;

namespace nipa::glue {
namespace {

constexpr std::chrono::milliseconds kDiffBudget{1000};

Dictionary diff_line_dict(EditorVCSInterface* vcs, const diff::Line& line) {
    return vcs->create_diff_line(static_cast<int32_t>(line.new_no), static_cast<int32_t>(line.old_no),
                                 to_godot(line.content), to_godot(std::string(1, line.status)));
}

TypedArray<Dictionary> hunks_array(EditorVCSInterface* vcs, const std::vector<diff::Hunk>& hunks) {
    TypedArray<Dictionary> out;
    for (const auto& hunk : hunks) {
        Dictionary hunk_dict = vcs->create_diff_hunk(static_cast<int32_t>(hunk.old_start),
                                                     static_cast<int32_t>(hunk.new_start),
                                                     static_cast<int32_t>(hunk.old_lines),
                                                     static_cast<int32_t>(hunk.new_lines));
        TypedArray<Dictionary> lines;
        for (const auto& line : hunk.lines) {
            lines.push_back(diff_line_dict(vcs, line));
        }
        out.push_back(vcs->add_line_diffs_into_diff_hunk(hunk_dict, lines));
    }
    return out;
}

Dictionary diff_file_dict(EditorVCSInterface* vcs, const diff::File& file) {
    Dictionary file_dict = vcs->create_diff_file(to_godot(file.new_path), to_godot(file.old_path));
    std::vector<diff::Hunk> hunks = file.hunks;
    if (file.placeholder()) {
        hunks = {diff::placeholder_hunk(file)};
    }
    return vcs->add_diff_hunks_into_diff_file(file_dict, hunks_array(vcs, hunks));
}

bool digits_at(const std::string& text, std::size_t at, std::size_t count, int64_t& value) {
    if (text.size() < at + count) {
        return false;
    }
    value = 0;
    for (std::size_t i = at; i < at + count; ++i) {
        if (text[i] < '0' || text[i] > '9') {
            return false;
        }
        value = value * 10 + (text[i] - '0');
    }
    return true;
}

// Seconds since the epoch for an RFC 3339 timestamp such as 2026-01-02T03:04:05Z; 0 when malformed.
int64_t unix_time_of(const std::string& rfc3339) {
    int64_t year = 0;
    int64_t month = 0;
    int64_t day = 0;
    int64_t hour = 0;
    int64_t minute = 0;
    int64_t second = 0;
    if (!digits_at(rfc3339, 0, 4, year) || !digits_at(rfc3339, 5, 2, month) || !digits_at(rfc3339, 8, 2, day) ||
        !digits_at(rfc3339, 11, 2, hour) || !digits_at(rfc3339, 14, 2, minute) || !digits_at(rfc3339, 17, 2, second)) {
        return 0;
    }
    year -= month <= 2 ? 1 : 0;
    int64_t era = (year >= 0 ? year : year - 399) / 400;
    int64_t year_of_era = year - era * 400;
    int64_t day_of_year = (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1;
    int64_t day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    int64_t days = era * 146097 + day_of_era - 719468;
    return days * 86400 + hour * 3600 + minute * 60 + second;
}

// Host part of the clone URL in `.nipa/config`, e.g. "nipa.example.com".
std::string server_from_clone(const std::string& clone_root) {
    std::ifstream in(clone_root + "/.nipa/config");
    if (!in) {
        return {};
    }
    std::stringstream buffer;
    buffer << in.rdbuf();
    auto config = nlohmann::json::parse(buffer.str(), nullptr, false);
    if (!config.is_object() || !config.contains("url") || !config["url"].is_string()) {
        return {};
    }
    std::string url = config["url"].get<std::string>();
    auto scheme = url.find("://");
    if (scheme != std::string::npos) {
        url = url.substr(scheme + 3);
    }
    auto end = url.find('/');
    return end == std::string::npos ? url : url.substr(0, end);
}

}  // namespace

NipaSession* NipaVCSInterface::session_node() const {
    return Object::cast_to<NipaSession>(Engine::get_singleton()->get_singleton("Nipa"));
}

bool NipaVCSInterface::_initialize(const String& p_project_path) {
    NipaSession* node = session_node();
    return node != nullptr && node->open_project(p_project_path, String(), 60);
}

void NipaVCSInterface::_set_credentials(const String& p_username, const String& p_password,
                                        const String& p_ssh_public_key_path, const String& p_ssh_private_key_path,
                                        const String& p_ssh_passphrase) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return;
    }
    std::string server = server_from_clone(from_godot(node->get_clone_root()));
    node->request_sign_in(to_godot(server), p_username, p_password, p_ssh_private_key_path, p_ssh_passphrase);
}

TypedArray<Dictionary> NipaVCSInterface::_get_modified_files_data() {
    TypedArray<Dictionary> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    node->status_blocking(2000);
    const auto cache = node->session()->status_cache();
    for (const auto& entry : cache.entries()) {
        auto projected = node->project_paths()->to_project(entry.path);
        if (!projected) {
            continue;
        }
        out.push_back(create_status_file(to_godot(*projected),
                                         static_cast<EditorVCSInterface::ChangeType>(entry.change),
                                         static_cast<EditorVCSInterface::TreeArea>(entry.area)));
    }
    return out;
}

void NipaVCSInterface::_stage_file(const String& p_file_path) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return;
    }
    PackedStringArray paths;
    paths.push_back(node->to_clone_path(p_file_path));
    node->request_stage(node->expand_sidecars(paths), PackedStringArray());
}

void NipaVCSInterface::_unstage_file(const String& p_file_path) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return;
    }
    PackedStringArray paths;
    paths.push_back(node->to_clone_path(p_file_path));
    node->request_stage(PackedStringArray(), node->expand_sidecars(paths));
}

void NipaVCSInterface::_discard_file(const String& p_file_path) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return;
    }
    PackedStringArray paths;
    paths.push_back(node->to_clone_path(p_file_path));
    node->request_restore(node->expand_sidecars(paths));
}

void NipaVCSInterface::_commit(const String& p_msg) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        popup_error("Nipa is not connected to a clone of this project.");
        return;
    }
    node->request_push(p_msg);
}

TypedArray<Dictionary> NipaVCSInterface::_get_diff(const String& p_identifier, int32_t p_area) {
    TypedArray<Dictionary> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    nlohmann::json body = {{"root", from_godot(node->get_clone_root())}};
    std::string key;
    if (p_area == EditorVCSInterface::TREE_AREA_COMMIT) {
        key = "commit:" + from_godot(p_identifier);
        body["revisions"] = nlohmann::json::array({from_godot(p_identifier)});
        body["show"] = true;
    } else {
        key = "work:" + std::to_string(p_area) + ":" + from_godot(p_identifier);
        body["paths"] = nlohmann::json::array({from_godot(node->to_clone_path(p_identifier))});
        body["staged"] = p_area == EditorVCSInterface::TREE_AREA_STAGED;
    }
    auto session = node->session();
    session->submit_diff(key, body);
    session->diff_blocking(key, kDiffBudget);
    auto files = session->diff_result(key);
    if (!files) {
        return out;
    }
    for (const auto& file : *files) {
        out.push_back(diff_file_dict(this, file));
    }
    return out;
}

bool NipaVCSInterface::_shut_down() {
    NipaSession* node = session_node();
    if (node != nullptr) {
        node->close_project();
    }
    return true;
}

String NipaVCSInterface::_get_vcs_name() {
    return "Nipa";
}

TypedArray<Dictionary> NipaVCSInterface::_get_previous_commits(int32_t p_max_commits) {
    TypedArray<Dictionary> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    for (const auto& commit : node->session()->commits()) {
        if (static_cast<int32_t>(out.size()) >= p_max_commits) {
            break;
        }
        out.push_back(create_commit(to_godot(commit.message), to_godot(commit.author_name),
                                    to_godot(commit.commit_id), unix_time_of(commit.created_at), 0));
    }
    return out;
}

TypedArray<String> NipaVCSInterface::_get_branch_list() {
    TypedArray<String> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    for (int i = 0; i < node->get_branch_names().size(); ++i) {
        out.push_back(node->get_branch_names()[i]);
    }
    return out;
}

TypedArray<String> NipaVCSInterface::_get_remotes() {
    TypedArray<String> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    PackedStringArray remotes = node->get_remote_names();
    for (int i = 0; i < remotes.size(); ++i) {
        out.push_back(remotes[i]);
    }
    return out;
}

void NipaVCSInterface::_create_branch(const String& p_branch_name) {
    NipaSession* node = session_node();
    if (node != nullptr && node->is_open()) {
        node->request_branch_create(p_branch_name, node->get_branch_name());
    }
}

void NipaVCSInterface::_remove_branch(const String& p_branch_name) {
    NipaSession* node = session_node();
    if (node != nullptr && node->is_open()) {
        node->request_branch_delete(p_branch_name);
    }
}

void NipaVCSInterface::_create_remote(const String& p_remote_name, const String& p_remote_url) {
    popup_error("Remotes are not supported by Nipa: the clone's server is the only remote.");
}

void NipaVCSInterface::_remove_remote(const String& p_remote_name) {
    popup_error("Remotes are not supported by Nipa: the clone's server is the only remote.");
}

String NipaVCSInterface::_get_current_branch_name() {
    NipaSession* node = session_node();
    return node != nullptr && node->is_open() ? node->get_branch_name() : String();
}

bool NipaVCSInterface::_checkout_branch(const String& p_branch_name) {
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return false;
    }
    node->request_switch(p_branch_name);
    return true;
}

void NipaVCSInterface::_pull(const String& p_remote) {
    NipaSession* node = session_node();
    if (node != nullptr && node->is_open()) {
        node->request_update();
    }
}

void NipaVCSInterface::_push(const String& p_remote, bool p_force) {
    NipaSession* node = session_node();
    if (node != nullptr) {
        node->emit_signal("notice", String("Nipa commits go to the server on Commit."));
    }
}

void NipaVCSInterface::_fetch(const String& p_remote) {
    NipaSession* node = session_node();
    if (node != nullptr && node->is_open()) {
        node->request_refresh();
    }
}

TypedArray<Dictionary> NipaVCSInterface::_get_line_diff(const String& p_file_path, const String& p_text) {
    TypedArray<Dictionary> out;
    NipaSession* node = session_node();
    if (node == nullptr || !node->is_open()) {
        return out;
    }
    std::string key = "blob:" + from_godot(p_file_path);
    auto session = node->session();
    std::string clone_path = from_godot(node->to_clone_path(p_file_path));
    if (!session->blob_result(key)) {
        session->submit_read_blob(key, clone_path);
        session->blob_blocking(key, kDiffBudget);
    }
    auto old_text = session->blob_result(key);
    if (!old_text) {
        return out;
    }
    return hunks_array(this, diff::line_diff(*old_text, from_godot(p_text)));
}

}  // namespace nipa::glue
