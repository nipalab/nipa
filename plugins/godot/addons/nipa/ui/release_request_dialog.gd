@tool
extends ConfirmationDialog

# Asks the holders of the locks on `clone_paths` to release them, with an optional note.

const MAX_MESSAGE := 500

var session: Object
var clone_paths := PackedStringArray()
var _message: LineEdit
var _intro: Label


func _init() -> void:
	title = "Ask to release"
	ok_button_text = "Send request"
	var body := VBoxContainer.new()
	_intro = Label.new()
	_intro.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	_intro.custom_minimum_size = Vector2(360, 0)
	body.add_child(_intro)
	_message = LineEdit.new()
	_message.placeholder_text = "Optional note for the holder"
	_message.max_length = MAX_MESSAGE
	body.add_child(_message)
	add_child(body)
	confirmed.connect(_on_confirmed)


static func intro_for(holders: Dictionary) -> String:
	var lines := PackedStringArray()
	for path in holders:
		lines.append("%s (locked by %s)" % [path, holders[path]])
	return "Ask the holder to release:\n%s" % "\n".join(lines)


func open(paths: PackedStringArray, holders: Dictionary) -> void:
	clone_paths = paths
	_intro.text = intro_for(holders)
	_message.text = ""
	popup_centered()
	_message.grab_focus()


func message() -> String:
	return _message.text.strip_edges()


func _on_confirmed() -> void:
	if session == null:
		return
	for path in clone_paths:
		session.request_lock_release(path, message())
