#pragma once

#include <optional>
#include <regex>
#include <string>
#include <vector>

namespace nipa::setup {

inline constexpr const char* kIgnoreFile = ".nipaignore";

// The client's .nipaignore subset (internal/client/ignore): `#` comments, `!`
// negation, `*`/`?`, `**`, leading or inner `/` anchoring, trailing `/` for
// directories only; the last matching rule wins.
class IgnoreRules {
public:
    static IgnoreRules parse(const std::string& content);

    // `path` is clone-relative; a path inside an ignored directory is ignored.
    bool ignores(const std::string& path, bool is_dir) const;

private:
    struct Rule {
        std::regex re;
        bool negate = false;
        bool dir_only = false;
    };

    bool match(const std::string& path, bool is_dir) const;

    std::vector<Rule> rules_;
};

// The Godot template; anchored entries are moved under the project folder.
std::vector<std::string> ignore_template(const std::string& project_prefix);

// Template lines `content` does not already have (trimmed, CRLF tolerant;
// `x` and `x/` count as the same line).
std::vector<std::string> missing_ignore_lines(const std::string& content, const std::vector<std::string>& wanted);

// `content` with the missing lines appended, keeping its line endings.
std::string merge_ignore(const std::string& content, const std::vector<std::string>& wanted);

// `content` with `lines` appended, keeping its line endings.
std::string append_lines(const std::string& content, const std::vector<std::string>& lines);

// The template merged in; when a later rule still re-includes the project's
// `.godot/`, an anchored `/<project>/.godot/` rule is appended (last match wins).
std::string complete_ignore(const std::string& content, const std::string& project_prefix);

// True when the project's `.godot/` folder is ignored by `content`.
bool ignores_godot_dir(const std::string& content, const std::string& project_prefix);

// Clone-relative path of the project's `.godot` folder.
std::string godot_dir(const std::string& project_prefix);

std::optional<std::string> read_ignore(const std::string& clone_root);

struct WriteResult {
    bool ok = false;
    bool changed = false;
    std::string error;
};

// Creates or completes <clone_root>/.nipaignore (temp file + rename). Never throws.
WriteResult write_ignore(const std::string& clone_root, const std::string& project_prefix);

}  // namespace nipa::setup
