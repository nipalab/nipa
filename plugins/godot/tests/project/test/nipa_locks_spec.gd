extends GdUnitTestSuite

const ContextMenu := preload("res://addons/nipa/context_menu.gd")
const LockBadge := preload("res://addons/nipa/ui/lock_badge.gd")
const NipaPlugin := preload("res://addons/nipa/nipa_plugin.gd")


class FakeSession:
	extends RefCounted
	signal locks_changed(paths: PackedStringArray)
	var locks: Array = []

	func is_open() -> bool:
		return true

	func get_locks() -> Array:
		return locks


func test_context_menu_offers_lock_for_free_files() -> void:
	assert_array(ContextMenu.entries_for([{"state": "free"}, {"state": "free"}])).contains_exactly([ContextMenu.LOCK])


func test_context_menu_offers_unlock_for_my_files() -> void:
	assert_array(ContextMenu.entries_for([{"state": "mine"}])).contains_exactly([ContextMenu.UNLOCK])


func test_context_menu_names_the_holders_of_locked_files() -> void:
	var entries := ContextMenu.entries_for([
		{"state": "theirs", "holder": "Ann"},
		{"state": "theirs", "holder": "Ann"},
		{"state": "theirs", "holder": "Bob"},
	])
	assert_array(entries).contains_exactly(["Nipa: Locked by Ann", "Nipa: Locked by Bob"])


func test_context_menu_mixed_selection() -> void:
	var entries := ContextMenu.entries_for([{"state": "mine"}, {"state": "free"}, {"state": "theirs", "holder": "Ann"}])
	assert_array(entries).contains_exactly([ContextMenu.UNLOCK, ContextMenu.LOCK, "Nipa: Locked by Ann"])
	assert_array(ContextMenu.entries_for([])).is_empty()


func test_badge_text_per_lock_state() -> void:
	assert_str(LockBadge.describe({"state": "mine"})).is_equal("Locked by you")
	assert_str(LockBadge.describe({"state": "theirs", "holder": "Ann"})).is_equal("Locked by Ann")
	assert_str(LockBadge.describe({})).is_equal("Not locked")
	assert_str(LockBadge.action_for({"state": "mine"})).is_equal("Unlock")
	assert_str(LockBadge.action_for({"state": "free"})).is_equal("Lock")
	assert_str(LockBadge.action_for({"state": "theirs"})).is_empty()


func test_edit_action_locks_free_files_warns_on_theirs() -> void:
	assert_str(NipaPlugin.edit_action({"state": "free", "relevant": true}, true)).is_equal("lock")
	assert_str(NipaPlugin.edit_action({"state": "free", "relevant": true}, false)).is_empty()
	assert_str(NipaPlugin.edit_action({"state": "theirs", "relevant": true}, false)).is_equal("warn")
	assert_str(NipaPlugin.edit_action({"state": "mine", "relevant": true}, true)).is_empty()
	assert_str(NipaPlugin.edit_action({"state": "theirs", "relevant": false}, true)).is_empty()


func test_lock_settings_defaults() -> void:
	assert_bool(NipaSettings.auto_lock()).is_true()
	assert_bool(NipaSettings.strict_read_only()).is_false()
	assert_array(NipaSettings.lock_extensions()).contains(["tscn", "png"])


func test_dock_reacts_to_locks_changed() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	var label: Label = dock.get_node("LocksLabel")
	assert_str(label.text).is_equal("Locks: 0 yours, 0 by others")

	var fake := FakeSession.new()
	dock._session = fake
	fake.locks_changed.connect(dock._on_locks_changed)
	fake.locks = [{"mine": true}, {"mine": false}, {"mine": false}]
	fake.locks_changed.emit(PackedStringArray())
	assert_str(label.text).is_equal("Locks: 1 yours, 2 by others")

	fake.locks_changed.disconnect(dock._on_locks_changed)
	dock._session = null
	remove_child(dock)
	dock.free()


func test_dock_lock_commit_dialog_lists_the_files() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	dock._on_commit_locks_needed("msg", PackedStringArray(["art/a.png", "art/b.png"]))
	var dialog: ConfirmationDialog = dock.get_node("LockCommitDialog")
	assert_bool(dialog.visible).is_true()
	assert_str(dialog.ok_button_text).is_equal("Lock 2 file(s) and commit")
	assert_str(dialog.dialog_text).contains("art/b.png")
	dialog.hide()
	remove_child(dock)
	dock.free()


func test_bottom_panel_lists_locks() -> void:
	var panel: Control = load("res://addons/nipa/ui/bottom_panel.tscn").instantiate()
	add_child(panel)
	assert_str(panel.get_node("Locks/Toolbar/Summary").text).is_equal("0 lock(s), 0 yours")
	assert_bool(panel.get_node("Locks/Toolbar/ReleaseButton").disabled).is_true()
	remove_child(panel)
	panel.free()
