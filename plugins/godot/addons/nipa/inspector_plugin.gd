@tool
extends EditorInspectorPlugin

const BADGE_SCENE := preload("res://addons/nipa/ui/lock_badge.tscn")


static func file_of(object: Object) -> String:
	if object is Resource:
		var path: String = object.resource_path
		return path if path.begins_with("res://") and not path.contains("::") else ""
	if object is Node:
		var root := EditorInterface.get_edited_scene_root()
		return root.scene_file_path if root else ""
	return ""


func _can_handle(object: Object) -> bool:
	return Engine.has_singleton("Nipa") and Engine.get_singleton("Nipa").is_open() and not file_of(object).is_empty()


func _parse_begin(object: Object) -> void:
	var badge := BADGE_SCENE.instantiate()
	add_custom_control(badge)
	badge.set_res_path(file_of(object))
