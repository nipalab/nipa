@tool
extends HBoxContainer

var clone_path := ""
var _session: Object

@onready var _label: Label = $StateLabel
@onready var _button: Button = $ActionButton


static func describe(info: Dictionary) -> String:
	match info.get("state", "free"):
		"mine":
			return "Locked by you"
		"theirs":
			return "Locked by %s" % info.get("holder", "")
	return "Not locked"


static func action_for(info: Dictionary) -> String:
	match info.get("state", "free"):
		"mine":
			return "Unlock"
		"free":
			return "Lock"
	return ""


func _enter_tree() -> void:
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session and not _session.locks_changed.is_connected(_on_locks_changed):
		_session.locks_changed.connect(_on_locks_changed)


func _exit_tree() -> void:
	if _session and _session.locks_changed.is_connected(_on_locks_changed):
		_session.locks_changed.disconnect(_on_locks_changed)
	_session = null


func _ready() -> void:
	_button.pressed.connect(_on_pressed)
	refresh()


func set_res_path(res_path: String) -> void:
	var session: Object = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	clone_path = session.to_clone_path(res_path) if session and not res_path.is_empty() else ""
	refresh()


func refresh() -> void:
	if not is_node_ready():
		return
	var info := {}
	if _session and _session.is_open() and not clone_path.is_empty():
		info = _session.get_lock_info(clone_path)
	visible = not info.is_empty() and (info.get("relevant", false) or info.get("state", "free") != "free")
	_label.text = describe(info)
	_button.text = action_for(info)
	_button.visible = not _button.text.is_empty()


func _on_locks_changed(_paths: PackedStringArray) -> void:
	refresh()


func _on_pressed() -> void:
	if _session == null or clone_path.is_empty():
		return
	if _button.text == "Unlock":
		_session.request_unlock(PackedStringArray([clone_path]), false)
	else:
		_session.request_lock(PackedStringArray([clone_path]))
