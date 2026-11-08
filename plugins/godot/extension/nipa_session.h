#pragma once

#include <cstdint>
#include <map>
#include <memory>
#include <optional>
#include <string>
#include <vector>

#include <godot_cpp/classes/object.hpp>
#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/variant/array.hpp>
#include <godot_cpp/variant/packed_string_array.hpp>
#include <godot_cpp/variant/string.hpp>

#include "core/paths/project_paths.h"
#include "core/session/session.h"
#include "nipa_request.h"

namespace nipa::glue {

// Engine singleton "Nipa". Owns the core session for the open clone, drains its
// event queue on the main thread (process_frame) and emits the signals the
// GDScript add-on listens to.
class NipaSession : public godot::Object {
    GDCLASS(NipaSession, godot::Object)

    std::unique_ptr<session::Session> session_;
    std::optional<paths::ProjectPaths> paths_;
    std::map<std::int64_t, godot::Ref<NipaRequest>> requests_;
    godot::Callable drain_callable_;

protected:
    static void _bind_methods();

public:
    NipaSession() = default;
    ~NipaSession() override;

    bool open_project(const godot::String& project_dir, const godot::String& binary_path, int poll_seconds);
    void close_project();
    bool is_open() const { return session_ != nullptr; }
    godot::String get_clone_root() const;
    godot::String get_project_prefix() const;
    godot::String to_clone_path(const godot::String& project_path) const;
    godot::String to_project_path(const godot::String& clone_path) const;
    bool status_blocking(int timeout_ms);
    bool is_connected() const;
    godot::String get_version() const;
    godot::Array get_status_entries() const;
    godot::String get_branch_name() const;
    godot::PackedStringArray get_branch_names() const;
    godot::PackedStringArray get_remote_names() const;
    godot::Array get_commits(int max_commits) const;
    godot::PackedStringArray get_outside_project_staged() const;

    godot::Ref<NipaRequest> request_stage(const godot::PackedStringArray& add, const godot::PackedStringArray& unstage);
    godot::Ref<NipaRequest> request_restore(const godot::PackedStringArray& paths);
    godot::Ref<NipaRequest> request_push(const godot::String& message);
    godot::Ref<NipaRequest> request_update();
    godot::Ref<NipaRequest> request_switch(const godot::String& branch);
    godot::Ref<NipaRequest> request_branch_create(const godot::String& name, const godot::String& from_branch);
    godot::Ref<NipaRequest> request_branch_delete(const godot::String& name);
    godot::Ref<NipaRequest> request_sign_in(const godot::String& server, const godot::String& username,
                                            const godot::String& password, const godot::String& key_path,
                                            const godot::String& passphrase);
    godot::Ref<NipaRequest> request_refresh();

    void drain_events();

    session::Session* session() const { return session_.get(); }
    const paths::ProjectPaths* project_paths() const { return paths_ ? &*paths_ : nullptr; }
    godot::PackedStringArray expand_sidecars(const godot::PackedStringArray& clone_paths) const;

private:
    godot::Ref<NipaRequest> track(std::int64_t id);
    void dispatch(const session::Event& event);
};

}  // namespace nipa::glue
