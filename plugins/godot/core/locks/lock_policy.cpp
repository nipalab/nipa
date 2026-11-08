#include "core/locks/lock_policy.h"

#include <algorithm>
#include <cctype>

#include "core/daemon/messages.h"
#include "core/paths/project_paths.h"

namespace nipa::locks {
namespace {

using daemon::json_string;

std::string trim(const std::string& path) {
    std::string p = paths::normalize(path);
    auto begin = p.find_first_not_of('/');
    if (begin == std::string::npos) {
        return {};
    }
    auto end = p.find_last_not_of('/');
    return p.substr(begin, end - begin + 1);
}

std::string lower(std::string text) {
    std::transform(text.begin(), text.end(), text.begin(),
                   [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    return text;
}

std::string normalize_extension(const std::string& extension) {
    auto begin = extension.find_first_not_of(" \t.");
    if (begin == std::string::npos) {
        return {};
    }
    auto end = extension.find_last_not_of(" \t");
    return lower(extension.substr(begin, end - begin + 1));
}

std::string extension_of(const std::string& path) {
    auto slash = path.find_last_of('/');
    auto dot = path.find_last_of('.');
    if (dot == std::string::npos || (slash != std::string::npos && dot <= slash + 1) || dot == 0) {
        return {};
    }
    return lower(path.substr(dot + 1));
}

bool json_bool(const nlohmann::json& j, const char* key) {
    auto found = j.find(key);
    return found != j.end() && found->is_boolean() && found->get<bool>();
}

}  // namespace

LockStatus parse_lock_status(const nlohmann::json& j) {
    LockStatus s;
    if (!j.is_object()) {
        return s;
    }
    s.user_id = json_string(j, "user_id");
    s.branch = json_string(j, "branch");
    s.default_branch = json_bool(j, "default_branch");
    s.can_release_any = json_bool(j, "can_release_any");
    auto locks = j.find("locks");
    if (locks != j.end() && locks->is_array()) {
        for (const auto& item : *locks) {
            LockDetail l;
            l.id = json_string(item, "id");
            l.path = trim(json_string(item, "path"));
            l.branch = json_string(item, "branch");
            l.global = json_bool(item, "global");
            l.held_by = json_string(item, "held_by");
            l.held_by_name = json_string(item, "held_by_name");
            s.locks.push_back(std::move(l));
        }
    }
    auto files = j.find("files");
    if (files != j.end() && files->is_array()) {
        for (const auto& item : *files) {
            FileState f;
            f.path = trim(json_string(item, "path"));
            f.state = json_string(item, "state");
            f.lock_path = trim(json_string(item, "lock_path"));
            f.held_by = json_string(item, "held_by");
            f.held_by_name = json_string(item, "held_by_name");
            f.tracked = json_bool(item, "tracked");
            s.files.push_back(std::move(f));
        }
    }
    return s;
}

const char* state_name(LockState state) {
    switch (state) {
        case LockState::Mine:
            return "mine";
        case LockState::Theirs:
            return "theirs";
        case LockState::Free:
            break;
    }
    return "free";
}

std::string PushCheck::theirs_summary() const {
    std::string out;
    for (const auto& f : theirs) {
        if (!out.empty()) {
            out += ", ";
        }
        out += f.path + " (" + f.holder() + ")";
    }
    return out;
}

std::vector<std::string> default_extensions() {
    return {"tscn", "tres", "scn", "res", "glb", "gltf", "blend", "fbx", "png", "jpg", "exr", "wav", "ogg"};
}

bool covers(const std::string& lock_path, const std::string& path) {
    std::string l = trim(lock_path);
    std::string p = trim(path);
    if (l.empty()) {
        return false;
    }
    return p == l || p.rfind(l + "/", 0) == 0;
}

LockPolicy::LockPolicy() : LockPolicy(LockStatus{}, default_extensions()) {
    loaded_ = false;
}

LockPolicy::LockPolicy(LockStatus status, const std::vector<std::string>& extensions)
    : loaded_(true), status_(std::move(status)) {
    for (const auto& e : extensions) {
        auto normalized = normalize_extension(e);
        if (!normalized.empty()) {
            extensions_.insert(normalized);
        }
    }
    for (const auto& l : status_.locks) {
        if (in_scope(l)) {
            scoped_.push_back(l);
        }
    }
}

std::string LockPolicy::lock_branch() const {
    return status_.default_branch ? std::string() : status_.branch;
}

bool LockPolicy::in_scope(const LockDetail& lock) const {
    return lock.global ? status_.default_branch : lock.branch == status_.branch;
}

bool LockPolicy::is_mine(const LockDetail& lock) const {
    return !status_.user_id.empty() && lock.held_by == status_.user_id;
}

bool LockPolicy::can_release(const LockDetail& lock) const {
    return is_mine(lock) || status_.can_release_any;
}

LockInfo LockPolicy::lookup(const std::string& clone_path) const {
    LockInfo mine;
    bool have_mine = false;
    for (const auto& p : paths::lock_unit(trim(clone_path))) {
        for (const auto& l : scoped_) {
            if (!covers(l.path, p)) {
                continue;
            }
            if (!is_mine(l)) {
                return LockInfo{LockState::Theirs, l.holder(), l.path};
            }
            if (!have_mine) {
                mine = LockInfo{LockState::Mine, l.holder(), l.path};
                have_mine = true;
            }
        }
    }
    return mine;
}

bool LockPolicy::held_by_me(const std::string& clone_path) const {
    return std::any_of(scoped_.begin(), scoped_.end(),
                       [&](const LockDetail& l) { return is_mine(l) && covers(l.path, clone_path); });
}

bool LockPolicy::lock_relevant(const std::string& clone_path) const {
    std::string asset = paths::asset_of(trim(clone_path));
    if (extensions_.count(extension_of(asset)) > 0) {
        return true;
    }
    return std::any_of(status_.files.begin(), status_.files.end(),
                       [&](const FileState& f) { return f.path == asset; });
}

PushCheck LockPolicy::check_push() const {
    PushCheck check;
    std::set<std::string> needed;
    for (const auto& f : status_.files) {
        if (f.state == "needed" && needed.insert(f.path).second) {
            check.needed.push_back(f.path);
        } else if (f.state == "theirs") {
            check.theirs.push_back(f);
        }
    }
    return check;
}

std::set<std::string> LockPolicy::theirs_paths() const {
    std::set<std::string> out;
    for (const auto& l : scoped_) {
        if (!is_mine(l)) {
            out.insert(l.path);
        }
    }
    return out;
}

std::vector<LockDetail> LockPolicy::releasable(const std::vector<std::string>& clone_paths, bool any_holder) const {
    std::set<std::string> assets;
    for (const auto& p : clone_paths) {
        assets.insert(paths::asset_of(trim(p)));
    }
    std::vector<LockDetail> out;
    for (const auto& l : scoped_) {
        bool allowed = any_holder ? can_release(l) : is_mine(l);
        if (allowed && assets.count(paths::asset_of(l.path)) > 0) {
            out.push_back(l);
        }
    }
    return out;
}

}  // namespace nipa::locks
