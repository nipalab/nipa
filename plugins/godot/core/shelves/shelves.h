#pragma once

#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace nipa::shelves {

// daemon ShelfEntry
struct Entry {
    std::string path;
    std::string status;
    bool staged = false;
    bool untracked = false;
    bool locked = false;
};

// daemon Shelf
struct Shelf {
    std::string id;
    std::string name;
    long long created_at = 0;
    std::string branch;
    std::string tag;
    long long size_bytes = 0;
    std::vector<Entry> entries;
};

// daemon ShelveListResponse; `applying` names the shelf whose apply waits on conflicts.
struct List {
    std::vector<Shelf> shelves;
    std::string applying;
};

Shelf parse_shelf(const nlohmann::json& j);
List parse_list(const nlohmann::json& j);
nlohmann::json to_json(const Shelf& shelf);
nlohmann::json to_json(const List& list);

// SyncPlanResponse.overwritten
std::vector<std::string> parse_overwritten(const nlohmann::json& j);

// One line for a ShelveResult or ShelveApplyResult; empty for other results.
std::string result_summary(const nlohmann::json& result);

}  // namespace nipa::shelves
