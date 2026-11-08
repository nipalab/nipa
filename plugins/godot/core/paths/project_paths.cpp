#include "core/paths/project_paths.h"

#include <algorithm>

namespace nipa::paths {
namespace {

constexpr int kMaxParents = 32;
constexpr const char* kResPrefix = "res://";

std::string join(const std::string& dir, const std::string& name) {
    if (dir.empty()) {
        return name;
    }
    if (dir.back() == '/') {
        return dir + name;
    }
    return dir + "/" + name;
}

std::string trim_trailing(std::string path) {
    while (path.size() > 1 && path.back() == '/') {
        path.pop_back();
    }
    return path;
}

std::string trim_relative(std::string path) {
    while (!path.empty() && path.front() == '/') {
        path.erase(0, 1);
    }
    return trim_trailing(path);
}

}  // namespace

std::string normalize(const std::string& path) {
    std::string out = path;
    std::replace(out.begin(), out.end(), '\\', '/');
    return out;
}

std::vector<std::string> sidecar_candidates(const std::string& path) {
    return {path + ".import", path + ".uid"};
}

std::vector<std::string> expand_with_sidecars(const std::vector<std::string>& paths,
                                              const std::set<std::string>& existing) {
    std::vector<std::string> out;
    std::set<std::string> seen;
    auto push = [&](const std::string& path) {
        if (seen.insert(path).second) {
            out.push_back(path);
        }
    };
    for (const auto& raw : paths) {
        push(normalize(raw));
    }
    for (const auto& raw : paths) {
        for (const auto& sidecar : sidecar_candidates(normalize(raw))) {
            if (existing.count(sidecar)) {
                push(sidecar);
            }
        }
    }
    return out;
}

std::optional<ProjectPaths> ProjectPaths::locate(const std::string& project_dir, const FileExists& exists) {
    std::string probe = trim_trailing(normalize(project_dir));
    std::string relative;
    for (int depth = 0; depth <= kMaxParents; ++depth) {
        if (exists(join(probe, ".nipa/config"))) {
            return ProjectPaths(probe, relative);
        }
        auto slash = probe.find_last_of('/');
        if (slash == std::string::npos) {
            break;
        }
        std::string name = probe.substr(slash + 1);
        if (name.empty()) {
            break;
        }
        relative = relative.empty() ? name : name + "/" + relative;
        probe = probe.substr(0, slash);
        if (probe.empty()) {
            probe = "/";
        }
    }
    return std::nullopt;
}

ProjectPaths::ProjectPaths(std::string clone_root, std::string project_prefix)
    : clone_root_(trim_trailing(normalize(clone_root))), project_prefix_(trim_relative(normalize(project_prefix))) {}

std::string ProjectPaths::to_clone(const std::string& path) const {
    std::string rel = normalize(path);
    if (rel.rfind(kResPrefix, 0) == 0) {
        rel = rel.substr(std::string(kResPrefix).size());
    }
    return trim_relative(join(project_prefix_, trim_relative(rel)));
}

std::optional<std::string> ProjectPaths::to_project(const std::string& clone_path) const {
    std::string path = trim_trailing(normalize(clone_path));
    if (project_prefix_.empty()) {
        return path;
    }
    std::string head = project_prefix_ + "/";
    if (path.rfind(head, 0) == 0) {
        return path.substr(head.size());
    }
    return std::nullopt;
}

std::string ProjectPaths::to_res(const std::string& project_path) {
    return kResPrefix + trim_relative(normalize(project_path));
}

}  // namespace nipa::paths
