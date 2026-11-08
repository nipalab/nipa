#pragma once

#include <string>

#include <nlohmann/json.hpp>

namespace nipa::preview {

// Collects one Preview stream: the side infos, each side's PNG assembled
// once from its chunks, and the pixel-diff statistics.
class Assembler {
public:
    // Feeds one stream line. Returns false on a failure event (see error()).
    bool add(const nlohmann::json& event);

    const std::string& png(const std::string& side) const;
    const std::string& error() const { return error_; }
    int failure_code() const { return failure_code_; }
    // {"old": info, "new": info, "diff": stats}; absent parts are omitted.
    const nlohmann::json& summary() const { return summary_; }

private:
    std::string old_png_;
    std::string new_png_;
    std::string error_;
    int failure_code_ = 0;
    nlohmann::json summary_ = nlohmann::json::object();
};

// Body of a Preview request comparing two BlobRefs ({"source", "revision", "path"}).
nlohmann::json request(const std::string& root, const nlohmann::json& old_ref, const nlohmann::json& new_ref,
                       int threshold, int max_size);

}  // namespace nipa::preview
