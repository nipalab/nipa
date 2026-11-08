#include "core/diff/diff.h"

#include <algorithm>
#include <cstddef>

namespace nipa::diff {
namespace {

enum class OpKind { Equal, Delete, Insert };

struct Op {
    OpKind kind;
    long long a;  // old index for Equal/Delete
    long long b;  // new index for Equal/Insert
};

// Shortest edit script via the classic Myers O(ND) search. Each step stores the
// frontier so the path can be walked back from the end.
std::vector<Op> myers(const std::vector<std::string>& a, const std::vector<std::string>& b) {
    const long long n = static_cast<long long>(a.size());
    const long long m = static_cast<long long>(b.size());
    const long long max = n + m;
    const long long offset = max + 1;
    std::vector<long long> v(static_cast<std::size_t>(2 * max + 3), 0);
    std::vector<std::vector<long long>> trace;

    for (long long d = 0; d <= max; ++d) {
        trace.push_back(v);
        for (long long k = -d; k <= d; k += 2) {
            long long x = 0;
            if (k == -d || (k != d && v[static_cast<std::size_t>(offset + k - 1)] <
                                          v[static_cast<std::size_t>(offset + k + 1)])) {
                x = v[static_cast<std::size_t>(offset + k + 1)];
            } else {
                x = v[static_cast<std::size_t>(offset + k - 1)] + 1;
            }
            long long y = x - k;
            while (x < n && y < m && a[static_cast<std::size_t>(x)] == b[static_cast<std::size_t>(y)]) {
                ++x;
                ++y;
            }
            v[static_cast<std::size_t>(offset + k)] = x;
            if (x >= n && y >= m) {
                std::vector<Op> ops;
                long long cx = n;
                long long cy = m;
                for (long long back = d; back >= 0; --back) {
                    const auto& frontier = trace[static_cast<std::size_t>(back)];
                    long long ck = cx - cy;
                    long long prev_k;
                    if (ck == -back ||
                        (ck != back && frontier[static_cast<std::size_t>(offset + ck - 1)] <
                                           frontier[static_cast<std::size_t>(offset + ck + 1)])) {
                        prev_k = ck + 1;
                    } else {
                        prev_k = ck - 1;
                    }
                    long long prev_x = back == 0 ? 0 : frontier[static_cast<std::size_t>(offset + prev_k)];
                    long long prev_y = prev_x - prev_k;
                    long long mid_x = prev_k == ck + 1 ? prev_x : prev_x + 1;
                    long long mid_y = mid_x - ck;
                    while (cx > mid_x && cy > mid_y) {
                        ops.push_back({OpKind::Equal, cx - 1, cy - 1});
                        --cx;
                        --cy;
                    }
                    if (back > 0) {
                        if (prev_k == ck + 1) {
                            ops.push_back({OpKind::Insert, -1, prev_y});
                        } else {
                            ops.push_back({OpKind::Delete, prev_x, -1});
                        }
                    }
                    cx = prev_x;
                    cy = prev_y;
                }
                std::reverse(ops.begin(), ops.end());
                return ops;
            }
        }
    }
    return {};
}

}  // namespace

Hunk placeholder_hunk(const File& file) {
    Hunk hunk;
    Line line;
    line.status = ' ';
    line.content = "Binary " + file.kind + ": " + std::to_string(file.old_size) + " \xE2\x86\x92 " +
                   std::to_string(file.new_size) + ", open in the Nipa dock to compare";
    hunk.lines.push_back(line);
    return hunk;
}

std::vector<std::string> split_lines(const std::string& text) {
    std::vector<std::string> out;
    std::size_t start = 0;
    while (start < text.size()) {
        std::size_t newline = text.find('\n', start);
        std::string line = newline == std::string::npos ? text.substr(start) : text.substr(start, newline - start);
        if (!line.empty() && line.back() == '\r') {
            line.pop_back();
        }
        out.push_back(line);
        if (newline == std::string::npos) {
            break;
        }
        start = newline + 1;
    }
    return out;
}

std::vector<Hunk> line_diff(const std::string& old_text, const std::string& new_text, int context) {
    auto a = split_lines(old_text);
    auto b = split_lines(new_text);
    auto ops = myers(a, b);

    const long long count = static_cast<long long>(ops.size());
    std::vector<std::pair<long long, long long>> ranges;
    for (long long i = 0; i < count; ++i) {
        if (ops[static_cast<std::size_t>(i)].kind == OpKind::Equal) {
            continue;
        }
        long long start = std::max<long long>(0, i - context);
        long long end = std::min<long long>(count, i + context + 1);
        if (!ranges.empty() && start <= ranges.back().second) {
            ranges.back().second = std::max(ranges.back().second, end);
        } else {
            ranges.emplace_back(start, end);
        }
    }

    std::vector<Hunk> hunks;
    long long old_before = 0;
    long long new_before = 0;
    long long cursor = 0;
    for (const auto& range : ranges) {
        for (; cursor < range.first; ++cursor) {
            const Op& op = ops[static_cast<std::size_t>(cursor)];
            if (op.kind != OpKind::Insert) ++old_before;
            if (op.kind != OpKind::Delete) ++new_before;
        }
        Hunk hunk;
        long long old_seen = old_before;
        long long new_seen = new_before;
        for (long long i = range.first; i < range.second; ++i) {
            const Op& op = ops[static_cast<std::size_t>(i)];
            Line line;
            if (op.kind == OpKind::Equal) {
                line.status = ' ';
                line.old_no = op.a + 1;
                line.new_no = op.b + 1;
                line.content = a[static_cast<std::size_t>(op.a)];
                ++hunk.old_lines;
                ++hunk.new_lines;
                ++old_seen;
                ++new_seen;
            } else if (op.kind == OpKind::Delete) {
                line.status = '-';
                line.old_no = op.a + 1;
                line.content = a[static_cast<std::size_t>(op.a)];
                ++hunk.old_lines;
                ++old_seen;
            } else {
                line.status = '+';
                line.new_no = op.b + 1;
                line.content = b[static_cast<std::size_t>(op.b)];
                ++hunk.new_lines;
                ++new_seen;
            }
            hunk.lines.push_back(std::move(line));
        }
        hunk.old_start = hunk.old_lines > 0 ? old_before + 1 : old_before;
        hunk.new_start = hunk.new_lines > 0 ? new_before + 1 : new_before;
        hunks.push_back(std::move(hunk));
        old_before = old_seen;
        new_before = new_seen;
        cursor = range.second;
    }
    return hunks;
}

}  // namespace nipa::diff
