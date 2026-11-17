@tool
extends TabContainer

signal release_requested(clone_paths: PackedStringArray, holders: Dictionary)

const ShelvesTab := preload("res://addons/nipa/ui/shelves_tab.gd")

var watches: RefCounted:
	set(value):
		if watches and watches.changed.is_connected(_update_release_button):
			watches.changed.disconnect(_update_release_button)
		watches = value
		if watches:
			watches.changed.connect(_update_release_button)
		if is_node_ready():
			_update_release_button()
var shelves_tab: Control
var _session: Object
var _ask_button: Button
var _watch_button: Button

@onready var _tree: Tree = $Locks/LockTree
@onready var _refresh_button: Button = $Locks/Toolbar/RefreshButton
@onready var _release_button: Button = $Locks/Toolbar/ReleaseButton
@onready var _summary: Label = $Locks/Toolbar/Summary
@onready var conflicts_tab: Control = $Conflicts
@onready var history_tab: Control = $History
@onready var image_diff_tab: Control = get_node("Image diff")


func _ready() -> void:
	_tree.set_column_title(0, "Path")
	_tree.set_column_title(1, "Holder")
	_tree.set_column_title(2, "Scope")
	_refresh_button.pressed.connect(_on_refresh_pressed)
	_release_button.pressed.connect(_on_release_pressed)
	_tree.item_selected.connect(_update_release_button)
	_ask_button = Button.new()
	_ask_button.text = "Ask to release"
	_ask_button.pressed.connect(_on_ask_pressed)
	_release_button.add_sibling(_ask_button)
	_watch_button = Button.new()
	_watch_button.pressed.connect(_on_watch_pressed)
	_ask_button.add_sibling(_watch_button)
	shelves_tab = ShelvesTab.new()
	add_child(shelves_tab)
	shelves_tab.conflicts_requested.connect(show_conflicts)
	conflicts_tab.compare_requested.connect(show_image_diff)
	history_tab.compare_requested.connect(show_image_diff)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session:
		_session.locks_changed.connect(_on_locks_changed)
	refresh()


func _exit_tree() -> void:
	watches = null
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


# Ask/watch button state for a selected lock: {theirs, watched, watch_text}.
static func theirs_actions(lock: Dictionary, watched: bool) -> Dictionary:
	var theirs: bool = not lock.is_empty() and not lock.get("mine", false)
	return {"theirs": theirs, "watched": watched,
		"watch_text": "Stop notifying" if watched else "Notify me when free"}


func _update_release_button() -> void:
	var lock := _selected_lock()
	_release_button.disabled = not lock.get("can_release", false)
	if _ask_button == null:
		return
	var path: String = lock.get("path", "")
	var actions := theirs_actions(lock, watches != null and watches.is_watched(path))
	_ask_button.disabled = not actions.theirs
	_watch_button.disabled = not actions.theirs or watches == null
	_watch_button.text = actions.watch_text


func _on_ask_pressed() -> void:
	var lock := _selected_lock()
	if lock.is_empty() or lock.get("mine", false):
		return
	release_requested.emit(PackedStringArray([lock.get("path", "")]), {lock.get("path", ""): lock.get("holder", "")})


func _on_watch_pressed() -> void:
	var path: String = _selected_lock().get("path", "")
	if watches == null or path.is_empty():
		return
	if watches.is_watched(path):
		watches.unwatch(path)
	else:
		watches.watch(path)


func _on_locks_changed(_paths: PackedStringArray) -> void:
	refresh()


func _on_refresh_pressed() -> void:
	if _session:
		_session.request_lock_refresh()


func _on_release_pressed() -> void:
	var lock := _selected_lock()
	if _session and lock.get("can_release", false):
		_session.request_unlock(PackedStringArray([lock.get("path", "")]), true)


func show_conflicts() -> void:
	current_tab = conflicts_tab.get_index()
	conflicts_tab.refresh_from_daemon()


func show_history(clone_path: String) -> void:
	current_tab = history_tab.get_index()
	history_tab.show_history(clone_path)


func show_image_diff(old_ref: Dictionary, new_ref: Dictionary, title: String) -> void:
	current_tab = image_diff_tab.get_index()
	image_diff_tab.show_diff(old_ref, new_ref, title)
