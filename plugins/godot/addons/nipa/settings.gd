@tool
class_name NipaSettings
extends RefCounted

const POLL_SETTING := "nipa/poll_interval_sec"
const BINARY_SETTING := "nipa/binary_path"
const AUTO_LOCK_SETTING := "nipa/locking/auto_lock_on_edit"
const STRICT_SETTING := "nipa/locking/strict_read_only"
const EXTENSIONS_SETTING := "nipa/locking/extensions"
const DEFAULT_EXTENSIONS: PackedStringArray = [
	"tscn", "tres", "scn", "res", "glb", "gltf", "blend", "fbx", "png", "jpg", "exr", "wav", "ogg",
]


static func register() -> void:
	_register_project_setting(POLL_SETTING, 60, {
		"type": TYPE_INT,
		"hint": PROPERTY_HINT_RANGE,
		"hint_string": "15,3600,1",
	})
	_register_project_setting(AUTO_LOCK_SETTING, true, {"type": TYPE_BOOL})
	_register_project_setting(STRICT_SETTING, false, {"type": TYPE_BOOL})
	_register_project_setting(EXTENSIONS_SETTING, DEFAULT_EXTENSIONS, {"type": TYPE_PACKED_STRING_ARRAY})
	var editor := EditorInterface.get_editor_settings()
	if not editor.has_setting(BINARY_SETTING):
		editor.set_setting(BINARY_SETTING, "")
	editor.add_property_info({"name": BINARY_SETTING, "type": TYPE_STRING, "hint": PROPERTY_HINT_GLOBAL_FILE})
	editor.set_initial_value(BINARY_SETTING, "", false)


static func poll_interval() -> int:
	return clampi(ProjectSettings.get_setting(POLL_SETTING, 60), 15, 3600)


static func binary_path() -> String:
	return EditorInterface.get_editor_settings().get_setting(BINARY_SETTING)


static func auto_lock() -> bool:
	return ProjectSettings.get_setting(AUTO_LOCK_SETTING, true)


static func strict_read_only() -> bool:
	return ProjectSettings.get_setting(STRICT_SETTING, false)


static func lock_extensions() -> PackedStringArray:
	return PackedStringArray(ProjectSettings.get_setting(EXTENSIONS_SETTING, DEFAULT_EXTENSIONS))


static func _register_project_setting(setting: String, default: Variant, info: Dictionary) -> void:
	if not ProjectSettings.has_setting(setting):
		ProjectSettings.set_setting(setting, default)
	ProjectSettings.set_initial_value(setting, default)
	var property := info.duplicate()
	property["name"] = setting
	ProjectSettings.add_property_info(property)
