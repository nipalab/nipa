@tool
extends EditorPlugin

const DOCK_SCENE := preload("res://addons/nipa/ui/dock.tscn")
const RECONNECT_ITEM := "Nipa: Reconnect"

var dock: Control
var _op_kinds := {}
var _connected_session: Object


func _enter_tree() -> void:
	NipaSettings.register()
	dock = DOCK_SCENE.instantiate()
	add_control_to_dock(DOCK_SLOT_RIGHT_UL, dock)
	dock.reconnect_requested.connect(reconnect)
	add_tool_menu_item(RECONNECT_ITEM, reconnect)
	var session := _session()
	if session:
		_connected_session = session
		session.op_queued.connect(_on_op_queued)
		session.op_finished.connect(_on_op_finished)
	_connect_open_project()


func _exit_tree() -> void:
	if _connected_session:
		if _connected_session.op_queued.is_connected(_on_op_queued):
			_connected_session.op_queued.disconnect(_on_op_queued)
		if _connected_session.op_finished.is_connected(_on_op_finished):
			_connected_session.op_finished.disconnect(_on_op_finished)
		_connected_session = null
	remove_tool_menu_item(RECONNECT_ITEM)
	if dock:
		dock.reconnect_requested.disconnect(reconnect)
		remove_control_from_docks(dock)
		dock.queue_free()
		dock = null
	_op_kinds.clear()


func reconnect() -> void:
	var session := _session()
	if session == null:
		return
	session.close_project()
	_connect_open_project()


func _connect_open_project() -> void:
	var session := _session()
	if session == null or session.is_open():
		return
	var root := ProjectSettings.globalize_path("res://")
	session.open_project(root.trim_suffix("/"), NipaSettings.binary_path(), NipaSettings.poll_interval())


func _session() -> Object:
	return Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null


func _on_op_queued(id: int, kind: String) -> void:
	_op_kinds[id] = kind


func _on_op_finished(id: int, ok: bool, message: String, changed_paths: PackedStringArray) -> void:
	var kind: String = _op_kinds.get(id, "operation")
	_op_kinds.erase(id)
	EditorInterface.get_resource_filesystem().scan()
	_reload_changed_scenes(changed_paths)
	var severity := EditorToaster.SEVERITY_INFO if ok else EditorToaster.SEVERITY_ERROR
	var text := "Nipa %s: %s" % [kind, message if not message.is_empty() else ("done" if ok else "failed")]
	EditorInterface.get_editor_toaster().push_toast(text, severity)


func _reload_changed_scenes(changed_paths: PackedStringArray) -> void:
	var session := _session()
	if session == null:
		return
	var open_scenes := EditorInterface.get_open_scenes()
	for clone_path in changed_paths:
		var project_path: String = session.to_project_path(clone_path)
		if project_path.is_empty():
			continue
		var res := "res://" + project_path
		if res.get_extension() in ["tscn", "scn"] and res in open_scenes:
			EditorInterface.reload_scene_from_path(res)
