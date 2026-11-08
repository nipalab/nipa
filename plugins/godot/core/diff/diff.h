#pragma once

#include <string>
#include <vector>

namespace nipa::diff {

struct Line {
    char status = ' ';  // '+', '-' or ' '
    long long old_no = -1;
    long long new_no = -1;
    std::string content;
};

struct Hunk {
    long long old_start = 0;
    long long old_lines = 0;
    long long new_start = 0;
    long long new_lines = 0;
    std::vector<Line> lines;
};

struct File {
    std::string old_path;
    std::string new_path;
    std::string status;
    std::string kind;
    long long old_size = 0;
    long long new_size = 0;
    long long additions = 0;
    long long deletions = 0;
    bool omitted = false;  // hunks omitted by the daemon or content unavailable
    std::vector<Hunk> hunks;

    bool placeholder() const { return omitted || kind != "text"; }
};

// One binary or omitted file becomes a single synthetic hunk that names the sizes.
Hunk placeholder_hunk(const File& file);

// Splits on '\n' and drops a trailing '\r' per line. A final line without a newline counts.
std::vector<std::string> split_lines(const std::string& text);

// Myers line diff grouped into hunks with `context` unchanged lines around each change.
std::vector<Hunk> line_diff(const std::string& old_text, const std::string& new_text, int context = 3);

}  // namespace nipa::diff
