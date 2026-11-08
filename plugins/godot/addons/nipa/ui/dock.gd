@tool
extends VBoxContainer

signal reconnect_requested
signal conflicts_requested

const DATA_OPS := ["conflicts", "merge-godot", "merge-tool", "history", "preview", "resolve"]

var _pending_action: Callable
var _commit_message := ""
var _commit_paths := PackedStringArray()
var _reload_path := ""

@onready var _state_label: Label = $Header/StateLabel
@onready var _reconnect_button: Button = $Header/ReconnectButton
@onready var _branch_label: Label = $BranchLabel
@onready var _out_of_date: HBoxContainer = $OutOfDate
@onready var _out_of_date_label: Label = $OutOfDate/OutOfDateLabel
@onready var _update_button: Button = $OutOfDate/UpdateButton
@onready var _op_label: Label = $OpLabel
@onready var _op_progress: ProgressBar = $OpProgress
@onready var _outside_note: Label = $OutsideNote
@onready var _open_desktop_button: Button = $OpenDesktopButton
@onready var _save_dialog: ConfirmationDialog = $SaveDialog
@onready var _locks_label: Label = $LocksLabel
@onready var _lock_commit_dialog: ConfirmationDialog = $LockCommitDialog
@onready var _reload_dialog: ConfirmationDialog = $ReloadDialog
@onready var _ref_picker: OptionButton = $Refs/RefPicker
@onready var _merge_button: Button = $Refs/MergeButton
@onready var _detached_banner: HBoxContainer = $DetachedBanner
@onready var _detached_label: Label = $DetachedBanner/DetachedLabel
@onready var _back_button: Button = $DetachedBanner/BackButton
@onready var _pending_banner: VBoxContainer = $PendingBanner
@onready var _pending_label: Label = $PendingBanner/PendingLabel
@onready var _conflicts_button: Button = $PendingBanner/Buttons/ConflictsButton
@onready var _finish_button: Button = $PendingBanner/Buttons/FinishButton
@onready var _continue_button: Button = $PendingBanner/Buttons/ContinueButton
@onready var _skip_button: Button = $PendingBanner/Buttons/SkipButton
@onready var _abort_button: Button = $PendingBanner/Buttons/AbortButton
@onready var _merge_dialog: ConfirmationDialog = $MergeDialog
@onready var _branch_picker: OptionButton = $MergeDialog/Body/BranchPicker

var _session: Object


func _ready() -> void:
	_reconnect_button.pressed.connect(_on_reconnect_pressed)
	_update_button.pressed.connect(request_update)
	_open_desktop_button.pressed.connect(_on_open_desktop_pressed)
	_save_dialog.confirmed.connect(_on_save_confirmed)
	_lock_commit_dialog.confirmed.connect(_on_lock_commit_confirmed)
	_reload_dialog.confirmed.connect(_on_reload_confirmed)
	_ref_picker.item_selected.connect(_on_ref_selected)
	_merge_button.pressed.connect(_on_merge_pressed)
	_merge_dialog.confirmed.connect(_on_merge_confirmed)
	_back_button.pressed.connect(_on_back_pressed)
	_conflicts_button.pressed.connect(func(): conflicts_requested.emit())
	_finish_button.pressed.connect(_on_finish_pressed)
	_continue_button.pressed.connect(_on_revert_step.bind("continue"))
	_skip_button.pressed.connect(_on_revert_step.bind("skip"))
	_abort_button.pressed.connect(_on_abort_pressed)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	if _session:
		_session.connected.connect(_on_connected)
		_session.disconnected.connect(_on_disconnected)
		_session.status_changed.connect(_on_status_changed)
		_session.branches_changed.connect(_refresh_branch)
		_session.op_progress.connect(_on_op_progress)
		_session.op_queued.connect(_on_op_queued)
		_session.op_finished.connect(_on_op_finished)
		_session.activity_summary.connect(_on_activity_summary)
		_session.notice.connect(_on_notice)
		_session.locks_changed.connect(_on_locks_changed)
		_session.commit_locks_needed.connect(_on_commit_locks_needed)
		_session.tags_changed.connect(_refresh_refs)
		_session.conflicts_changed.connect(_refresh_banners)
	_refresh_branch()
	_refresh_locks()


func _exit_tree() -> void:
	if _session == null:
		return
	for pair in [
		[_session.connected, _on_connected],
		[_session.disconnected, _on_disconnected],
		[_session.status_changed, _on_status_changed],
		[_session.branches_changed, _refresh_branch],
		[_session.op_progress, _on_op_progress],
		[_session.op_queued, _on_op_queued],
		[_session.op_finished, _on_op_finished],
		[_session.activity_summary, _on_activity_summary],
		[_session.notice, _on_notice],
		[_session.locks_changed, _on_locks_changed],
		[_session.commit_locks_needed, _on_commit_locks_needed],
		[_session.tags_changed, _refresh_refs],
		[_session.conflicts_changed, _refresh_banners],
	]:
		if pair[0].is_connected(pair[1]):
			pair[0].disconnect(pair[1])
	_session = null


