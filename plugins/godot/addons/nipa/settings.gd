@tool
class_name NipaSettings
extends RefCounted

const POLL_SETTING := "nipa/poll_interval_sec"
const BINARY_SETTING := "nipa/binary_path"


static func register() -> void:
	_register_project_setting(POLL_SETTING, 60, {
		"type": TYPE_INT,
		"hint": PROPERTY_HINT_RANGE,
		"hint_string": "15,3600,1",
	})
	var editor := EditorInterface.get_editor_settings()
	if not editor.has_setting(BINARY_SETTING):
		editor.set_setting(BINARY_SETTING, "")
	editor.add_property_info({"name": BINARY_SETTING, "type": TYPE_STRING, "hint": PROPERTY_HINT_GLOBAL_FILE})
	editor.set_initial_value(BINARY_SETTING, "", false)


static func poll_interval() -> int:
	return clampi(ProjectSettings.get_setting(POLL_SETTING, 60), 15, 3600)


static func binary_path() -> String:
	return EditorInterface.get_editor_settings().get_setting(BINARY_SETTING)


static func _register_project_setting(setting: String, default: Variant, info: Dictionary) -> void:
	if not ProjectSettings.has_setting(setting):
		ProjectSettings.set_setting(setting, default)
	ProjectSettings.set_initial_value(setting, default)
	var property := info.duplicate()
	property["name"] = setting
	ProjectSettings.add_property_info(property)
