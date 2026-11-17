extends GdUnitTestSuite

# Runs without the editor: the native extension is editor-only, so the
# native cases skip here and are covered by check_load.gd in editor mode.


func test_settings_clamp_the_poll_interval() -> void:
	ProjectSettings.set_setting(NipaSettings.POLL_SETTING, 5)
	assert_int(NipaSettings.poll_interval()).is_equal(15)
	ProjectSettings.set_setting(NipaSettings.POLL_SETTING, 100000)
	assert_int(NipaSettings.poll_interval()).is_equal(3600)
	ProjectSettings.set_setting(NipaSettings.POLL_SETTING, 60)
	assert_int(NipaSettings.poll_interval()).is_equal(60)


func test_dock_builds_from_its_scene_and_frees_cleanly() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	assert_object(dock.get_node("Header/StateLabel")).is_not_null()
	assert_bool(dock.get_node("OutOfDate").visible).is_false()
	assert_bool(dock.get_node("Header/ReconnectButton").disabled).is_true()
	remove_child(dock)
	dock.free()


func test_native_surface_is_registered_when_the_editor_loads_it() -> void:
	if not Engine.has_singleton("Nipa"):
		push_warning("native Nipa extension is editor-only; skipped (see check_load.gd)")
		return
	assert_bool(ClassDB.class_exists("NipaVCSInterface")).is_true()
	var session = Engine.get_singleton("Nipa")
	assert_bool(session.open_project("/nonexistent/project", "", 60)).is_false()
	assert_bool(session.is_open()).is_false()
	assert_bool(session.request_update().is_done()).is_true()


func test_dock_keeps_the_op_label_until_the_last_operation_finishes() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	var label: Label = dock.get_node("OpLabel")
	dock._on_op_queued(1, "push")
	dock._on_op_queued(2, "lock")
	dock._on_op_queued(3, "history")
	assert_bool(label.visible).is_true()
	dock._on_op_finished(1, true, "c9", PackedStringArray())
	assert_bool(label.visible).is_true()
	assert_str(label.text).is_equal("Queued: lock")
	dock._on_op_progress(3, "loading", 1, 2)
	assert_str(label.text).is_equal("Queued: lock")
	dock._on_op_finished(2, true, "", PackedStringArray())
	assert_bool(label.visible).is_false()
	remove_child(dock)
	dock.free()


func test_dock_hides_the_out_of_date_banner_after_a_successful_update() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	var banner: Control = dock.get_node("OutOfDate")
	dock._on_activity_summary(2, 0, "")
	assert_bool(banner.visible).is_true()
	dock._on_op_queued(1, "update")
	dock._on_op_finished(1, false, "offline", PackedStringArray())
	assert_bool(banner.visible).is_true()
	dock._on_op_queued(2, "push")
	dock._on_op_finished(2, true, "c9", PackedStringArray())
	assert_bool(banner.visible).is_true()
	dock._on_op_queued(3, "update")
	dock._on_op_finished(3, true, "c10", PackedStringArray())
	assert_bool(banner.visible).is_false()
	remove_child(dock)
	dock.free()


func test_vcs_refresh_only_targets_the_nipa_interface() -> void:
	var plugin_script: GDScript = load("res://addons/nipa/nipa_plugin.gd")
	var previous = ProjectSettings.get_setting("editor/version_control/plugin_name", "")
	ProjectSettings.set_setting("editor/version_control/plugin_name", "GitPlugin")
	assert_bool(plugin_script.vcs_active()).is_false()
	ProjectSettings.set_setting("editor/version_control/plugin_name", "NipaVCSInterface")
	assert_bool(plugin_script.vcs_active()).is_true()
	ProjectSettings.set_setting("editor/version_control/plugin_name", previous)


func test_history_reloads_only_after_it_was_shown_and_while_visible() -> void:
	var panel: Control = load("res://addons/nipa/ui/bottom_panel.tscn").instantiate()
	add_child(panel)
	var history: Control = panel.history_tab
	history._on_commits_changed()
	assert_bool(history._stale).is_false()
	history._shown = true
	panel.current_tab = panel.conflicts_tab.get_index()
	history._on_commits_changed()
	assert_bool(history._stale).is_true()
	panel.current_tab = history.get_index()
	assert_bool(history._stale).is_false()
	assert_str(history.get_node("Toolbar/Target").text).is_equal("History: whole project")
	remove_child(panel)
	panel.free()
