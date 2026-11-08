#include "core/conflicts/conflicts.h"

#include <algorithm>
#include <cctype>

#include "core/daemon/messages.h"

namespace nipa::conflicts {
namespace {

using daemon::json_string;

bool flag(const nlohmann::json& j, const char* key) {
    auto found = j.find(key);
    return found != j.end() && found->is_boolean() && found->get<bool>();
}

Revision parse_revision(const nlohmann::json& j, const char* key) {
    if (!j.contains(key) || !j[key].is_object()) {
        return {};
    }
    return {json_string(j[key], "id"), json_string(j[key], "label")};
}

nlohmann::json revision_json(const Revision& r) {
    return {{"id", r.id}, {"label", r.label}};
}

std::string lower_extension(const std::string& path) {
    auto slash = path.find_last_of('/');
    auto dot = path.find_last_of('.');
    if (dot == std::string::npos || (slash != std::string::npos && dot < slash)) {
        return {};
    }
    std::string ext = path.substr(dot + 1);
    std::transform(ext.begin(), ext.end(), ext.begin(), [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    return ext;
}

}  // namespace

int Set::open_count() const {
    return static_cast<int>(std::count_if(files.begin(), files.end(), [](const File& f) { return !f.resolved; }));
}

Set parse_conflicts(const nlohmann::json& j) {
    Set set;
    if (!j.is_object()) {
        return set;
    }
    set.op = json_string(j, "op");
    set.base = parse_revision(j, "base");
    set.ours = parse_revision(j, "ours");
    set.theirs = parse_revision(j, "theirs");
    auto files = j.find("files");
    if (files != j.end() && files->is_array()) {
        for (const auto& item : *files) {
            File f;
            f.path = json_string(item, "path");
            f.kind = json_string(item, "kind");
            f.binary = flag(item, "binary");
            f.resolved = flag(item, "resolved");
            f.viewer = json_string(item, "viewer");
            set.files.push_back(std::move(f));
        }
    }
    return set;
}

bool godot_mergeable(const std::string& path) {
    auto ext = lower_extension(path);
    return ext == "tscn" || ext == "tres";
}

std::vector<Action> actions_for(const File& file) {
    std::vector<Action> out{Action::Mine, Action::Theirs};
    bool both_sides = file.kind != "modify_delete" && file.kind != "delete_modify";
    if (!file.binary && both_sides) {
        out.push_back(godot_mergeable(file.path) ? Action::Merge : Action::ExternalTool);
    }
    if (file.viewer == "image" && both_sides) {
        out.push_back(Action::Compare);
    }
    return out;
}

const char* action_name(Action action) {
    switch (action) {
        case Action::Mine:
            return "mine";
        case Action::Theirs:
            return "theirs";
        case Action::Merge:
            return "merge";
        case Action::ExternalTool:
            return "external";
        case Action::Compare:
            return "compare";
    }
    return "";
}

std::string GodotConflict::label() const {
    if (section.empty()) {
        return key;
    }
    return key.empty() ? section : section + " \xc2\xb7 " + key;
}

GodotMerge parse_godot_merge(const nlohmann::json& j) {
    GodotMerge out;
    if (!j.is_object()) {
        return out;
    }
    out.merged = daemon::base64_decode(json_string(j, "merged"));
    out.clean = flag(j, "clean");
    auto list = j.find("conflicts");
    if (list != j.end() && list->is_array()) {
        for (const auto& item : *list) {
            out.conflicts.push_back({json_string(item, "section"), json_string(item, "key"), json_string(item, "message")});
        }
    }
    return out;
}

nlohmann::json to_json(const Set& set) {
    nlohmann::json files = nlohmann::json::array();
    for (const auto& f : set.files) {
        nlohmann::json actions = nlohmann::json::array();
        for (auto a : actions_for(f)) {
            actions.push_back(action_name(a));
        }
        files.push_back({{"path", f.path},
                         {"kind", f.kind},
                         {"binary", f.binary},
                         {"resolved", f.resolved},
                         {"viewer", f.viewer},
                         {"actions", actions}});
    }
    return {{"op", set.op},
            {"base", revision_json(set.base)},
            {"ours", revision_json(set.ours)},
            {"theirs", revision_json(set.theirs)},
            {"files", files},
            {"open", set.open_count()},
            {"can_finish", set.can_finish()}};
}

nlohmann::json to_json(const GodotMerge& merge) {
    nlohmann::json list = nlohmann::json::array();
    for (const auto& c : merge.conflicts) {
        list.push_back({{"section", c.section}, {"key", c.key}, {"message", c.message}, {"label", c.label()}});
    }
    return {{"clean", merge.clean}, {"conflicts", list}};
}

}  // namespace nipa::conflicts
