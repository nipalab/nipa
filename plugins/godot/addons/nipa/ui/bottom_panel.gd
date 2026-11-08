@tool
extends TabContainer

var _session: Object

@onready var _tree: Tree = $Locks/LockTree
@onready var _refresh_button: Button = $Locks/Toolbar/RefreshButton
@onready var _release_button: Button = $Locks/Toolbar/ReleaseButton
@onready var _summary: Label = $Locks/Toolbar/Summary


func _ready() -> void:
	_tree.set_column_title(0, "Path")
	_tree.set_column_title(1, "Holder")
	_tree.set_column_title(2, "Scope")
	_refresh_button.pressed.connect(_on_refresh_pressed)
	_release_button.pressed.connect(_on_release_pressed)
	_tree.item_selected.connect(_update_release_button)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session:
		_session.locks_changed.connect(_on_locks_changed)
	refresh()


func _exit_tree() -> void:
	if _session and _session.locks_changed.is_connected(_on_locks_changed):
		_session.locks_changed.disconnect(_on_locks_changed)
	_session = null


func refresh() -> void:
	_tree.clear()
	var root := _tree.create_item()
	var locks: Array = _session.get_locks() if _session and _session.is_open() else []
	var mine := 0
	for lock in locks:
		var item := _tree.create_item(root)
		item.set_text(0, lock.get("path", ""))
		item.set_text(1, "you" if lock.get("mine", false) else lock.get("holder", ""))
		item.set_text(2, "mainline" if lock.get("global", false) else lock.get("branch", ""))
		item.set_metadata(0, lock)
		if lock.get("mine", false):
			mine += 1
	_summary.text = "%d lock(s), %d yours" % [locks.size(), mine]
	_update_release_button()


func _selected_lock() -> Dictionary:
	var item := _tree.get_selected()
	return item.get_metadata(0) if item else {}


func _update_release_button() -> void:
	_release_button.disabled = not _selected_lock().get("can_release", false)


func _on_locks_changed(_paths: PackedStringArray) -> void:
	refresh()


func _on_refresh_pressed() -> void:
	if _session:
		_session.request_lock_refresh()


func _on_release_pressed() -> void:
	var lock := _selected_lock()
	if _session and lock.get("can_release", false):
		_session.request_unlock(PackedStringArray([lock.get("path", "")]), true)
