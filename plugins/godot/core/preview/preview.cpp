#include "core/preview/preview.h"

#include "core/daemon/messages.h"

namespace nipa::preview {

bool Assembler::add(const nlohmann::json& event) {
    if (!event.is_object()) {
        return true;
    }
    if (event.contains("failure")) {
        error_ = daemon::json_string(event["failure"], "message");
        failure_code_ = static_cast<int>(daemon::json_int(event["failure"], "code"));
        return false;
    }
    if (event.contains("image") && event["image"].is_object()) {
        const auto& image = event["image"];
        std::string side = daemon::json_string(image, "side");
        summary_[side] = {{"width", daemon::json_int(image, "width")},
                          {"height", daemon::json_int(image, "height")},
                          {"format", daemon::json_string(image, "format")},
                          {"size_bytes", daemon::json_int(image, "size_bytes")},
                          {"preview_width", daemon::json_int(image, "preview_width")},
                          {"preview_height", daemon::json_int(image, "preview_height")},
                          {"hdr", image.value("hdr", false)}};
    } else if (event.contains("data") && event["data"].is_object()) {
        const auto& data = event["data"];
        auto& target = daemon::json_string(data, "side") == "old" ? old_png_ : new_png_;
        target += daemon::base64_decode(daemon::json_string(data, "png"));
    } else if (event.contains("diff") && event["diff"].is_object()) {
        const auto& d = event["diff"];
        summary_["diff"] = {{"changed_pixels", daemon::json_int(d, "changed_pixels")},
                            {"total_pixels", daemon::json_int(d, "total_pixels")},
                            {"changed_ratio", d.value("changed_ratio", 0.0)},
                            {"max_delta", daemon::json_int(d, "max_delta")},
                            {"size_changed", d.value("size_changed", false)}};
    }
    return true;
}

const std::string& Assembler::png(const std::string& side) const {
    return side == "old" ? old_png_ : new_png_;
}

nlohmann::json request(const std::string& root, const nlohmann::json& old_ref, const nlohmann::json& new_ref,
                       int threshold, int max_size) {
    nlohmann::json body = {{"root", root}, {"threshold", threshold}, {"max_size", max_size}};
    if (!old_ref.is_null()) {
        body["old"] = old_ref;
    }
    if (!new_ref.is_null()) {
        body["new"] = new_ref;
    }
    return body;
}

}  // namespace nipa::preview
