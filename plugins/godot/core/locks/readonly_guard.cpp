#include "core/locks/readonly_guard.h"

#include <filesystem>
#include <fstream>
#include <sstream>

#include <nlohmann/json.hpp>

#include "core/paths/project_paths.h"

namespace nipa::locks {
namespace {

namespace fs = std::filesystem;

constexpr auto kWriteBits = fs::perms::owner_write | fs::perms::group_write | fs::perms::others_write;

bool is_regular(const fs::path& path) {
    std::error_code error;
    return fs::is_regular_file(path, error);
}

bool writable(const fs::path& path, fs::perms& current) {
    std::error_code error;
    auto status = fs::status(path, error);
    if (error) {
        return false;
    }
    current = status.permissions();
    return (current & fs::perms::owner_write) != fs::perms::none;
}

bool set_perms(const fs::path& path, fs::perms perms) {
    std::error_code error;
    fs::permissions(path, perms, fs::perm_options::replace, error);
    return !error;
}

bool inside_metadata(const std::string& relative) {
    return relative == ".nipa" || relative.rfind(".nipa/", 0) == 0;
}

}  // namespace

ReadOnlyGuard::ReadOnlyGuard(std::string clone_root, std::string state_path)
    : clone_root_(std::move(clone_root)), state_path_(std::move(state_path)) {}

void ReadOnlyGuard::load() {
    if (state_path_.empty()) {
        return;
    }
    std::ifstream in(state_path_);
    if (!in) {
        return;
    }
    std::stringstream buffer;
    buffer << in.rdbuf();
    auto state = nlohmann::json::parse(buffer.str(), nullptr, false);
    if (!state.is_object()) {
        return;
    }
    for (const auto& [path, perms] : state.items()) {
        if (perms.is_number_unsigned()) {
            guarded_[path] = perms.get<unsigned>();
        }
    }
}

void ReadOnlyGuard::save() const {
    if (state_path_.empty()) {
        return;
    }
    std::error_code error;
    if (guarded_.empty()) {
        fs::remove(state_path_, error);
        return;
    }
    fs::create_directories(fs::path(state_path_).parent_path(), error);
    nlohmann::json state = nlohmann::json::object();
    for (const auto& [path, perms] : guarded_) {
        state[path] = perms;
    }
    std::ofstream out(state_path_, std::ios::trunc);
    out << state.dump();
}

std::set<std::string> ReadOnlyGuard::expand(const std::set<std::string>& lock_paths) const {
    std::set<std::string> out;
    const fs::path root(clone_root_);
    for (const auto& lock_path : lock_paths) {
        if (lock_path.empty() || inside_metadata(lock_path)) {
            continue;
        }
        std::error_code error;
        if (fs::is_directory(root / lock_path, error)) {
            fs::recursive_directory_iterator it(root / lock_path, fs::directory_options::skip_permission_denied, error);
            for (; !error && it != fs::recursive_directory_iterator(); it.increment(error)) {
                if (it->is_regular_file(error)) {
                    out.insert(paths::normalize(fs::relative(it->path(), root, error).generic_string()));
                }
            }
            continue;
        }
        for (const auto& member : paths::lock_unit(lock_path)) {
            if (is_regular(root / member)) {
                out.insert(member);
            }
        }
    }
    return out;
}

ReadOnlyGuard::Result ReadOnlyGuard::apply(const std::set<std::string>& lock_paths) {
    Result result;
    const fs::path root(clone_root_);
    auto wanted = expand(lock_paths);
    for (auto it = guarded_.begin(); it != guarded_.end();) {
        if (wanted.count(it->first) > 0) {
            ++it;
            continue;
        }
        if (is_regular(root / it->first)) {
            if (set_perms(root / it->first, static_cast<fs::perms>(it->second))) {
                result.released.push_back(it->first);
            } else {
                result.failed.push_back(it->first);
            }
        }
        it = guarded_.erase(it);
    }
    for (const auto& path : wanted) {
        fs::perms current = fs::perms::none;
        if (!writable(root / path, current)) {
            continue;
        }
        if (set_perms(root / path, current & ~kWriteBits)) {
            guarded_[path] = static_cast<unsigned>(current);
            result.guarded.push_back(path);
        } else {
            result.failed.push_back(path);
        }
    }
    save();
    return result;
}

ReadOnlyGuard::Result ReadOnlyGuard::release_all() {
    return apply({});
}

std::set<std::string> ReadOnlyGuard::guarded() const {
    std::set<std::string> out;
    for (const auto& entry : guarded_) {
        out.insert(entry.first);
    }
    return out;
}

}  // namespace nipa::locks
