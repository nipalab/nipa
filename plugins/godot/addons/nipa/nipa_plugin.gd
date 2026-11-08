@tool
extends EditorPlugin

const DOCK_SCENE := preload("res://addons/nipa/ui/dock.tscn")
const BOTTOM_PANEL_SCENE := preload("res://addons/nipa/ui/bottom_panel.tscn")
const BADGE_SCENE := preload("res://addons/nipa/ui/lock_badge.tscn")
const ContextMenu := preload("res://addons/nipa/context_menu.gd")
const InspectorPlugin := preload("res://addons/nipa/inspector_plugin.gd")
const RECONNECT_ITEM := "Nipa: Reconnect"
const QUIET_OPS := ["lock", "unlock", "conflicts", "merge-godot", "merge-tool", "history", "preview"]
const SILENT_OPS := ["conflicts", "merge-godot", "merge-tool", "history", "preview"]

var dock: Control
var bottom_panel: Control
var _op_kinds := {}
var _scene_hashes := {}
var _connected_session: Object
var _context_menus: Array[EditorContextMenuPlugin] = []
var _inspector_plugin: EditorInspectorPlugin
var _badges: Array = []
var _lock_timer: Timer
var _lock_requested := {}
var _warned := {}


# What an edit of a file does: "lock" it, "warn" that someone else holds it, or nothing.
static func edit_action(info: Dictionary, auto_lock: bool) -> String:
	if not info.get("relevant", false):
		return ""
	match info.get("state", "free"):
		"theirs":
			return "warn"
		"free":
			return "lock" if auto_lock else ""
	return ""


func _enter_tree() -> void:
	NipaSettings.register()
	dock = DOCK_SCENE.instantiate()
	add_control_to_dock(DOCK_SLOT_RIGHT_UL, dock)
	dock.reconnect_requested.connect(reconnect)
	bottom_panel = BOTTOM_PANEL_SCENE.instantiate()
	add_control_to_bottom_panel(bottom_panel, "Nipa")
	dock.conflicts_requested.connect(_show_conflicts)
	add_tool_menu_item(RECONNECT_ITEM, reconnect)
	for slot in [EditorContextMenuPlugin.CONTEXT_SLOT_FILESYSTEM, EditorContextMenuPlugin.CONTEXT_SLOT_SCENE_TABS]:
		var menu := ContextMenu.new()
		menu.slot = slot
		menu.history_requested.connect(_show_history)
		menu.compare_requested.connect(_show_image_diff)
		add_context_menu_plugin(slot, menu)
		_context_menus.append(menu)
	_inspector_plugin = InspectorPlugin.new()
	add_inspector_plugin(_inspector_plugin)
	for container in [CONTAINER_CANVAS_EDITOR_MENU, CONTAINER_SPATIAL_EDITOR_MENU]:
		var badge: Control = BADGE_SCENE.instantiate()
		add_control_to_container(container, badge)
		_badges.append([container, badge])
	_lock_timer = Timer.new()
	_lock_timer.wait_time = NipaSettings.poll_interval()
	_lock_timer.timeout.connect(_on_lock_timer)
	add_child(_lock_timer)
	_lock_timer.start()

	var session := _session()
	if session:
		_connected_session = session
		session.op_queued.connect(_on_op_queued)
		session.op_finished.connect(_on_op_finished)
		session.locks_changed.connect(_on_locks_changed)
	scene_saved.connect(_on_scene_saved)
	scene_changed.connect(_on_scene_changed)
	resource_saved.connect(_on_resource_saved)
	get_undo_redo().history_changed.connect(_on_history_changed)
	EditorInterface.get_inspector().property_edited.connect(_on_property_edited)
	ProjectSettings.settings_changed.connect(_apply_lock_settings)
	_remember_open_scenes()
	_connect_open_project()
	_apply_lock_settings()
	_refresh_badges()


func _exit_tree() -> void:
	for pair in [
		[scene_saved, _on_scene_saved],
		[scene_changed, _on_scene_changed],
		[resource_saved, _on_resource_saved],
		[get_undo_redo().history_changed, _on_history_changed],
		[EditorInterface.get_inspector().property_edited, _on_property_edited],
		[ProjectSettings.settings_changed, _apply_lock_settings],
	]:
		if pair[0].is_connected(pair[1]):
			pair[0].disconnect(pair[1])
	if _connected_session:
		for pair in [
			[_connected_session.op_queued, _on_op_queued],
			[_connected_session.op_finished, _on_op_finished],
			[_connected_session.locks_changed, _on_locks_changed],
		]:
			if pair[0].is_connected(pair[1]):
				pair[0].disconnect(pair[1])
		_connected_session = null
	if _lock_timer:
		_lock_timer.stop()
		remove_child(_lock_timer)
		_lock_timer.queue_free()
		_lock_timer = null
	for pair in _badges:
		remove_control_from_container(pair[0], pair[1])
		pair[1].queue_free()
	_badges.clear()
	if _inspector_plugin:
		remove_inspector_plugin(_inspector_plugin)
		_inspector_plugin = null
	for menu in _context_menus:
		menu.history_requested.disconnect(_show_history)
		menu.compare_requested.disconnect(_show_image_diff)
		remove_context_menu_plugin(menu)
	_context_menus.clear()
	remove_tool_menu_item(RECONNECT_ITEM)
	if bottom_panel:
		remove_control_from_bottom_panel(bottom_panel)
		bottom_panel.queue_free()
		bottom_panel = null
	if dock:
		dock.reconnect_requested.disconnect(reconnect)
		dock.conflicts_requested.disconnect(_show_conflicts)
		remove_control_from_docks(dock)
		dock.queue_free()
		dock = null
	_op_kinds.clear()
	_scene_hashes.clear()
	_lock_requested.clear()
	_warned.clear()


