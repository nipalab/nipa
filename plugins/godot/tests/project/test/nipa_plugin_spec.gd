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
