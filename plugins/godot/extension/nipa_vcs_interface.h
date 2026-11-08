#pragma once

#include <godot_cpp/classes/editor_vcs_interface.hpp>
#include <godot_cpp/core/class_db.hpp>
#include <godot_cpp/variant/typed_array.hpp>

#include "nipa_session.h"

namespace nipa::glue {

// Native VCS plugin listed by Godot's Version Control settings. Read virtuals
// answer from the session caches; write virtuals queue operations.
class NipaVCSInterface : public godot::EditorVCSInterface {
    GDCLASS(NipaVCSInterface, godot::EditorVCSInterface)

    NipaSession* session_node() const;

protected:
    static void _bind_methods() {}

public:
    bool _initialize(const godot::String& p_project_path) override;
    void _set_credentials(const godot::String& p_username, const godot::String& p_password,
                          const godot::String& p_ssh_public_key_path, const godot::String& p_ssh_private_key_path,
                          const godot::String& p_ssh_passphrase) override;
    godot::TypedArray<godot::Dictionary> _get_modified_files_data() override;
    void _stage_file(const godot::String& p_file_path) override;
    void _unstage_file(const godot::String& p_file_path) override;
    void _discard_file(const godot::String& p_file_path) override;
    void _commit(const godot::String& p_msg) override;
    godot::TypedArray<godot::Dictionary> _get_diff(const godot::String& p_identifier, int32_t p_area) override;
    bool _shut_down() override;
    godot::String _get_vcs_name() override;
    godot::TypedArray<godot::Dictionary> _get_previous_commits(int32_t p_max_commits) override;
    godot::TypedArray<godot::String> _get_branch_list() override;
    godot::TypedArray<godot::String> _get_remotes() override;
    void _create_branch(const godot::String& p_branch_name) override;
    void _remove_branch(const godot::String& p_branch_name) override;
    void _create_remote(const godot::String& p_remote_name, const godot::String& p_remote_url) override;
    void _remove_remote(const godot::String& p_remote_name) override;
    godot::String _get_current_branch_name() override;
    bool _checkout_branch(const godot::String& p_branch_name) override;
    void _pull(const godot::String& p_remote) override;
    void _push(const godot::String& p_remote, bool p_force) override;
    void _fetch(const godot::String& p_remote) override;
    godot::TypedArray<godot::Dictionary> _get_line_diff(const godot::String& p_file_path,
                                                        const godot::String& p_text) override;
};

}  // namespace nipa::glue
