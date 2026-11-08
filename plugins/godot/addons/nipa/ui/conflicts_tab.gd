@tool
extends VBoxContainer

signal compare_requested(old_ref: Dictionary, new_ref: Dictionary, title: String)

const ACTION_BUTTONS := {
	"mine": "Actions/MineButton",
	"theirs": "Actions/TheirsButton",
	"merge": "Actions/MergeButton",
	"external": "Actions/ExternalButton",
	"compare": "Actions/CompareButton",
}
const POLL_SECONDS := 0.5

var _session: Object
var _conflicts: Dictionary = {}
var _tool_pid := -1
var _tool_path := ""
var _tool_merged := ""

@onready var _summary: Label = $Toolbar/Summary
@onready var _refresh_button: Button = $Toolbar/RefreshButton
@onready var _tree: Tree = $FileTree
@onready var _detail: Label = $Detail
@onready var _merge_conflicts: ItemList = $MergeConflicts
@onready var _tool_timer: Timer = $ToolTimer


static func summary_for(conflicts: Dictionary) -> String:
	var op: String = conflicts.get("op", "")
	if op.is_empty():
		return "No merge or revert in progress."
	var files: Array = conflicts.get("files", [])
	var open: int = conflicts.get("open", 0)
	var ours: String = conflicts.get("ours", {}).get("label", "")
	var theirs: String = conflicts.get("theirs", {}).get("label", "")
	var head := "Merging %s into %s" % [theirs, ours] if op == "merge" else "Reverting %s" % conflicts.get("base", {}).get("label", "")
	if open == 0:
		return "%s: all %d file(s) resolved." % [head, files.size()]
	return "%s: %d of %d file(s) still conflicted." % [head, open, files.size()]


static func conflict_lines(result: Dictionary) -> PackedStringArray:
	var out := PackedStringArray()
	for item in result.get("conflicts", []):
		var label: String = item.get("label", "")
		out.append("%s: %s" % [label, item.get("message", "")] if not label.is_empty() else item.get("message", ""))
	return out


func _ready() -> void:
	_tree.set_column_title(0, "File")
	_tree.set_column_title(1, "Conflict")
	_tree.set_column_title(2, "State")
	_tree.item_selected.connect(_update_actions)
	_refresh_button.pressed.connect(refresh_from_daemon)
	for action in ACTION_BUTTONS:
		get_node(ACTION_BUTTONS[action]).pressed.connect(_on_action.bind(action))
	_tool_timer.wait_time = POLL_SECONDS
	_tool_timer.timeout.connect(_poll_tool)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session:
		_session.conflicts_changed.connect(_on_conflicts_changed)
	show_conflicts(_session.get_conflicts() if _session and _session.is_open() else {})


func _exit_tree() -> void:
	_tool_timer.stop()
	if _session and _session.conflicts_changed.is_connected(_on_conflicts_changed):
		_session.conflicts_changed.disconnect(_on_conflicts_changed)
	_session = null


func refresh_from_daemon() -> void:
	if _session and _session.is_open():
		_session.request_conflicts()


func _on_conflicts_changed() -> void:
	show_conflicts(_session.get_conflicts())


func show_conflicts(conflicts: Dictionary) -> void:
	var selected: String = _selected_file().get("path", "")
	_conflicts = conflicts
	_summary.text = summary_for(conflicts)
	_tree.clear()
	var root := _tree.create_item()
	for file in conflicts.get("files", []):
		var item := _tree.create_item(root)
		item.set_text(0, file.get("path", ""))
		item.set_text(1, file.get("kind", ""))
		item.set_text(2, "resolved" if file.get("resolved", false) else "conflicted")
		item.set_metadata(0, file)
		if file.get("path", "") == selected:
			item.select(0)
	_update_actions()


func _selected_file() -> Dictionary:
	if _tree == null:
		return {}
	var item := _tree.get_selected()
	return item.get_metadata(0) if item else {}


