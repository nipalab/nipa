@tool
extends EditorContextMenuPlugin

const LOCK := "Nipa: Lock"
const UNLOCK := "Nipa: Unlock"

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