func _notification(what: int) -> void:
	if what == NOTIFICATION_APPLICATION_FOCUS_IN:
		var session := _session()
		if session and session.is_open():
			session.request_lock_refresh()


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


func _apply_lock_settings() -> void:
	var session := _session()
	if session:
		session.configure_locks(NipaSettings.lock_extensions(), NipaSettings.strict_read_only())
	if _lock_timer:
		_lock_timer.wait_time = NipaSettings.poll_interval()


func _session() -> Object:
	return Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null


func _on_op_queued(id: int, kind: String) -> void:
	_op_kinds[id] = kind


func _on_op_finished(id: int, ok: bool, message: String, changed_paths: PackedStringArray) -> void:
	var kind: String = _op_kinds.get(id, "operation")
	_op_kinds.erase(id)
	if SILENT_OPS.has(kind):
		return
	if not QUIET_OPS.has(kind):
		EditorInterface.get_resource_filesystem().scan()
		_reload_changed_scenes(changed_paths)
	var severity := EditorToaster.SEVERITY_INFO if ok else EditorToaster.SEVERITY_ERROR
	var text := "Nipa %s: %s" % [kind, message if not message.is_empty() else ("done" if ok else "failed")]
	EditorInterface.get_editor_toaster().push_toast(text, severity)


func _show_conflicts() -> void:
	if bottom_panel:
		make_bottom_panel_item_visible(bottom_panel)
		bottom_panel.show_conflicts()


func _show_history(clone_path: String) -> void:
	if bottom_panel:
		make_bottom_panel_item_visible(bottom_panel)
		bottom_panel.show_history(clone_path)


func _show_image_diff(old_ref: Dictionary, new_ref: Dictionary, title: String) -> void:
	if bottom_panel:
		make_bottom_panel_item_visible(bottom_panel)
		bottom_panel.show_image_diff(old_ref, new_ref, title)


func _on_lock_timer() -> void:
	var session := _session()
	if session and session.is_open():
		session.request_lock_refresh()


func _on_locks_changed(_paths: PackedStringArray) -> void:
	_lock_requested.clear()
	var session := _session()
	for clone_path in _warned.keys():
		if session == null or session.get_lock_info(clone_path).get("state", "free") != "theirs":
			_warned.erase(clone_path)
	_refresh_badges()


func _edited_scene_path() -> String:
	var root := EditorInterface.get_edited_scene_root()
	return root.scene_file_path if root else ""


func _refresh_badges() -> void:
	var path := _edited_scene_path()
	for pair in _badges:
		pair[1].set_res_path(path)


# Fires on commits and on cleared histories (opening a scene), so only a scene
# with something to undo counts as edited.
func _on_history_changed() -> void:
	var root := EditorInterface.get_edited_scene_root()
	if root == null:
		return
	var manager := get_undo_redo()
	var history := manager.get_history_undo_redo(manager.get_object_history_id(root))
	if history and history.has_undo():
		_on_file_edited(root.scene_file_path)


func _on_property_edited(_property: String) -> void:
	var edited := EditorInterface.get_inspector().get_edited_object()
	var path := InspectorPlugin.file_of(edited) if edited else ""
	_on_file_edited(path)


func _on_file_edited(res_path: String) -> void:
	var session := _session()
	if res_path.is_empty() or session == null or not session.is_open():
		return
	var clone_path: String = session.to_clone_path(res_path)
	var info: Dictionary = session.get_lock_info(clone_path)
	match edit_action(info, NipaSettings.auto_lock()):
		"lock":
			if not _lock_requested.has(clone_path):
				_lock_requested[clone_path] = true
				session.request_lock(PackedStringArray([clone_path]))
		"warn":
			if not _warned.has(clone_path):
				_warned[clone_path] = true
				var holder: String = info.get("holder", "")
				EditorInterface.get_editor_toaster().push_toast(
					"%s is locked by %s: changes cannot be pushed." % [res_path.get_file(), holder],
					EditorToaster.SEVERITY_WARNING)
				if res_path.get_extension() in ["tscn", "scn"] and dock:
					dock.offer_reload(res_path, holder)


func _check_saved(res_path: String) -> void:
	var session := _session()
	if res_path.is_empty() or session == null or not session.is_open():
		return
	var info: Dictionary = session.get_lock_info(session.to_clone_path(res_path))
	if edit_action(info, false) == "warn":
		EditorInterface.get_editor_toaster().push_toast(
			"Saved %s, but it is locked by %s: the change cannot be pushed." % [res_path.get_file(), info.get("holder", "")],
			EditorToaster.SEVERITY_ERROR)


func _on_scene_saved(path: String) -> void:
	_remember_scene(path)
	_check_saved(path)


func _on_resource_saved(resource: Resource) -> void:
	_check_saved(InspectorPlugin.file_of(resource))


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
	_refresh_badges()


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
