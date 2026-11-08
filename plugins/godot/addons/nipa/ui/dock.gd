@tool
extends VBoxContainer

signal reconnect_requested

var _pending_action: Callable

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

var _session: Object


func _ready() -> void:
	_reconnect_button.pressed.connect(_on_reconnect_pressed)
	_update_button.pressed.connect(request_update)
	_open_desktop_button.pressed.connect(_on_open_desktop_pressed)
	_save_dialog.confirmed.connect(_on_save_confirmed)
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
	_refresh_branch()


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


func _on_op_queued(_id: int, kind: String) -> void:
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
