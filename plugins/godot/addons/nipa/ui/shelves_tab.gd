@tool
extends VBoxContainer

# Local shelves of the clone: apply (drop or keep), delete, and abort a waiting apply.

signal conflicts_requested

const SHELF_OPS := ["shelve", "shelve-apply", "shelve-abort", "shelve-drop"]

var _session: Object
var _shelves: Array = []
var _applying := ""
var _ops := {}
var _stale := true

var _summary: Label
var _tree: Tree
var _apply_button: Button
var _keep_button: Button
var _delete_button: Button
var _abort_button: Button
var _refresh_button: Button
var _delete_dialog: ConfirmationDialog


static func describe(shelf: Dictionary) -> String:
	var where: String = shelf.get("tag", "")
	where = "tag " + where if not where.is_empty() else shelf.get("branch", "")
	var files: Array = shelf.get("entries", [])
	return "%d file(s) on %s" % [files.size(), where] if not where.is_empty() else "%d file(s)" % files.size()


static func created_text(unix_seconds: int) -> String:
	if unix_seconds <= 0:
		return ""
	return Time.get_datetime_string_from_unix_time(unix_seconds, true).left(16)


static func summary_for(shelves: Array, applying: String) -> String:
	if not applying.is_empty():
		return "A shelf apply waits on conflicts: resolve them in Conflicts or abort it."
	if shelves.is_empty():
		return "No shelves. Update and Switch offer to shelve the changes they would replace."
	return "%d shelf(s)." % shelves.size()


func _init() -> void:
	name = "Shelves"
	var toolbar := HBoxContainer.new()
	_summary = Label.new()
	_summary.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	_summary.clip_text = true
	toolbar.add_child(_summary)
	_apply_button = _button(toolbar, "Apply", _on_apply.bind(false))
	_keep_button = _button(toolbar, "Apply and keep", _on_apply.bind(true))
	_delete_button = _button(toolbar, "Delete", _on_delete)
	_abort_button = _button(toolbar, "Abort apply", _on_abort)
	_refresh_button = _button(toolbar, "Refresh", refresh_from_daemon)
	add_child(toolbar)
	_tree = Tree.new()
	_tree.columns = 3
	_tree.column_titles_visible = true
	_tree.hide_root = true
	_tree.size_flags_vertical = Control.SIZE_EXPAND_FILL
	_tree.set_column_title(0, "Shelf")
	_tree.set_column_title(1, "Contents")
	_tree.set_column_title(2, "Created")
	_tree.item_selected.connect(_update_actions)
	add_child(_tree)
	_delete_dialog = ConfirmationDialog.new()
	_delete_dialog.ok_button_text = "Delete"
	_delete_dialog.confirmed.connect(_on_delete_confirmed)
	add_child(_delete_dialog)


func _button(parent: Control, text: String, action: Callable) -> Button:
	var button := Button.new()
	button.text = text
	button.pressed.connect(action)
	parent.add_child(button)
	return button


func _ready() -> void:
	visibility_changed.connect(_on_visibility_changed)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session:
		_session.op_queued.connect(_on_op_queued)
		_session.op_finished.connect(_on_op_finished)
	show_shelves([], "")


func _exit_tree() -> void:
	if _session:
		for pair in [[_session.op_queued, _on_op_queued], [_session.op_finished, _on_op_finished]]:
			if pair[0].is_connected(pair[1]):
				pair[0].disconnect(pair[1])
	_session = null


func refresh_from_daemon() -> void:
	_stale = false
	if _session == null or not _session.is_open():
		show_shelves([], "")
		return
	var request: RefCounted = _session.request_shelves()
	if not request.is_done():
		await request.completed
	if not is_inside_tree():
		return
	if request.is_ok():
		var result: Dictionary = request.get_result()
		show_shelves(result.get("shelves", []), result.get("applying", ""))
	else:
		_summary.text = "Could not list shelves: %s" % request.get_message()


func show_shelves(shelves: Array, applying: String) -> void:
	var selected: String = _selected().get("id", "")
	_shelves = shelves
	_applying = applying
	_summary.text = summary_for(shelves, applying)
	_tree.clear()
	var root := _tree.create_item()
	for shelf in shelves:
		var item := _tree.create_item(root)
		item.set_text(0, shelf.get("name", "") if not String(shelf.get("name", "")).is_empty() else shelf.get("id", ""))
		item.set_text(1, describe(shelf))
		item.set_text(2, created_text(int(shelf.get("created_at", 0))))
		var paths := PackedStringArray()
		for entry in shelf.get("entries", []):
			paths.append("%s (%s)" % [entry.get("path", ""), entry.get("status", "")])
		item.set_tooltip_text(0, "\n".join(paths))
		item.set_metadata(0, shelf)
		if shelf.get("id", "") == selected:
			item.select(0)
	_update_actions()


func _selected() -> Dictionary:
	var item := _tree.get_selected() if _tree else null
	return item.get_metadata(0) if item else {}


func _update_actions() -> void:
	var has := not _selected().is_empty()
	var waiting := not _applying.is_empty()
	_apply_button.disabled = not has or waiting
	_keep_button.disabled = not has or waiting
	_delete_button.disabled = not has or _selected().get("id", "") == _applying
	_abort_button.visible = waiting


func _on_apply(keep: bool) -> void:
	var shelf := _selected()
	if _session == null or shelf.is_empty():
		return
	var request: RefCounted = _session.request_shelve_apply(shelf.get("id", ""), keep)
	if not request.is_done():
		await request.completed
	if not request.is_ok():
		_summary.text = "Could not apply the shelf: %s" % request.get_message()
	elif not request.get_result().get("shelve_apply", {}).get("conflicts", []).is_empty():
		conflicts_requested.emit()


func _on_delete() -> void:
	var shelf := _selected()
	if shelf.is_empty():
		return
	_delete_dialog.dialog_text = "Delete shelf \"%s\" (%s)? Its changes are lost." % [shelf.get("name", ""), describe(shelf)]
	_delete_dialog.popup_centered()


func _on_delete_confirmed() -> void:
	var shelf := _selected()
	if _session and not shelf.is_empty():
		_session.request_shelve_drop(shelf.get("id", ""))


func _on_abort() -> void:
	if _session:
		_session.request_shelve_abort()


func _on_op_queued(id: int, kind: String) -> void:
	if SHELF_OPS.has(kind):
		_ops[id] = kind


func _on_op_finished(id: int, _ok: bool, _message: String, _paths: PackedStringArray) -> void:
	if not _ops.has(id):
		return
	_ops.erase(id)
	_stale = true
	if is_visible_in_tree():
		refresh_from_daemon()


func _on_visibility_changed() -> void:
	if _stale and is_visible_in_tree():
		refresh_from_daemon()
