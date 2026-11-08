@tool
extends AcceptDialog

const ROW_SCENE := preload("res://addons/nipa/ui/setup_row.tscn")
const VCS_NAME_SETTING := "editor/version_control/plugin_name"
const VCS_AUTOLOAD_SETTING := "editor/version_control/autoload_on_startup"
const VCS_PLUGIN := "NipaVCSInterface"

var session: Object
var _busy := false
var _note := ""

@onready var _summary: Label = $Body/Summary
@onready var _rows: VBoxContainer = $Body/Scroll/Rows
@onready var _recheck: Button = $Body/Toolbar/RecheckButton


func _ready() -> void:
	_recheck.pressed.connect(refresh)


func _exit_tree() -> void:
	session = null


static func summary_for(rows: Array) -> String:
	var open := 0
	for row in rows:
		if row.get("level", "ok") != "ok":
			open += 1
	if rows.is_empty():
		return ""
	return "Everything is set up." if open == 0 else "%d item(s) need attention." % open


# Selects NipaVCSInterface with autoload; writes project.godot only when a value changes.
static func apply_vcs_settings() -> bool:
	var changed := false
	if ProjectSettings.get_setting(VCS_NAME_SETTING, "") != VCS_PLUGIN:
		ProjectSettings.set_setting(VCS_NAME_SETTING, VCS_PLUGIN)
		changed = true
	if not ProjectSettings.get_setting(VCS_AUTOLOAD_SETTING, false):
		ProjectSettings.set_setting(VCS_AUTOLOAD_SETTING, true)
		changed = true
	if changed:
		ProjectSettings.save()
	return changed


func refresh() -> void:
	if session == null or not session.is_open():
		show_rows([])
		_summary.text = "Not connected: open a Godot project inside a Nipa clone."
		return
	if _busy:
		return
	_set_busy(true)
	_summary.text = "Checking..."
	var request: RefCounted = session.request_setup_check()
	if not request.is_done():
		await request.completed
	_set_busy(false)
	if not is_inside_tree():
		return
	if request.is_ok():
		show_rows(request.get_result().get("rows", []))
	else:
		show_rows([])
		_summary.text = "Setup check failed: %s" % request.get_message()


func show_rows(rows: Array) -> void:
	for child in _rows.get_children():
		_rows.remove_child(child)
		child.queue_free()
	for row in rows:
		var item: Control = ROW_SCENE.instantiate()
		_rows.add_child(item)
		item.show_row(row)
		item.fix_requested.connect(_on_fix)
	_summary.text = summary_for(rows)
	if not _note.is_empty():
		_summary.text = "%s %s" % [_note, _summary.text]
		_note = ""


func _set_busy(busy: bool) -> void:
	_busy = busy
	_recheck.disabled = busy
	for item in _rows.get_children():
		item.set_busy(busy)


func _on_fix(row: Dictionary) -> void:
	if _busy:
		return
	var paths := PackedStringArray(row.get("paths", []))
	match row.get("fix", ""):
		"set_vcs":
			apply_vcs_settings()
			_note = "Saved. Restart the editor (or use Project > Version Control) to load NipaVCSInterface."
		"write_ignore":
			if session:
				_note = session.apply_ignore_template().get("message", "")
		"stage_sidecars":
			await _stage(paths, PackedStringArray())
		"unstage_godot":
			await _stage(PackedStringArray(), paths)
	if is_inside_tree():
		refresh()


func _stage(add: PackedStringArray, unstage: PackedStringArray) -> void:
	if session == null or (add.is_empty() and unstage.is_empty()):
		return
	_set_busy(true)
	var request: RefCounted = session.request_stage(add, unstage)
	if not request.is_done():
		await request.completed
	_set_busy(false)
	_note = "Staged %d file(s)." % add.size() if request.is_ok() and not add.is_empty() else ""
	if not request.is_ok():
		_note = "Staging failed: %s" % request.get_message()
