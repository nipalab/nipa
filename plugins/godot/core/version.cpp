#include "core/version.h"

#include <algorithm>
#include <cctype>
#include <optional>
#include <vector>

namespace nipa {
namespace {

std::optional<std::vector<long>> parse_numbers(const std::string& raw) {
    std::string text = raw;
    if (!text.empty() && (text[0] == 'v' || text[0] == 'V')) {
        text.erase(0, 1);
    }
    std::vector<long> parts;
    std::size_t i = 0;
    while (i < text.size() && std::isdigit(static_cast<unsigned char>(text[i]))) {
        long value = 0;
        while (i < text.size() && std::isdigit(static_cast<unsigned char>(text[i]))) {
            value = value * 10 + (text[i] - '0');
            ++i;
        }
        parts.push_back(value);
        if (i < text.size() && text[i] == '.') {
            ++i;
            continue;
        }
        break;
    }
    if (parts.empty()) {
        return std::nullopt;
    }
    return parts;
}

}  // namespace

bool version_older(const std::string& version, const std::string& minimum) {
    auto minimum_parts = parse_numbers(minimum);
    if (!minimum_parts) {
        return false;
    }
    auto version_parts = parse_numbers(version);
    if (!version_parts) {
        return false;
    }
    std::size_t width = std::max(version_parts->size(), minimum_parts->size());
    for (std::size_t i = 0; i < width; ++i) {
        long have = i < version_parts->size() ? (*version_parts)[i] : 0;
        long want = i < minimum_parts->size() ? (*minimum_parts)[i] : 0;
        if (have != want) {
            return have < want;
        }
    }
    return false;
}

}  // namespace nipa
