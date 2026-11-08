#pragma once

#include <string>

namespace nipa {

inline constexpr const char* kPluginVersion = "0.1.0";

// True when `version` is a numbered release older than `minimum`. Empty or
// unparsable minimums never fail, and development builds ("dev-<sha>") count
// as current.
bool version_older(const std::string& version, const std::string& minimum);

}  // namespace nipa
