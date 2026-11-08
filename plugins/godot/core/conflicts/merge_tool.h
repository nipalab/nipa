#pragma once

#include <optional>
#include <string>
#include <vector>

namespace nipa::conflicts {

// Temp files handed to an external merge tool. `merged` starts as the working
// copy and is read back as the resolution.
struct ToolPaths {
    std::string base;
    std::string local;
    std::string remote;
    std::string merged;
};

// Files under `dir` named after `clone_path`, e.g. dir/player.BASE.gd.
ToolPaths tool_paths(const std::string& dir, const std::string& clone_path);

// Splits a command line into words like a POSIX shell: blanks separate,
// '...' is literal, "..." honours \" and \\, a backslash escapes outside
// quotes. nullopt on an unterminated quote.
std::optional<std::vector<std::string>> split_command(const std::string& line);

// Splits `command` (git's mergetool convention) and substitutes $BASE,
// $LOCAL, $REMOTE and $MERGED (or ${NAME}) inside each word, so a path with
// blanks stays one argument. A command without placeholders gets the four
// paths appended. Empty when the command is blank or badly quoted.
std::vector<std::string> build_merge_command(const std::string& command, const ToolPaths& paths);

// Shell-quoted form for display and logs.
std::string quote_argument(const std::string& arg);
std::string command_line(const std::vector<std::string>& argv);

// True when a line starts with a <<<<<<< or >>>>>>> conflict marker.
bool has_conflict_markers(const std::string& text);

}  // namespace nipa::conflicts
