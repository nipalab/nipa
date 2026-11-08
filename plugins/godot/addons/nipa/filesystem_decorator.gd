@tool
extends RefCounted

# Opt-in status colors and tooltips on the FileSystem dock's Tree and ItemList.
# Only item colors and tooltips are touched, and the originals come back on
# disable(). Re-applies when the dock rebuilds its items (checked on draw, O(1)).

const MIN_MAJOR := 4
const MIN_MINOR := 4

var _session: Object
var _tree: Tree
var _list: ItemList
var _tree_saved := {}  # TreeItem instance id -> [path, color, tooltip]
var _list_saved := {}  # index -> [path, color, tooltip]
var _tree_signature := []
var _list_signature := []
var _queued := false


static func version_supported(info: Dictionary) -> bool:
	return info.get("major", 0) == MIN_MAJOR and info.get("minor", 0) >= MIN_MINOR


# The dock's own Tree and optional ItemList; dialogs the dock owns live in other windows.
static func find_targets(dock: Node) -> Dictionary:
	if dock == null:
		return {"error": "the FileSystem dock is missing"}
	var window := dock.get_window()
	var trees := dock.find_children("*", "Tree", true, false).filter(func(n): return n.get_window() == window)
	var lists := dock.find_children("*", "ItemList", true, false).filter(func(n): return n.get_window() == window)
	if trees.size() != 1 or lists.size() > 1:
		return {"error": "the FileSystem dock has an unexpected layout"}
	return {"error": "", "tree": trees[0], "list": lists[0] if lists.size() == 1 else null}


func enable(dock: Node, session: Object, version_info: Dictionary = Engine.get_version_info()) -> String:
	if not version_supported(version_info):
		return "needs Godot %d.%d or later" % [MIN_MAJOR, MIN_MINOR]
	var targets := find_targets(dock)
	if not targets.error.is_empty():
		return targets.error
	_tree = targets.tree
	_list = targets.list
	_session = session
	_tree.draw.connect(_on_drawn)
	if _list:
		_list.draw.connect(_on_drawn)
	if _session:
		_session.status_changed.connect(_on_status_changed)
		_session.locks_changed.connect(_on_status_changed)
	refresh()
	return ""


func disable() -> void:
	if _session:
		for pair in [[_session.status_changed, _on_status_changed], [_session.locks_changed, _on_status_changed]]:
			if pair[0].is_connected(pair[1]):
				pair[0].disconnect(pair[1])
	for node in [_tree, _list]:
		if node and is_instance_valid(node) and node.draw.is_connected(_on_drawn):
			node.draw.disconnect(_on_drawn)
	restore()
	_session = null
	_tree = null
	_list = null


func is_enabled() -> bool:
	return _tree != null


func refresh() -> void:
	if _queued or _tree == null:
		return
	_queued = true
	apply.call_deferred()


func apply() -> void:
	_queued = false
	if _tree == null or not is_instance_valid(_tree):
		return
	restore()
	var tree_items := _tree_items()
	var paths := PackedStringArray()
	for item in tree_items:
		paths.append(item.get_metadata(0))
	if _list_ok():
		for i in _list.item_count:
			if _is_res_path(_list.get_item_metadata(i)):
				paths.append(_list.get_item_metadata(i))
	var decorations: Dictionary = _session.get_decorations(paths) if _session else {}
	for item in tree_items:
		var found: Dictionary = decorations.get(item.get_metadata(0), {})
		if found.is_empty():
			continue
		_tree_saved[item.get_instance_id()] = [item.get_metadata(0), item.get_custom_color(0), item.get_tooltip_text(0)]
		item.set_custom_color(0, Color.html(found.color))
		item.set_tooltip_text(0, _with_note(item.get_tooltip_text(0), found.tooltip))
	if _list_ok():
		for i in _list.item_count:
			var found: Dictionary = decorations.get(_list.get_item_metadata(i), {}) if _is_res_path(_list.get_item_metadata(i)) else {}
			if found.is_empty():
				continue
			_list_saved[i] = [_list.get_item_metadata(i), _list.get_item_custom_fg_color(i), _list.get_item_tooltip(i)]
			_list.set_item_custom_fg_color(i, Color.html(found.color))
			_list.set_item_tooltip(i, _with_note(_list.get_item_tooltip(i), found.tooltip))
	_tree_signature = _tree_state()
	_list_signature = _list_state()


# Puts back the colors and tooltips of the items that still show the same path.
func restore() -> void:
	for id in _tree_saved:
		var item := instance_from_id(id) as TreeItem
		var saved: Array = _tree_saved[id]
		if item == null or item.get_metadata(0) != saved[0]:
			continue
		if saved[1] == Color():
			item.clear_custom_color(0)
		else:
			item.set_custom_color(0, saved[1])
		item.set_tooltip_text(0, saved[2])
	_tree_saved.clear()
	if _list_ok():
		for i in _list_saved:
			var saved: Array = _list_saved[i]
			if i < _list.item_count and _list.get_item_metadata(i) == saved[0]:
				_list.set_item_custom_fg_color(i, saved[1])
				_list.set_item_tooltip(i, saved[2])
	_list_saved.clear()


func _on_status_changed(_paths: PackedStringArray) -> void:
	refresh()


func _on_drawn() -> void:
	if _tree_state() != _tree_signature or _list_state() != _list_signature:
		refresh()


func _list_ok() -> bool:
	return _list != null and is_instance_valid(_list)


# Cheap fingerprints: a rebuilt Tree has a new root, a rebuilt list loses the first decorated color.
func _tree_state() -> Array:
	if _tree == null or not is_instance_valid(_tree) or _tree.get_root() == null:
		return []
	return [_tree.get_root().get_instance_id()]


func _list_state() -> Array:
	if not _list_ok():
		return []
	var state := [_list.item_count, _list.get_item_metadata(0) if _list.item_count > 0 else null]
	for i in _list_saved:
		state.append(_list.get_item_custom_fg_color(i) if i < _list.item_count else null)
		break
	return state


func _tree_items() -> Array:
	var out := []
	var item := _tree.get_root()
	while item:
		if _is_res_path(item.get_metadata(0)):
			out.append(item)
		item = item.get_next_in_tree()
	return out


static func _is_res_path(value: Variant) -> bool:
	return value is String and value.begins_with("res://")


static func _with_note(tooltip: String, note: String) -> String:
	return note if tooltip.is_empty() else "%s\n%s" % [tooltip, note]
