@tool
extends RefCounted

# Clone paths whose lock release raises a "free now" prompt, kept per project
# in the editor metadata (.godot/editor/project_metadata.cfg).

signal changed

const SECTION := "nipa"
const KEY := "lock_watches"

var _paths := PackedStringArray()
var _settings: EditorSettings


static func covers(lock_path: String, path: String) -> bool:
	var base := lock_path.trim_suffix("/")
	return path == base or path.begins_with(base + "/") or base.is_empty()


# An asset and its .import/.uid sidecars are one lock unit.
static func asset_of(path: String) -> String:
	for suffix in [".import", ".uid"]:
		if path.ends_with(suffix) and path.length() > suffix.length():
			return path.left(path.length() - suffix.length())
	return path


# The watched paths a release of `released_path` frees.
static func hits(watched: PackedStringArray, released_path: String) -> PackedStringArray:
	var out := PackedStringArray()
	if released_path.is_empty():
		return out
	var released := asset_of(released_path)
	for path in watched:
		if covers(released_path, path) or asset_of(path) == released:
			out.append(path)
	return out


func _init(settings: EditorSettings = null) -> void:
	_settings = settings
	if _settings:
		_paths = PackedStringArray(_settings.get_project_metadata(SECTION, KEY, PackedStringArray()))


func paths() -> PackedStringArray:
	return _paths


func is_watched(clone_path: String) -> bool:
	return _paths.has(clone_path)


func watch(clone_path: String) -> void:
	if clone_path.is_empty() or _paths.has(clone_path):
		return
	_paths.append(clone_path)
	_save()


func unwatch(clone_path: String) -> void:
	var index := _paths.find(clone_path)
	if index < 0:
		return
	_paths.remove_at(index)
	_save()


# Removes and returns the watches a release of `released_path` frees.
func take_hits(released_path: String) -> PackedStringArray:
	var found := hits(_paths, released_path)
	for path in found:
		_paths.remove_at(_paths.find(path))
	if not found.is_empty():
		_save()
	return found


func _save() -> void:
	if _settings:
		_settings.set_project_metadata(SECTION, KEY, _paths)
	changed.emit()
