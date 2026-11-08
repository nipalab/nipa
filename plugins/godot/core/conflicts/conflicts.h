#pragma once

#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace nipa::conflicts {

struct Revision {
    std::string id;
    std::string label;
};

struct File {
    std::string path;  // clone-relative
    std::string kind;  // text, binary, add_add, modify_delete, delete_modify
    bool binary = false;
    bool resolved = false;
    std::string viewer;  // internal/preview kind: text, image, scene, ...
};

// What a pending merge or revert waits on (daemon Conflicts).
struct Set {
    std::string op;  // merge or revert; empty when nothing is pending
    Revision base;
    Revision ours;
    Revision theirs;
    std::vector<File> files;

    bool pending() const { return !op.empty(); }
    int open_count() const;
    // Finish (merge) and Continue (revert) need every conflict resolved.
    bool can_finish() const { return pending() && open_count() == 0; }
};

Set parse_conflicts(const nlohmann::json& j);

// Resolution actions offered for one conflicted file, in UI order.
enum class Action { Mine, Theirs, Merge, ExternalTool, Compare };
std::vector<Action> actions_for(const File& file);
const char* action_name(Action action);

// .tscn/.tres: the Godot-aware three-way merge applies.
bool godot_mergeable(const std::string& path);

struct GodotConflict {
    std::string section;
    std::string key;
    std::string message;

    // "section · key" (or the section alone) for the conflict list.
    std::string label() const;
};

// Result of MergeGodotResource; `merged` holds the decoded bytes.
struct GodotMerge {
    std::string merged;
    bool clean = false;
    std::vector<GodotConflict> conflicts;
};

GodotMerge parse_godot_merge(const nlohmann::json& j);

nlohmann::json to_json(const Set& set);
nlohmann::json to_json(const GodotMerge& merge);

}  // namespace nipa::conflicts