func request_update() -> void:
	_ask_save_then(func(): _session.request_update())


func request_switch(branch: String) -> void:
	_ask_save_then(func(): _session.request_switch(branch))


func _ask_save_then(action: Callable) -> void:
	_pending_action = action
	if EditorInterface.get_open_scenes().is_empty():
		_on_save_confirmed()
	else:
		_save_dialog.popup_centered()


func _on_save_confirmed() -> void:
	if _pending_action.is_valid():
		EditorInterface.save_all_scenes()
		_pending_action.call()
	_pending_action = Callable()


func _on_connected(version: String) -> void:
	_state_label.text = "Connected (daemon %s)" % version
	_reconnect_button.disabled = true
	_refresh_branch()


func _on_disconnected(reason: String) -> void:
	_state_label.text = "Disconnected: %s" % reason
	_reconnect_button.disabled = false


func _on_reconnect_pressed() -> void:
	reconnect_requested.emit()


func _on_status_changed(_paths: PackedStringArray) -> void:
	_refresh_branch()
	var outside: PackedStringArray = _session.get_outside_project_staged() if _session else PackedStringArray()
	_outside_note.visible = not outside.is_empty()
	_outside_note.text = "%d staged file(s) are outside this project and will be committed too." % outside.size()


func _refresh_branch() -> void:
	_branch_label.text = "Branch: %s" % (_session.get_branch_name() if _session and _session.is_open() else "-")
	_refresh_refs()
	_refresh_banners()


# Switcher entries: branches, then tags ("Tag: v1.0"), each {text, kind, name}.
static func ref_items(branches: PackedStringArray, tags: PackedStringArray) -> Array:
	var out := []
	for branch in branches:
		out.append({"text": branch, "kind": "branch", "name": branch})
	for tag in tags:
		out.append({"text": "Tag: %s" % tag, "kind": "tag", "name": tag})
	return out


# What the banners show for a head (get_head) and the conflict set (get_conflicts).
static func banner_state(head: Dictionary, conflicts: Dictionary) -> Dictionary:
	var state := {"detached": head.get("detached", false), "detached_text": "", "pending": head.get("pending", ""),
		"pending_text": "", "can_finish": false}
	if state.detached:
		state.detached_text = "Detached at tag %s. Commits are disabled; switch to a branch to work." % head.get("head_name", "")
	var open: int = conflicts.get("open", 0)
	var conflict_text := "%d conflict(s) left." % open if open > 0 else "All conflicts resolved."
	match state.pending:
		"merge":
			state.pending_text = "Merging %s into %s. %s" % [
				conflicts.get("theirs", {}).get("label", "a branch"), head.get("branch", ""), conflict_text]
		"revert":
			var remaining: int = head.get("pending_remaining", 0)
			state.pending_text = "Revert in progress (%d step(s) left). %s" % [remaining, conflict_text]
	state.can_finish = state.pending != "" and open == 0
	return state


func _refresh_refs() -> void:
	if _ref_picker == null:
		return
	_ref_picker.clear()
	var open: bool = _session != null and _session.is_open()
	_merge_button.disabled = not open
	if not open:
		return
	var head: Dictionary = _session.get_head()
	var current: String = head.get("head_name", "") if head.get("detached", false) else head.get("branch", "")
	var items := ref_items(_session.get_branch_names(), _session.get_tag_names())
	for i in items.size():
		_ref_picker.add_item(items[i].text)
		_ref_picker.set_item_metadata(i, items[i])
		var kind := "tag" if head.get("detached", false) else "branch"
		if items[i].name == current and items[i].kind == kind:
			_ref_picker.select(i)


func _refresh_banners() -> void:
	if _pending_banner == null:
		return
	var open: bool = _session != null and _session.is_open()
	var state := banner_state(_session.get_head() if open else {}, _session.get_conflicts() if open else {})
	_detached_banner.visible = state.detached
	_detached_label.text = state.detached_text
	_pending_banner.visible = state.pending != ""
	_pending_label.text = state.pending_text
	_finish_button.visible = state.pending == "merge"
	_continue_button.visible = state.pending == "revert"
	_skip_button.visible = state.pending == "revert"
	_finish_button.disabled = not state.can_finish
	_continue_button.disabled = not state.can_finish


func _on_ref_selected(index: int) -> void:
	var item: Dictionary = _ref_picker.get_item_metadata(index)
	var head: Dictionary = _session.get_head() if _session else {}
	if item.kind == "tag":
		if head.get("detached", false) and head.get("head_name", "") == item.name:
			return
		_ask_save_then(func(): _session.request_switch_tag(item.name))
	elif head.get("detached", false) or head.get("branch", "") != item.name:
		request_switch(item.name)


