#pragma once

#include <functional>
#include <optional>
#include <set>
#include <string>
#include <vector>

namespace nipa::paths {

using FileExists = std::function<bool(const std::string&)>;

// Converts backslashes to slashes; every path in this module uses '/'.
std::string normalize(const std::string& path);

// Sidecars of an asset or script: `foo.png` -> `foo.png.import`, `foo.gd` -> `foo.gd.uid`.
std::vector<std::string> sidecar_candidates(const std::string& path);

// Adds the sidecars of each path that exist in `existing`, keeping order and dropping duplicates.
std::vector<std::string> expand_with_sidecars(const std::vector<std::string>& paths,
                                              const std::set<std::string>& existing);

class ProjectPaths {
public:
    // Walks up from `project_dir` (at most 32 parents) to the directory holding .nipa/config.
    static std::optional<ProjectPaths> locate(const std::string& project_dir, const FileExists& exists);

    ProjectPaths(std::string clone_root, std::string project_prefix);

    const std::string& clone_root() const { return clone_root_; }
    // Clone-relative folder of the Godot project ("" when it is the clone root).
    const std::string& project_prefix() const { return project_prefix_; }

    // Accepts `res://x.tscn` or a project-relative `x.tscn`; returns a clone-relative path.
    std::string to_clone(const std::string& path) const;

    // Clone-relative to project-relative; nullopt when the file is outside the project.
    std::optional<std::string> to_project(const std::string& clone_path) const;

    bool outside_project(const std::string& clone_path) const { return !to_project(clone_path).has_value(); }

    static std::string to_res(const std::string& project_path);

private:
    std::string clone_root_;
    std::string project_prefix_;
};

}  // namespace nipa::paths
