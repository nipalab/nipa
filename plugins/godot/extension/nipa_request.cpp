#include "nipa_request.h"

namespace nipa::glue {

void NipaRequest::_bind_methods() {
    godot::ClassDB::bind_method(godot::D_METHOD("is_done"), &NipaRequest::is_done);
    godot::ClassDB::bind_method(godot::D_METHOD("is_ok"), &NipaRequest::is_ok);
    godot::ClassDB::bind_method(godot::D_METHOD("get_message"), &NipaRequest::get_message);
    godot::ClassDB::bind_method(godot::D_METHOD("get_result"), &NipaRequest::get_result);
    ADD_SIGNAL(godot::MethodInfo("completed", godot::PropertyInfo(godot::Variant::BOOL, "ok"),
                                 godot::PropertyInfo(godot::Variant::STRING, "message")));
}

void NipaRequest::complete(bool ok, const godot::String& message, const godot::Dictionary& result) {
    if (done_) {
        return;
    }
    done_ = true;
    ok_ = ok;
    message_ = message;
    result_ = result;
    emit_signal("completed", ok, message);
}

}  // namespace nipa::glue
