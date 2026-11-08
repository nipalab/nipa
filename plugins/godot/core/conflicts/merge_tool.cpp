#include "core/conflicts/merge_tool.h"

#include <utility>

namespace nipa::conflicts {
namespace {

bool blank(char c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r';
}

bool replace_all(std::string& word, const std::string& from, const std::string& to) {
    bool replaced = false;
    for (auto at = word.find(from); at != std::string::npos; at = word.find(from, at + to.size())) {
        word.replace(at, from.size(), to);
        replaced = true;
    }
    return replaced;
}

}  // namespace

ToolPaths tool_paths(const std::string& dir, const std::string& clone_path) {
    auto slash = clone_path.find_last_of('/');
    std::string name = slash == std::string::npos ? clone_path : clone_path.substr(slash + 1);
    auto dot = name.find_last_of('.');
    std::string stem = dot == std::string::npos || dot == 0 ? name : name.substr(0, dot);
    std::string ext = dot == std::string::npos || dot == 0 ? std::string() : name.substr(dot);
    auto at = [&](const char* tag) { return dir + "/" + stem + "." + tag + ext; };
    return {at("BASE"), at("LOCAL"), at("REMOTE"), at("MERGED")};
}

std::optional<std::vector<std::string>> split_command(const std::string& line) {
    std::vector<std::string> words;
    std::string word;
    bool in_word = false;
    for (std::size_t i = 0; i < line.size(); ++i) {
        char c = line[i];
        if (blank(c)) {
            if (in_word) {
                words.push_back(std::exchange(word, {}));
                in_word = false;
            }
            continue;
        }
        in_word = true;
        if (c == '\'') {
            auto end = line.find('\'', i + 1);
            if (end == std::string::npos) {
                return std::nullopt;
            }
            word += line.substr(i + 1, end - i - 1);
            i = end;
        } else if (c == '"') {
            ++i;
            while (i < line.size() && line[i] != '"') {
                if (line[i] == '\\' && i + 1 < line.size() && (line[i + 1] == '"' || line[i + 1] == '\\')) {
                    ++i;
                }
                word += line[i++];
            }
            if (i >= line.size()) {
                return std::nullopt;
            }
        } else if (c == '\\' && i + 1 < line.size()) {
            word += line[++i];
        } else {
            word += c;
        }
    }
    if (in_word) {
        words.push_back(word);
    }
    return words;
}

std::vector<std::string> build_merge_command(const std::string& command, const ToolPaths& paths) {
    auto split = split_command(command);
    if (!split || split->empty()) {
        return {};
    }
    auto words = std::move(*split);
    const std::pair<const char*, const std::string*> names[] = {
        {"BASE", &paths.base}, {"LOCAL", &paths.local}, {"REMOTE", &paths.remote}, {"MERGED", &paths.merged}};
    bool any = false;
    for (auto& word : words) {
        for (const auto& [name, value] : names) {
            any = replace_all(word, std::string("${") + name + "}", *value) || any;
            any = replace_all(word, std::string("$") + name, *value) || any;
        }
    }
    if (!any) {
        for (const auto& entry : names) {
            words.push_back(*entry.second);
        }
    }
    return words;
}

std::string quote_argument(const std::string& arg) {
    if (!arg.empty() && arg.find_first_of(" \t\n'\"\\$`*?;&|<>()") == std::string::npos) {
        return arg;
    }
    std::string out = "'";
    for (char c : arg) {
        out += c == '\'' ? std::string("'\\''") : std::string(1, c);
    }
    return out + "'";
}

std::string command_line(const std::vector<std::string>& argv) {
    std::string out;
    for (const auto& arg : argv) {
        out += (out.empty() ? "" : " ") + quote_argument(arg);
    }
    return out;
}

bool has_conflict_markers(const std::string& text) {
    for (std::size_t at = 0; at < text.size();) {
        if (text.compare(at, 7, "<<<<<<<") == 0 || text.compare(at, 7, ">>>>>>>") == 0) {
            return true;
        }
        auto nl = text.find('\n', at);
        if (nl == std::string::npos) {
            break;
        }
        at = nl + 1;
    }
    return false;
}

}  // namespace nipa::conflicts
