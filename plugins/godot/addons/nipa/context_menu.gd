@tool
extends EditorContextMenuPlugin

signal history_requested(clone_path: String)
signal compare_requested(old_ref: Dictionary, new_ref: Dictionary, title: String)

const LOCK := "Nipa: Lock"
const UNLOCK := "Nipa: Unlock"
const HISTORY := "Nipa: History"
const COMPARE := "Nipa: Compare with last pushed"
const IMAGE_EXTENSIONS := ["png", "jpg", "jpeg", "webp", "bmp", "tga", "exr", "hdr", "dds", "ktx2", "psd", "svg"]

var slot := CONTEXT_SLOT_FILESYSTEM

var _clone_paths := PackedStringArray()
var _infos: Array = []


static func entries_for(infos: Array) -> PackedStringArray:
	var out := PackedStringArray()
	var holders := PackedStringArray()
	for info in infos:
		match info.get("state", "free"):
			"free":
				if not out.has(LOCK):
					out.append(LOCK)
			"mine":
				if not out.has(UNLOCK):
					out.append(UNLOCK)
			"theirs":
				var holder: String = info.get("holder", "")
				if not holders.has(holder):
					holders.append(holder)
	for holder in holders:
		out.append("Nipa: Locked by %s" % holder)
	return out


# History and compare entries for a single selected file (FileSystem dock only).
static func file_entries(paths: PackedStringArray) -> PackedStringArray:
	var out := PackedStringArray()
	if paths.size() != 1 or paths[0].ends_with("/"):
		return out
	out.append(HISTORY)
	if IMAGE_EXTENSIONS.has(paths[0].get_extension().to_lower()):
		out.append(COMPARE)
	return out


func _popup_menu(paths: PackedStringArray) -> void:
	var session := _session()
	_clone_paths = PackedStringArray()
	_infos = []
	if session == null or not session.is_open():
		return
	for path in paths:
		var clone_path: String = session.to_clone_path(path)
		_clone_paths.append(clone_path)
		_infos.append(session.get_lock_info(clone_path))
	for entry in entries_for(_infos):
		match entry:
			LOCK:
				add_context_menu_item(entry, _on_lock)
			UNLOCK:
				add_context_menu_item(entry, _on_unlock)
			_:
				add_context_menu_item(entry, _on_locked_by)
	if slot != CONTEXT_SLOT_FILESYSTEM:
		return
	for entry in file_entries(paths):
		add_context_menu_item(entry, _on_history if entry == HISTORY else _on_compare)


func _session() -> Object:
	return Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null


func _paths_in(state: String) -> PackedStringArray:
	var out := PackedStringArray()
	for i in _clone_paths.size():
		if _infos[i].get("state", "free") == state:
			out.append(_clone_paths[i])
	return out


func _on_lock(_arg: Variant) -> void:
	var session := _session()
	if session:
		session.request_lock(_paths_in("free"))


func _on_unlock(_arg: Variant) -> void:
	var session := _session()
	if session:
		session.request_unlock(_paths_in("mine"), false)


func _on_locked_by(_arg: Variant) -> void:
	var lines := PackedStringArray()
	for i in _clone_paths.size():
		if _infos[i].get("state", "free") == "theirs":
			lines.append("%s is locked by %s" % [_clone_paths[i], _infos[i].get("holder", "")])
	EditorInterface.get_editor_toaster().push_toast("\n".join(lines), EditorToaster.SEVERITY_WARNING)


func _on_history(_arg: Variant) -> void:
	if _clone_paths.size() == 1:
		history_requested.emit(_clone_paths[0])


func _on_compare(_arg: Variant) -> void:
	if _clone_paths.size() == 1:
		var path := _clone_paths[0]
		compare_requested.emit({"source": "snapshot", "path": path}, {"source": "working", "path": path},
			"%s: last pushed vs working copy" % path)
