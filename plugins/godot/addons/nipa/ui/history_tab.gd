@tool
extends VBoxContainer

signal compare_requested(old_ref: Dictionary, new_ref: Dictionary, title: String)

const PAGE_SIZE := 30
const IMAGE_EXTENSIONS := ["png", "jpg", "jpeg", "webp", "bmp", "tga", "exr", "hdr", "dds", "ktx2", "psd", "svg"]

var _session: Object
var _clone_path := ""
var _next := ""
var _generation := 0

@onready var _target: Label = $Toolbar/Target
@onready var _project_button: Button = $Toolbar/ProjectButton
@onready var _more_button: Button = $Toolbar/MoreButton
@onready var _tree: Tree = $CommitTree
@onready var _open_button: Button = $Actions/OpenButton
@onready var _compare_button: Button = $Actions/CompareButton
@onready var _detail: Label = $Actions/Detail


static func is_image(path: String) -> bool:
	return IMAGE_EXTENSIONS.has(path.get_extension().to_lower())


static func first_line(message: String) -> String:
	return message.strip_edges().get_slice("\n", 0)


func _ready() -> void:
	_tree.set_column_title(0, "Message")
	_tree.set_column_title(1, "Author")
	_tree.set_column_title(2, "Date")
	_tree.set_column_title(3, "Commit")
	_tree.item_selected.connect(_update_actions)
	_tree.item_activated.connect(_open_selected)
	_project_button.pressed.connect(show_history.bind(""))
	_more_button.pressed.connect(_load_page)
	_open_button.pressed.connect(_open_selected)
	_compare_button.pressed.connect(_compare_selected)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	_update_actions()


func _exit_tree() -> void:
	_session = null


# Lists the commits that changed `clone_path` (empty = the whole project).
func show_history(clone_path: String) -> void:
	_clone_path = clone_path
	_next = ""
	_generation += 1
	_tree.clear()
	_tree.create_item()
	_target.text = "History: %s" % (clone_path if not clone_path.is_empty() else "whole project")
	_load_page()


func _load_page() -> void:
	if _session == null or not _session.is_open():
		_detail.text = "Not connected."
		return
	var generation := _generation
	_more_button.disabled = true
	var request: RefCounted = _session.request_history(_clone_path, _next, PAGE_SIZE)
	await request.completed
	if not is_inside_tree() or generation != _generation:
		return
	if not request.is_ok():
		_detail.text = "History failed: %s" % request.get_message()
		return
	var page: Dictionary = request.get_result()
	add_page(page.get("commits", []), page.get("next", ""))


func add_page(commits: Array, next: String) -> void:
	var root := _tree.get_root()
	if root == null:
		root = _tree.create_item()
	for commit in commits:
		var item := _tree.create_item(root)
		item.set_text(0, first_line(commit.get("message", "")))
		item.set_text(1, commit.get("author", ""))
		item.set_text(2, String(commit.get("created_at", "")).left(16).replace("T", " "))
		item.set_text(3, commit.get("id", ""))
		item.set_tooltip_text(0, commit.get("message", ""))
		item.set_metadata(0, commit)
	_next = next
	_more_button.disabled = next.is_empty()
	_update_actions()


func _selected() -> Dictionary:
	var item := _tree.get_selected()
	return item.get_metadata(0) if item else {}


func _update_actions() -> void:
	var commit := _selected()
	_open_button.disabled = commit.is_empty()
	_compare_button.visible = is_image(_clone_path)
	_compare_button.disabled = commit.is_empty() or String(commit.get("parent", "")).is_empty()
	_detail.text = "" if commit.is_empty() else "%s by %s" % [commit.get("id", ""), commit.get("author", "")]


func _open_selected() -> void:
	var commit := _selected()
	if commit.is_empty() or _session == null:
		return
	var link: String = _session.get_commit_link(commit.get("id", ""))
	if link.is_empty():
		_detail.text = "No link for this clone."
		return
	OS.shell_open(link)


func _compare_selected() -> void:
	var commit := _selected()
	if commit.is_empty():
		return
	compare_requested.emit(
		{"source": "revision", "revision": commit.get("parent", ""), "path": _clone_path},
		{"source": "revision", "revision": commit.get("id", ""), "path": _clone_path},
		"%s at %s" % [_clone_path, commit.get("id", "")])
