#include "core/shelves/shelves.h"

#include "core/daemon/messages.h"

namespace nipa::shelves {
namespace {

using json = nlohmann::json;

bool flag(const json& j, const char* key) {
    auto found = j.find(key);
    return j.is_object() && found != j.end() && found->is_boolean() && found->get<bool>();
}

const json& object_at(const json& j, const char* key) {
    static const json kEmpty = json::object();
    if (!j.is_object()) {
        return kEmpty;
    }
    auto found = j.find(key);
    return found != j.end() && found->is_object() ? *found : kEmpty;
}

}  // namespace

Shelf parse_shelf(const json& j) {
    Shelf shelf;
    shelf.id = daemon::json_string(j, "id");
    shelf.name = daemon::json_string(j, "name");
    shelf.created_at = daemon::json_int(j, "created_at");
    shelf.branch = daemon::json_string(j, "branch");
    shelf.tag = daemon::json_string(j, "tag");
    shelf.size_bytes = daemon::json_int(j, "size_bytes");
    auto entries = j.is_object() ? j.find("entries") : j.end();
    if (j.is_object() && entries != j.end() && entries->is_array()) {
        for (const auto& item : *entries) {
            shelf.entries.push_back({daemon::json_string(item, "path"), daemon::json_string(item, "status"),
                                     flag(item, "staged"), flag(item, "untracked"), flag(item, "locked")});
        }
    }
    return shelf;
}

List parse_list(const json& j) {
    List list;
    list.applying = daemon::json_string(j, "applying");
    auto shelves = j.is_object() ? j.find("shelves") : j.end();
    if (j.is_object() && shelves != j.end() && shelves->is_array()) {
        for (const auto& item : *shelves) {
            list.shelves.push_back(parse_shelf(item));
        }
    }
    return list;
}

json to_json(const Shelf& shelf) {
    json entries = json::array();
    for (const auto& e : shelf.entries) {
        entries.push_back({{"path", e.path},
                           {"status", e.status},
                           {"staged", e.staged},
                           {"untracked", e.untracked},
                           {"locked", e.locked}});
    }
    return {{"id", shelf.id},         {"name", shelf.name},
            {"created_at", shelf.created_at}, {"branch", shelf.branch},
            {"tag", shelf.tag},       {"size_bytes", shelf.size_bytes},
            {"entries", entries}};
}

json to_json(const List& list) {
    json shelves = json::array();
    for (const auto& shelf : list.shelves) {
        shelves.push_back(to_json(shelf));
    }
    return {{"shelves", shelves}, {"applying", list.applying}};
}

std::vector<std::string> parse_overwritten(const json& j) {
    return daemon::json_strings(j, "overwritten");
}

std::string result_summary(const json& result) {
    const auto& created = object_at(result, "shelve");
    if (!created.empty()) {
        auto shelf = parse_shelf(object_at(created, "shelf"));
        return "shelved " + std::to_string(shelf.entries.size()) + " file(s) as \"" + shelf.name + "\"";
    }
    const auto& applied = object_at(result, "shelve_apply");
    if (applied.empty()) {
        return {};
    }
    auto conflicts = daemon::json_strings(applied, "conflicts");
    if (!conflicts.empty()) {
        return std::to_string(conflicts.size()) + " conflict(s) to resolve";
    }
    if (flag(applied, "aborted")) {
        return "shelf apply aborted";
    }
    return flag(applied, "dropped") ? "shelf applied and deleted" : "shelf applied";
}

}  // namespace nipa::shelves
