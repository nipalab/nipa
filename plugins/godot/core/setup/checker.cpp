#include "core/setup/checker.h"

#include <algorithm>
#include <cstddef>
#include <set>

#include "core/daemon/messages.h"
#include "core/setup/ignore.h"
#include "core/version.h"

namespace nipa::setup {
namespace {

constexpr std::size_t kDetailLines = 10;

std::vector<std::string> under(const std::vector<std::string>& paths, const std::string& dir) {
    std::set<std::string> out;
    for (const auto& path : paths) {
        if (path == dir || path.compare(0, dir.size() + 1, dir + "/") == 0) {
            out.insert(path);
        }
    }
    return {out.begin(), out.end()};
}

std::string join(const std::vector<std::string>& parts, const std::string& separator) {
    std::string out;
    for (const auto& part : parts) {
        out += (out.empty() ? "" : separator) + part;
    }
    return out;
}

std::string listing(const std::vector<std::string>& lines) {
    std::vector<std::string> shown(lines.begin(), lines.begin() + static_cast<std::ptrdiff_t>(
                                                                    std::min(lines.size(), kDetailLines)));
    std::string out = join(shown, "\n");
    if (lines.size() > kDetailLines) {
        out += "\nand " + std::to_string(lines.size() - kDetailLines) + " more";
    }
    return out;
}

Row vcs_row(const Inputs& in) {
    Row row{"vcs", Level::Ok, "Version control plugin", "NipaVCSInterface, loaded on startup.", "", "", {}};
    if (in.vcs_plugin_name == kVcsPluginName && in.vcs_autoload) {
        return row;
    }
    row.level = Level::Error;
    std::vector<std::string> problems;
    if (in.vcs_plugin_name.empty()) {
        problems.push_back("No version control plugin is selected.");
    } else if (in.vcs_plugin_name != kVcsPluginName) {
        problems.push_back("The project uses \"" + in.vcs_plugin_name + "\".");
    }
    if (!in.vcs_autoload) {
        problems.push_back("Autoload on startup is off.");
    }
    row.detail = join(problems, " ") + " The Commit dock needs NipaVCSInterface.";
    row.fix = "set_vcs";
    row.fix_label = "Use NipaVCSInterface";
    return row;
}

Row ignore_row(const Inputs& in) {
    Row row{"ignore", Level::Ok, ".nipaignore", "Has the Godot patterns.", "", "", {}};
    if (!in.ignore_content) {
        row.level = Level::Warning;
        row.detail = "Missing: .godot/, export credentials and other generated files show up as changes.";
        row.fix = "write_ignore";
        row.fix_label = "Create .nipaignore";
        return row;
    }
    auto missing = missing_ignore_lines(*in.ignore_content, ignore_template(in.project_prefix));
    if (!ignores_godot_dir(*in.ignore_content, in.project_prefix)) {
        row.level = Level::Warning;
        row.detail = "Does not ignore " + godot_dir(in.project_prefix) + "/.";
    } else if (!missing.empty()) {
        row.level = Level::Warning;
        row.detail = std::to_string(missing.size()) + " recommended pattern(s) missing: " + join(missing, ", ") + ".";
    } else {
        return row;
    }
    row.fix = "write_ignore";
    row.fix_label = "Add Godot patterns";
    return row;
}

Row godot_dir_row(const Inputs& in) {
    const auto& state = in.godot_dir;
    std::string dir = godot_dir(in.project_prefix) + "/";
    Row row{"godot-dir", Level::Ok, dir + " folder", "Not versioned.", "", "", {}};
    if (!state.staged.empty()) {
        row.level = Level::Error;
        row.detail = std::to_string(state.staged.size()) + " file(s) under " + dir +
                     " are staged and would be pushed:\n" + listing(state.staged);
        row.fix = "unstage_godot";
        row.fix_label = "Unstage them";
        row.paths = state.staged;
        return row;
    }
    if (state.server == Tracking::Tracked || !state.changed.empty()) {
        row.level = Level::Error;
        row.detail = "The editor cache is versioned. Close Godot, delete " + dir +
                     ", stage the deletions with `nipa add " + godot_dir(in.project_prefix) +
                     "` and push; Godot rebuilds the folder on the next start.";
        return row;
    }
    if (!state.untracked.empty()) {
        row.level = Level::Warning;
        row.detail = std::to_string(state.untracked.size()) + " untracked file(s) under " + dir +
                     " are not ignored.";
        row.fix = "write_ignore";
        row.fix_label = "Add Godot patterns";
        return row;
    }
    if (state.server == Tracking::Unknown) {
        row.level = Level::Unknown;
        row.detail = "Could not check the server" + (state.error.empty() ? "." : ": " + state.error);
    }
    return row;
}

Row sidecar_row(const Inputs& in) {
    Row row{"sidecars", Level::Ok, "Sidecars (.import / .uid)", "No problems in the pending changes.", "", "", {}};
    if (!in.sidecars_known) {
        row.level = Level::Unknown;
        row.detail = "The daemon did not answer MetaWarnings.";
        return row;
    }
    if (in.sidecars.empty()) {
        return row;
    }
    row.level = Level::Warning;
    std::vector<std::string> lines;
    for (const auto& w : in.sidecars) {
        lines.push_back(w.path + ": " + w.message);
    }
    row.detail = std::to_string(in.sidecars.size()) + " warning(s):\n" + listing(lines);
    row.paths = stage_paths(in.sidecars);
    if (!row.paths.empty()) {
        row.fix = "stage_sidecars";
        row.fix_label = "Stage sidecars";
    }
    return row;
}

Row version_row(const Inputs& in) {
    Row row{"version", Level::Ok, "nipa daemon", "", "", "", {}};
    std::string cli = "CLI: " + (in.binary.empty() ? "not found (set nipa/binary_path, NIPA_BIN or PATH)" : in.binary);
    if (!in.connected) {
        row.level = Level::Error;
        row.title = "nipa daemon not connected";
        row.detail = (in.connect_error.empty() ? "" : in.connect_error + "\n") + cli;
        return row;
    }
    std::string versions = "Daemon " + in.daemon_version + ", minimum " +
                           (in.min_version.empty() ? "-" : in.min_version) + ", plugin " + in.plugin_version + ".";
    if (version_older(in.daemon_version, in.min_version)) {
        row.level = Level::Error;
        row.title = "nipa daemon is too old";
        row.detail = versions + " Update the nipa CLI.\n" + cli;
        return row;
    }
    row.title = "nipa daemon " + in.daemon_version;
    row.detail = versions + "\n" + cli;
    return row;
}

}  // namespace

std::vector<SidecarWarning> parse_meta_warnings(const nlohmann::json& j) {
    std::vector<SidecarWarning> out;
    auto found = j.find("warnings");
    if (found == j.end() || !found->is_array()) {
        return out;
    }
    for (const auto& item : *found) {
        if (!item.is_object()) {
            continue;
        }
        out.push_back({daemon::json_string(item, "path"), daemon::json_string(item, "kind"),
                       daemon::json_string(item, "related"), daemon::json_string(item, "message")});
    }
    return out;
}

nlohmann::json to_json(const std::vector<SidecarWarning>& warnings) {
    auto out = nlohmann::json::array();
    for (const auto& w : warnings) {
        out.push_back({{"path", w.path},
                       {"kind", w.kind},
                       {"related", w.related},
                       {"message", w.message},
                       {"stageable", w.stageable()}});
    }
    return out;
}

std::vector<std::string> stage_paths(const std::vector<SidecarWarning>& warnings) {
    std::set<std::string> out;
    for (const auto& w : warnings) {
        if (w.stageable() && !w.path.empty()) {
            out.insert(w.path);
        }
    }
    return {out.begin(), out.end()};
}

GodotDirState godot_dir_from_status(const status::Snapshot& snapshot, const std::string& project_prefix) {
    std::string dir = godot_dir(project_prefix);
    GodotDirState state;
    auto staged = under(snapshot.staged, dir);
    auto added = under(snapshot.added, dir);
    std::set<std::string> staged_set(staged.begin(), staged.end());
    staged_set.insert(added.begin(), added.end());
    state.staged.assign(staged_set.begin(), staged_set.end());

    std::set<std::string> changed;
    for (const auto* list : {&snapshot.modified, &snapshot.missing, &snapshot.deleted, &snapshot.conflicts}) {
        for (const auto& path : under(*list, dir)) {
            if (!staged_set.count(path)) {
                changed.insert(path);
            }
        }
    }
    state.changed.assign(changed.begin(), changed.end());
    state.untracked = under(snapshot.untracked, dir);
    return state;
}

Tracking tracking_from_manifest(int http_status, const std::string& message) {
    if (http_status == 200) {
        return Tracking::Tracked;
    }
    if (http_status == 404 && message.find("path") != std::string::npos) {
        return Tracking::NotTracked;
    }
    return Tracking::Unknown;
}

const char* level_name(Level level) {
    switch (level) {
        case Level::Ok:
            return "ok";
        case Level::Warning:
            return "warning";
        case Level::Error:
            return "error";
        case Level::Unknown:
            break;
    }
    return "unknown";
}

std::vector<Row> evaluate(const Inputs& inputs) {
    return {vcs_row(inputs), ignore_row(inputs), godot_dir_row(inputs), sidecar_row(inputs), version_row(inputs)};
}

nlohmann::json to_json(const std::vector<Row>& rows) {
    auto out = nlohmann::json::array();
    for (const auto& row : rows) {
        out.push_back({{"id", row.id},
                       {"level", level_name(row.level)},
                       {"title", row.title},
                       {"detail", row.detail},
                       {"fix", row.fix},
                       {"fix_label", row.fix_label},
                       {"paths", row.paths}});
    }
    return out;
}

}  // namespace nipa::setup
