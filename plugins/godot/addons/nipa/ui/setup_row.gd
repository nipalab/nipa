@tool
extends HBoxContainer

signal fix_requested(row: Dictionary)

const LEVEL_TEXT := {"ok": "OK", "warning": "Warning", "error": "Error", "unknown": "Unknown"}

var row := {}

@onready var _level: Label = $LevelLabel
@onready var _title: Label = $Body/TitleLabel
@onready var _detail: Label = $Body/DetailLabel
@onready var _fix: Button = $FixButton


func _ready() -> void:
	_fix.pressed.connect(func(): fix_requested.emit(row))


static func level_text(level: String) -> String:
	return LEVEL_TEXT.get(level, "Unknown")


func show_row(data: Dictionary) -> void:
	row = data
	_level.text = level_text(data.get("level", ""))
	_title.text = data.get("title", "")
	_detail.text = data.get("detail", "")
	_detail.visible = not _detail.text.is_empty()
	var fix: String = data.get("fix", "")
	_fix.visible = not fix.is_empty()
	_fix.text = data.get("fix_label", "Fix")
	_fix.disabled = false


func set_busy(busy: bool) -> void:
	_fix.disabled = busy
