#pragma once

#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "core/status/status.h"

namespace nipa::setup {

inline constexpr const char* kVcsPluginName = "NipaVCSInterface";

// daemon MetaWarning (engine godot).
struct SidecarWarning {
    std::string path;
    std::string kind;  // missing-sidecar, orphan-sidecar, sidecar-not-staged, uid-changed
    std::string related;
    std::string message;

    bool operator==(const SidecarWarning& o) const {
        return path == o.path && kind == o.kind && related == o.related && message == o.message;
    }
    // Only an unstaged sidecar (or its deletion) has a one-click fix: staging it.
    bool stageable() const { return kind == "sidecar-not-staged"; }
};

std::vector<SidecarWarning> parse_meta_warnings(const nlohmann::json& j);
nlohmann::json to_json(const std::vector<SidecarWarning>& warnings);
std::vector<std::string> stage_paths(const std::vector<SidecarWarning>& warnings);

enum class Tracking { Unknown, NotTracked, Tracked };

// What is known about the project's .godot/ folder.
struct GodotDirState {
    Tracking server = Tracking::Unknown;
    std::string error;
    std::vector<std::string> staged;     // will be pushed with the next commit
    std::vector<std::string> changed;    // tracked files with local changes
    std::vector<std::string> untracked;  // not ignored
};

// Status entries below the project's .godot/ folder.
GodotDirState godot_dir_from_status(const status::Snapshot& snapshot, const std::string& project_prefix);

// Classifies a ProxyTreeManifest outcome for the .godot path: an HTTP 404
// naming the path means the server has no such folder.
Tracking tracking_from_manifest(int http_status, const std::string& message);

enum class Level { Ok, Warning, Error, Unknown };

const char* level_name(Level level);

struct Row {
    std::string id;
    Level level = Level::Ok;
    std::string title;
    std::string detail;
    std::string fix;        // action id, empty when there is nothing to click
    std::string fix_label;
    std::vector<std::string> paths;  // clone-relative paths the fix acts on
};

struct Inputs {
    std::string vcs_plugin_name;
    bool vcs_autoload = false;
    std::string project_prefix;
    std::optional<std::string> ignore_content;  // nullopt: no .nipaignore
    GodotDirState godot_dir;
    bool sidecars_known = false;
    std::vector<SidecarWarning> sidecars;
    bool connected = false;
    std::string daemon_version;
    std::string min_version;
    std::string plugin_version;
    std::string binary;  // the nipa binary the connector would start; empty when none
    std::string connect_error;
};

// Rows in display order: vcs, ignore, godot-dir, sidecars, version.
std::vector<Row> evaluate(const Inputs& inputs);

nlohmann::json to_json(const std::vector<Row>& rows);

}  // namespace nipa::setup
