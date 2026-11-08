#pragma once

#include <godot_cpp/classes/ref_counted.hpp>
#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/variant/string.hpp>

namespace nipa::glue {

// Handle for one queued operation; `await request.completed` resumes once it finishes.
class NipaRequest : public godot::RefCounted {
    GDCLASS(NipaRequest, godot::RefCounted)

    bool done_ = false;
    bool ok_ = false;
    godot::String message_;

protected:
    static void _bind_methods();

public:
    void complete(bool ok, const godot::String& message);
    bool is_done() const { return done_; }
    bool is_ok() const { return ok_; }
    godot::String get_message() const { return message_; }
};

}  // namespace nipa::glue