func _update_actions() -> void:
	var file := _selected_file()
	var actions: Array = file.get("actions", [])
	for action in ACTION_BUTTONS:
		var button: Button = get_node(ACTION_BUTTONS[action])
		button.visible = actions.has(action)
		button.disabled = _tool_pid >= 0
	_detail.text = "" if file.is_empty() else "%s (%s)" % [file.get("path", ""), file.get("kind", "")]
	_merge_conflicts.clear()
	_merge_conflicts.visible = false


func _on_action(action: String) -> void:
	var file := _selected_file()
	var path: String = file.get("path", "")
	if _session == null or path.is_empty():
		return
	match action:
		"mine":
			_resolve(path, "ours", PackedByteArray())
		"theirs":
			_resolve(path, "theirs", PackedByteArray())
		"merge":
			_merge_scene(path)
		"external":
			_run_external_tool(path)
		"compare":
			var ours: Dictionary = _conflicts.get("ours", {})
			var theirs: Dictionary = _conflicts.get("theirs", {})
			compare_requested.emit(
				{"source": "revision", "revision": ours.get("id", ""), "path": path},
				{"source": "revision", "revision": theirs.get("id", ""), "path": path},
				"%s: %s vs %s" % [path, ours.get("label", "mine"), theirs.get("label", "theirs")])


func _resolve(path: String, choice: String, content: PackedByteArray) -> void:
	var request: RefCounted = _session.request_resolve(path, choice, content)
	await request.completed
	if not request.is_ok():
		_detail.text = "Could not resolve %s: %s" % [path, request.get_message()]


func _merge_scene(path: String) -> void:
	_detail.text = "Merging %s..." % path
	var request: RefCounted = _session.request_merge_godot(path)
	await request.completed
	if not is_inside_tree():
		return
	if not request.is_ok():
		_detail.text = "Merge failed: %s" % request.get_message()
		return
	var result: Dictionary = request.get_result()
	if result.get("clean", false):
		_detail.text = "Merged %s cleanly." % path
		_resolve(path, "content", result.get("merged", PackedByteArray()))
		return
	_detail.text = "%s needs a manual pick, keep Mine or Theirs:" % path
	_merge_conflicts.clear()
	for line in conflict_lines(result):
		_merge_conflicts.add_item(line)
	_merge_conflicts.visible = true


func _run_external_tool(path: String) -> void:
	var command := NipaSettings.merge_tool()
	if command.strip_edges().is_empty():
		_detail.text = "Set Editor Settings > nipa/merge_tool, e.g. meld $LOCAL $BASE $REMOTE -o $MERGED."
		return
	var request: RefCounted = _session.request_prepare_merge_tool(path)
	await request.completed
	if not is_inside_tree():
		return
	if not request.is_ok():
		_detail.text = "Could not prepare %s: %s" % [path, request.get_message()]
		return
	var paths: Dictionary = request.get_result()
	var argv: PackedStringArray = _session.build_merge_command(command, paths)
	if argv.is_empty():
		_detail.text = "nipa/merge_tool is not a valid command (check its quotes)."
		return
	var pid := OS.create_process(argv[0], argv.slice(1))
	if pid < 0:
		_detail.text = "Could not start %s." % argv[0]
		return
	_tool_pid = pid
	_tool_path = path
	_tool_merged = paths.get("merged", "")
	_detail.text = "Waiting for %s to finish..." % argv[0]
	_update_actions()
	_tool_timer.start()


func _poll_tool() -> void:
	if _tool_pid < 0 or OS.is_process_running(_tool_pid):
		return
	_tool_timer.stop()
	var code := OS.get_process_exit_code(_tool_pid)
	var path := _tool_path
	_tool_pid = -1
	_update_actions()
	if code != 0:
		_detail.text = "The merge tool exited with %d; %s is unchanged." % [code, path]
		return
	var merged := FileAccess.get_file_as_bytes(_tool_merged)
	if _session.has_conflict_markers(merged):
		_detail.text = "%s still has conflict markers; nothing was resolved." % path
		return
	_resolve(path, "content", merged)
