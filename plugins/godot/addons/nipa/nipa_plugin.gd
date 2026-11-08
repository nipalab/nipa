@tool
extends EditorPlugin

const DOCK_SCENE := preload("res://addons/nipa/ui/dock.tscn")
const RECONNECT_ITEM := "Nipa: Reconnect"

var dock: Control
var _op_kinds := {}
var _scene_hashes := {}
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
	scene_saved.connect(_remember_scene)
	scene_changed.connect(_on_scene_changed)
	_remember_open_scenes()
	_connect_open_project()


func _exit_tree() -> void:
	if scene_saved.is_connected(_remember_scene):
		scene_saved.disconnect(_remember_scene)
	if scene_changed.is_connected(_on_scene_changed):
		scene_changed.disconnect(_on_scene_changed)
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
	_scene_hashes.clear()


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


# The on-disk content of each open scene as the editor last loaded or saved it.
func _remember_scene(path: String) -> void:
	if FileAccess.file_exists(path):
		_scene_hashes[path] = FileAccess.get_md5(path)


func _remember_open_scenes() -> void:
	for scene in EditorInterface.get_open_scenes():
		if not _scene_hashes.has(scene):
			_remember_scene(scene)


func _on_scene_changed(_scene_root: Node) -> void:
	_remember_open_scenes()


# Ops report no per-file list, so an open scene counts as changed when its file
# no longer matches what the editor loaded or saved (or the op named it).
func _reload_changed_scenes(changed_paths: PackedStringArray) -> void:
	var session := _session()
	var named := {}
	if session:
		for clone_path in changed_paths:
			var project_path: String = session.to_project_path(clone_path)
			if not project_path.is_empty():
				named["res://" + project_path] = true
	for scene in EditorInterface.get_open_scenes():
		if not FileAccess.file_exists(scene):
			continue
		var current := FileAccess.get_md5(scene)
		if named.has(scene) or (_scene_hashes.has(scene) and _scene_hashes[scene] != current):
			EditorInterface.reload_scene_from_path(scene)
		_scene_hashes[scene] = current
