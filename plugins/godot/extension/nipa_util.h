#pragma once

#include <string>

#include <godot_cpp/variant/string.hpp>

namespace nipa::glue {

inline godot::String to_godot(const std::string& text) {
    return godot::String::utf8(text.c_str(), static_cast<int64_t>(text.size()));
}

inline std::string from_godot(const godot::String& text) {
    auto bytes = text.utf8();
    return std::string(bytes.get_data(), static_cast<std::size_t>(bytes.size()));
}

}  // namespace nipa::glue
