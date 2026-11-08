@tool
extends VBoxContainer

const MAX_SIZE := 1024
const DEBOUNCE_SECONDS := 0.3

var _session: Object
var _old_ref: Dictionary = {}
var _new_ref: Dictionary = {}
var _generation := 0

@onready var _title: Label = $Toolbar/Title
@onready var _threshold: HSlider = $Toolbar/Threshold
@onready var _threshold_label: Label = $Toolbar/ThresholdValue
@onready var _old_texture: TextureRect = $Sides/Old/Texture
@onready var _new_texture: TextureRect = $Sides/New/Texture
@onready var _old_label: Label = $Sides/Old/Label
@onready var _new_label: Label = $Sides/New/Label
@onready var _stats: Label = $Stats
@onready var _debounce: Timer = $Debounce


static func stats_text(diff: Dictionary) -> String:
	if diff.is_empty():
		return ""
	var total: int = diff.get("total_pixels", 0)
	var changed: int = diff.get("changed_pixels", 0)
	var text := "%d of %d pixels changed (%.2f%%), max channel delta %d" % [
		changed, total, 100.0 * float(diff.get("changed_ratio", 0.0)), diff.get("max_delta", 0)]
	if diff.get("size_changed", false):
		text += ", size changed"
	return text


static func side_text(label: String, info: Dictionary) -> String:
	if info.is_empty():
		return "%s: none" % label
	return "%s: %dx%d %s" % [label, info.get("width", 0), info.get("height", 0), info.get("format", "")]


static func texture_from_png(png: PackedByteArray) -> Texture2D:
	if png.is_empty():
		return null
	var image := Image.new()
	if image.load_png_from_buffer(png) != OK:
		return null
	return ImageTexture.create_from_image(image)


func _ready() -> void:
	_threshold.value_changed.connect(_on_threshold_changed)
	_debounce.wait_time = DEBOUNCE_SECONDS
	_debounce.timeout.connect(_request)
	_session = Engine.get_singleton("Nipa") if Engine.has_singleton("Nipa") else null
	_on_threshold_changed(_threshold.value)


func _exit_tree() -> void:
	_debounce.stop()
	clear()
	_session = null


# Compares two BlobRefs ({source, revision, path}); either may be empty.
func show_diff(old_ref: Dictionary, new_ref: Dictionary, title: String) -> void:
	_old_ref = old_ref
	_new_ref = new_ref
	_title.text = title
	clear()
	_request()


func clear() -> void:
	_generation += 1
	_old_texture.texture = null
	_new_texture.texture = null
	_stats.text = ""


func _on_threshold_changed(value: float) -> void:
	_threshold_label.text = "Threshold %d" % int(value)
	if not (_old_ref.is_empty() and _new_ref.is_empty()):
		_debounce.start()


func _request() -> void:
	if _session == null or not _session.is_open():
		_stats.text = "Not connected."
		return
	_generation += 1
	var generation := _generation
	_stats.text = "Loading preview..."
	var request: RefCounted = _session.request_preview(_old_ref, _new_ref, int(_threshold.value), MAX_SIZE)
	await request.completed
	if not is_inside_tree() or generation != _generation:
		return
	if not request.is_ok():
		_stats.text = "No preview: %s" % request.get_message()
		return
	apply_result(request.get_result())


func apply_result(result: Dictionary) -> void:
	_old_texture.texture = texture_from_png(result.get("old_png", PackedByteArray()))
	_new_texture.texture = texture_from_png(result.get("new_png", PackedByteArray()))
	_old_label.text = side_text("Before", result.get("old", {}))
	_new_label.text = side_text("After", result.get("new", {}))
	_stats.text = stats_text(result.get("diff", {}))