func _on_merge_pressed() -> void:
	_branch_picker.clear()
	var current: String = _session.get_head().get("branch", "") if _session else ""
	for branch in (_session.get_branch_names() if _session else PackedStringArray()):
		if branch != current:
			_branch_picker.add_item(branch)
	_merge_dialog.get_ok_button().disabled = _branch_picker.item_count == 0
	_merge_dialog.popup_centered()


func _on_merge_confirmed() -> void:
	if _branch_picker.selected < 0:
		return
	var source := _branch_picker.get_item_text(_branch_picker.selected)
	_ask_save_then(func(): _merge(source))


func _merge(source: String) -> void:
	var request: RefCounted = _session.request_merge(source)
	await request.completed
	if request.is_ok() and not request.get_result().get("merge", {}).get("conflicts", []).is_empty():
		conflicts_requested.emit()


func _on_back_pressed() -> void:
	if _session:
		request_switch(_session.get_head().get("branch", ""))


func _on_finish_pressed() -> void:
	if _session == null:
		return
	var conflicts: Dictionary = _session.get_conflicts()
	_session.request_push("Merge branch '%s' into '%s'" % [
		conflicts.get("theirs", {}).get("label", ""), _session.get_head().get("branch", "")])


func _on_revert_step(step: String) -> void:
	if _session == null:
		return
	if step == "continue":
		_ask_save_then(func(): _session.request_revert_continue())
	else:
		_ask_save_then(func(): _session.request_revert_skip())


func _on_abort_pressed() -> void:
	if _session == null:
		return
	if _session.get_head().get("pending", "") == "revert":
		_ask_save_then(func(): _session.request_revert_abort())
	else:
		_ask_save_then(func(): _session.request_merge_abort())


func _on_op_queued(_id: int, kind: String) -> void:
	if DATA_OPS.has(kind):
		return
	_op_label.visible = true
	_op_label.text = "Queued: %s" % kind
	_op_progress.visible = false


func _on_op_progress(_id: int, phase: String, done: int, total: int) -> void:
	_op_label.visible = true
	_op_label.text = phase
	_op_progress.visible = total > 0
	_op_progress.value = float(done) / float(total) if total > 0 else 0.0


func _on_op_finished(_id: int, _ok: bool, _message: String, _paths: PackedStringArray) -> void:
	_op_label.visible = false
	_op_progress.visible = false
	_refresh_branch()


func _on_activity_summary(incoming: int, _review_requests: int, error: String) -> void:
	_out_of_date.visible = incoming > 0
	_out_of_date_label.text = "%d new commit(s) on this branch." % incoming
	if not error.is_empty():
		_out_of_date_label.text += " (%s)" % error


func _on_notice(text: String) -> void:
	EditorInterface.get_editor_toaster().push_toast(text, EditorToaster.SEVERITY_INFO)


func _on_open_desktop_pressed() -> void:
	if _session and _session.is_open():
		OS.shell_open("nipa://open?root=" + _session.get_clone_root().uri_encode())


static func locks_summary(locks: Array) -> String:
	var mine := 0
	for lock in locks:
		if lock.get("mine", false):
			mine += 1
	return "Locks: %d yours, %d by others" % [mine, locks.size() - mine]


func _refresh_locks() -> void:
	var locks: Array = _session.get_locks() if _session and _session.is_open() else []
	_locks_label.text = locks_summary(locks)


func _on_locks_changed(_paths: PackedStringArray) -> void:
	_refresh_locks()


func _on_commit_locks_needed(message: String, paths: PackedStringArray) -> void:
	_commit_message = message
	_commit_paths = paths
	_lock_commit_dialog.dialog_text = "%d changed file(s) need a lock before they can be committed:\n%s" % [
		paths.size(), "\n".join(paths)]
	_lock_commit_dialog.ok_button_text = "Lock %d file(s) and commit" % paths.size()
	_lock_commit_dialog.popup_centered()


func _on_lock_commit_confirmed() -> void:
	if _session == null or _commit_paths.is_empty():
		return
	var message := _commit_message
	var request: RefCounted = _session.request_lock(_commit_paths)
	_commit_paths = PackedStringArray()
	await request.completed
	if request.is_ok() and _session:
		_session.request_push(message)


func offer_reload(res_path: String, holder: String) -> void:
	_reload_path = res_path
	_reload_dialog.dialog_text = "%s is locked by %s, your changes cannot be pushed.\nReload it from disk?" % [
		res_path.get_file(), holder]
	_reload_dialog.popup_centered()


func _on_reload_confirmed() -> void:
	if not _reload_path.is_empty():
		EditorInterface.reload_scene_from_path(_reload_path)
	_reload_path = ""
