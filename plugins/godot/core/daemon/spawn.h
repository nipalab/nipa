#pragma once

#include <string>

namespace nipa::daemon {

// Starts `binary serve [--endpoint path]` detached from the editor. Returns
// false only when the process could not be started.
bool spawn_detached(const std::string& binary, const std::string& endpoint_path);

}  // namespace nipa::daemon
