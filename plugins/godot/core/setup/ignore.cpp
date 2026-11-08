#include "core/setup/ignore.h"

#include <filesystem>
#include <fstream>
#include <iterator>
#include <set>
#include <sstream>

namespace nipa::setup {
namespace {

const std::vector<std::string> kTemplate = {
    ".godot/", "/android/", "*.tmp", "export_credentials.cfg", ".mono/", "mono_crash.*.json", "data_*/",
};

std::string trim(const std::string& text) {
    const char* blanks = " \t\r";
    auto begin = text.find_first_not_of(blanks);
    if (begin == std::string::npos) {
        return {};
    }
    auto end = text.find_last_not_of(blanks);
    return text.substr(begin, end - begin + 1);
}

std::vector<std::string> split_lines(const std::string& content) {
    std::vector<std::string> lines;
    std::string line;
    std::istringstream in(content);
    while (std::getline(in, line)) {
        lines.push_back(line);
    }
    return lines;
}

std::string trim_slashes(const std::string& prefix) {
    auto begin = prefix.find_first_not_of('/');
    if (begin == std::string::npos) {
        return {};
    }
    auto end = prefix.find_last_not_of('/');
    return prefix.substr(begin, end - begin + 1);
}

std::string line_key(const std::string& line) {
    std::string key = trim(line);
    if (key.size() > 1 && key.back() == '/') {
        key.pop_back();
    }
    return key;
}

std::string segment_regex(const std::string& segment) {
    std::string out;
    for (std::size_t i = 0; i < segment.size(); ++i) {
        char c = segment[i];
        if (c == '*') {
            out += "[^/]*";
            if (i + 1 < segment.size() && segment[i + 1] == '*') {
                ++i;
            }
        } else if (c == '?') {
            out += "[^/]";
        } else if (std::string("\\^$.|+()[]{}").find(c) != std::string::npos) {
            out += '\\';
            out += c;
        } else {
            out += c;
        }
    }
    return out;
}

std::string pattern_regex(const std::string& pattern, bool anchored) {
    std::vector<std::string> segments;
    std::string segment;
    std::istringstream in(pattern);
    while (std::getline(in, segment, '/')) {
        segments.push_back(segment);
    }
    std::string out = anchored ? "^" : "(?:^|/)";
    for (std::size_t i = 0; i < segments.size(); ++i) {
        if (segments[i] == "**") {
            if (i == 0 && segments.size() == 1) {
                out += ".*";
            } else if (i == 0) {
                out += "(?:[^/]+/)*";
            } else if (i == segments.size() - 1) {
                out += "/.*";
            } else {
                out += "/(?:[^/]+/)*";
            }
            continue;
        }
        if (i > 0 && segments[i - 1] != "**") {
            out += "/";
        }
        out += segment_regex(segments[i]);
    }
    return out + "$";
}

}  // namespace

IgnoreRules IgnoreRules::parse(const std::string& content) {
    IgnoreRules rules;
    for (const auto& raw : split_lines(content)) {
        std::string line = trim(raw);
        if (line.empty() || line[0] == '#') {
            continue;
        }
        Rule rule;
        if (line[0] == '!') {
            rule.negate = true;
            line = trim(line.substr(1));
        }
        rule.dir_only = !line.empty() && line.back() == '/';
        while (!line.empty() && line.back() == '/') {
            line.pop_back();
        }
        bool anchored = line.find('/') != std::string::npos;
        while (!line.empty() && line.front() == '/') {
            line.erase(0, 1);
        }
        if (line.empty()) {
            continue;
        }
        rule.re = std::regex(pattern_regex(line, anchored), std::regex::ECMAScript);
        rules.rules_.push_back(std::move(rule));
    }
    return rules;
}

bool IgnoreRules::match(const std::string& path, bool is_dir) const {
    bool ignored = false;
    for (const auto& rule : rules_) {
        if (rule.dir_only && !is_dir) {
            continue;
        }
        if (std::regex_search(path, rule.re)) {
            ignored = !rule.negate;
        }
    }
    return ignored;
}

bool IgnoreRules::ignores(const std::string& path, bool is_dir) const {
    std::string clean = trim_slashes(path);
    if (rules_.empty() || clean.empty() || clean == ".") {
        return false;
    }
    for (auto slash = clean.find('/'); slash != std::string::npos; slash = clean.find('/', slash + 1)) {
        if (match(clean.substr(0, slash), true)) {
            return true;
        }
    }
    return match(clean, is_dir);
}

std::vector<std::string> ignore_template(const std::string& project_prefix) {
    std::string prefix = trim_slashes(project_prefix);
    std::vector<std::string> out;
    for (const auto& line : kTemplate) {
        out.push_back(!prefix.empty() && line[0] == '/' ? "/" + prefix + line : line);
    }
    return out;
}

std::vector<std::string> missing_ignore_lines(const std::string& content, const std::vector<std::string>& wanted) {
    std::set<std::string> present;
    for (const auto& line : split_lines(content)) {
        auto key = line_key(line);
        if (!key.empty() && key[0] != '#') {
            present.insert(key);
        }
    }
    std::vector<std::string> out;
    for (const auto& line : wanted) {
        auto key = line_key(line);
        if (!key.empty() && present.insert(key).second) {
            out.push_back(trim(line));
        }
    }
    return out;
}

std::string append_lines(const std::string& content, const std::vector<std::string>& lines) {
    if (lines.empty()) {
        return content;
    }
    std::string eol = content.find("\r\n") != std::string::npos ? "\r\n" : "\n";
    std::string out = content;
    if (!out.empty() && out.back() != '\n') {
        out += eol;
    }
    for (const auto& line : lines) {
        out += line + eol;
    }
    return out;
}

std::string merge_ignore(const std::string& content, const std::vector<std::string>& wanted) {
    return append_lines(content, missing_ignore_lines(content, wanted));
}

std::string complete_ignore(const std::string& content, const std::string& project_prefix) {
    std::string merged = merge_ignore(content, ignore_template(project_prefix));
    if (ignores_godot_dir(merged, project_prefix)) {
        return merged;
    }
    return append_lines(merged, {"/" + godot_dir(project_prefix) + "/"});
}

std::string godot_dir(const std::string& project_prefix) {
    std::string prefix = trim_slashes(project_prefix);
    return prefix.empty() ? ".godot" : prefix + "/.godot";
}

bool ignores_godot_dir(const std::string& content, const std::string& project_prefix) {
    return IgnoreRules::parse(content).ignores(godot_dir(project_prefix), true);
}

std::optional<std::string> read_ignore(const std::string& clone_root) {
    std::ifstream in(std::filesystem::path(clone_root) / kIgnoreFile, std::ios::binary);
    if (!in) {
        return std::nullopt;
    }
    return std::string(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
}

WriteResult write_ignore(const std::string& clone_root, const std::string& project_prefix) {
    WriteResult result;
    auto existing = read_ignore(clone_root);
    std::string content = existing.value_or(std::string());
    std::string merged = complete_ignore(content, project_prefix);
    if (existing && merged == content) {
        result.ok = true;
        return result;
    }
    auto target = std::filesystem::path(clone_root) / kIgnoreFile;
    auto temp = target;
    temp += ".tmp";
    {
        std::ofstream out(temp, std::ios::binary | std::ios::trunc);
        out.write(merged.data(), static_cast<std::streamsize>(merged.size()));
        if (!out) {
            result.error = "cannot write " + temp.string();
            return result;
        }
    }
    std::error_code error;
    std::filesystem::rename(temp, target, error);
    if (error) {
        std::filesystem::remove(temp, error);
        result.error = "cannot write " + target.string();
        return result;
    }
    result.ok = true;
    result.changed = true;
    return result;
}

}  // namespace nipa::setup
