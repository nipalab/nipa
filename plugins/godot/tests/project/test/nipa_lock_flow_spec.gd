extends GdUnitTestSuite

const LockWatch := preload("res://addons/nipa/lock_watch.gd")
const ContextMenu := preload("res://addons/nipa/context_menu.gd")
const ReleaseDialog := preload("res://addons/nipa/ui/release_request_dialog.gd")
const Plugin := preload("res://addons/nipa/nipa_plugin.gd")
const Dock := preload("res://addons/nipa/ui/dock.gd")
const ConflictsTab := preload("res://addons/nipa/ui/conflicts_tab.gd")
const ShelvesTab := preload("res://addons/nipa/ui/shelves_tab.gd")
const BottomPanel := preload("res://addons/nipa/ui/bottom_panel.gd")


func test_lock_watch_matches_exact_folder_and_sidecar_releases() -> void:
	var watched := PackedStringArray(["art/hero.png", "levels/one.tscn", "audio/a.wav.import"])
	assert_array(LockWatch.hits(watched, "art/hero.png")).contains_exactly(["art/hero.png"])
	assert_array(LockWatch.hits(watched, "art/hero.png.import")).contains_exactly(["art/hero.png"])
	assert_array(LockWatch.hits(watched, "levels")).contains_exactly(["levels/one.tscn"])
	assert_array(LockWatch.hits(watched, "levels/")).contains_exactly(["levels/one.tscn"])
	assert_array(LockWatch.hits(watched, "audio/a.wav")).contains_exactly(["audio/a.wav.import"])
	assert_array(LockWatch.hits(watched, "art/hero")).is_empty()
	assert_array(LockWatch.hits(watched, "art/hero.png2")).is_empty()
	assert_array(LockWatch.hits(watched, "")).is_empty()
	assert_str(LockWatch.asset_of("a.png.uid")).is_equal("a.png")
	assert_str(LockWatch.asset_of(".import")).is_equal(".import")


func test_lock_watch_adds_removes_and_takes_hits_once() -> void:
	var watches = LockWatch.new()
	var changes := [0]
	watches.changed.connect(func(): changes[0] += 1)
	watches.watch("art/hero.png")
	watches.watch("art/hero.png")
	watches.watch("")
	watches.watch("levels/one.tscn")
	assert_bool(watches.is_watched("art/hero.png")).is_true()
	assert_int(changes[0]).is_equal(2)
	assert_array(watches.take_hits("art/hero.png.import")).contains_exactly(["art/hero.png"])
	assert_array(watches.take_hits("art/hero.png")).is_empty()
	assert_array(watches.paths()).contains_exactly(["levels/one.tscn"])
	watches.unwatch("missing")
	watches.unwatch("levels/one.tscn")
	assert_array(watches.paths()).is_empty()
	assert_int(changes[0]).is_equal(4)


func test_context_menu_offers_ask_and_watch_for_theirs_only() -> void:
	assert_array(ContextMenu.theirs_entries(PackedStringArray(), PackedStringArray())).is_empty()
	assert_array(ContextMenu.theirs_entries(PackedStringArray(["a.png"]), PackedStringArray())) \
		.contains_exactly([ContextMenu.ASK, ContextMenu.WATCH])
	assert_array(ContextMenu.theirs_entries(PackedStringArray(["a.png", "b.png"]), PackedStringArray(["a.png"]))) \
		.contains_exactly([ContextMenu.ASK, ContextMenu.WATCH])
	assert_array(ContextMenu.theirs_entries(PackedStringArray(["a.png"]), PackedStringArray(["a.png"]))) \
		.contains_exactly([ContextMenu.ASK, ContextMenu.UNWATCH])


func test_release_dialog_lists_paths_and_trims_the_note() -> void:
	var dialog = ReleaseDialog.new()
	add_child(dialog)
	assert_str(ReleaseDialog.intro_for({"art/a.png": "Ann"})).contains("art/a.png (locked by Ann)")
	dialog.open(PackedStringArray(["art/a.png"]), {"art/a.png": "Ann"})
	assert_array(dialog.clone_paths).contains_exactly(["art/a.png"])
	dialog._message.text = "  need it  "
	assert_str(dialog.message()).is_equal("need it")
	dialog._on_confirmed()
	dialog.hide()
	remove_child(dialog)
	dialog.free()


func test_prompt_texts_name_paths_people_and_notes() -> void:
	assert_str(Plugin.free_prompt_text(PackedStringArray(["a.png"]), "Ann")) \
		.is_equal("a.png is free now (released by Ann). Lock it?")
	assert_str(Plugin.free_prompt_text(PackedStringArray(["a.png", "b.png"]), "")) \
		.is_equal("a.png, b.png are free now. Lock them?")
	var text: String = Plugin.release_prompt_text("art/a.png", "Bo", " boss fight ")
	assert_str(text).contains("Bo asks you to release your lock on art/a.png.")
	assert_str(text).contains("\"boss fight\"")
	assert_str(Plugin.release_prompt_text("a.png", "", "")).starts_with("A teammate asks")
	assert_str(Plugin.release_prompt_text("a.png", "", "")).not_contains("\"")


func test_sync_guard_text_caps_the_list() -> void:
	var few: String = Dock.sync_guard_text("Update", ["a.gd", "b.tscn"])
	assert_str(few).contains("Update would replace 2 local change(s):\na.gd\nb.tscn")
	var many := []
	for i in 13:
		many.append("f%d.gd" % i)
	var capped: String = Dock.sync_guard_text("Switch to dev", many)
	assert_str(capped).contains("f9.gd\n... and 3 more")
	assert_str(capped).not_contains("f10.gd")
	assert_str(Dock.shelf_name("Switch to dev")).is_equal("Before switch to dev")


func test_sync_guard_step_fails_closed() -> void:
	assert_str(Dock.sync_guard_step(true, true, "", [])).is_equal(Dock.SYNC_RUN)
	assert_str(Dock.sync_guard_step(true, true, "", ["a.gd"])).is_equal(Dock.SYNC_CONFIRM)
	assert_str(Dock.sync_guard_step(true, false, "daemon unavailable", [])).is_equal(Dock.SYNC_UNCHECKED)
	assert_str(Dock.sync_guard_step(true, false, "", [])).is_equal(Dock.SYNC_UNCHECKED)
	for message in Dock.SYNC_CANCELLED_MESSAGES:
		assert_str(Dock.sync_guard_step(true, false, message, [])).is_equal(Dock.SYNC_STOP)
	assert_str(Dock.sync_guard_step(false, true, "", [])).is_equal(Dock.SYNC_STOP)
	assert_str(Dock.sync_guard_step(false, false, "daemon unavailable", [])).is_equal(Dock.SYNC_STOP)
	var text: String = Dock.sync_unchecked_text("Switch to dev", "daemon unavailable")
	assert_str(text).is_equal("Could not check your local changes before: Switch to dev\ndaemon unavailable\n\nIf you continue, local changes may be overwritten.")
	assert_str(Dock.sync_unchecked_text("Update", "")).contains("The check did not answer.")


func test_banner_and_conflicts_describe_a_pending_shelf() -> void:
	var state: Dictionary = Dock.banner_state({"pending": "shelf"}, {"open": 1})
	assert_str(state.pending_text).contains("Applying a shelf. 1 conflict(s) left.")
	assert_bool(state.can_finish).is_false()
	assert_bool(Dock.banner_state({"pending": "shelf"}, {"open": 0}).can_finish).is_false()
	assert_bool(Dock.banner_state({"pending": "merge"}, {"open": 0}).can_finish).is_true()
	var shelf := {"op": "shelf", "open": 1, "files": [{}, {}], "theirs": {"label": "wip"}}
	assert_str(ConflictsTab.summary_for(shelf)).is_equal("Applying shelf wip: 1 of 2 file(s) still conflicted.")
	shelf.theirs = {}
	assert_str(ConflictsTab.summary_for(shelf)).starts_with("Applying a shelf:")


func test_dock_guard_without_changes_runs_the_action_at_once() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	var ran := [false]
	dock._guard_action = func(): ran[0] = true
	dock._guard_paths = PackedStringArray(["a.gd"])
	dock._on_shelve_canceled()
	assert_bool(dock._guard_action.is_valid()).is_false()
	assert_array(dock._guard_paths).is_empty()
	assert_bool(ran[0]).is_false()
	assert_str(dock._shelve_dialog.ok_button_text).is_equal("Shelve and continue")
	remove_child(dock)
	dock.free()


func test_shelves_tab_lists_shelves_and_gates_actions() -> void:
	var tab = ShelvesTab.new()
	add_child(tab)
	assert_str(tab._summary.text).starts_with("No shelves.")
	assert_bool(tab._apply_button.disabled).is_true()
	assert_bool(tab._abort_button.visible).is_false()
	var shelves := [
		{"id": "s1", "name": "Before update", "branch": "main", "tag": "", "created_at": 1700000000,
			"entries": [{"path": "a.gd", "status": "modified"}]},
		{"id": "s2", "name": "", "branch": "", "tag": "v1", "created_at": 0, "entries": []},
	]
	tab.show_shelves(shelves, "")
	assert_str(tab._summary.text).is_equal("2 shelf(s).")
	var first: TreeItem = tab._tree.get_root().get_first_child()
	assert_str(first.get_text(1)).is_equal("1 file(s) on main")
	assert_str(first.get_text(2)).is_equal("2023-11-14 22:13")
	assert_str(first.get_tooltip_text(0)).is_equal("a.gd (modified)")
	assert_str(first.get_next().get_text(0)).is_equal("s2")
	assert_str(first.get_next().get_text(1)).is_equal("0 file(s) on tag v1")
	first.select(0)
	assert_bool(tab._apply_button.disabled).is_false()
	tab.show_shelves(shelves, "s1")
	assert_str(tab._summary.text).contains("waits on conflicts")
	assert_bool(tab._apply_button.disabled).is_true()
	assert_bool(tab._delete_button.disabled).is_true()
	assert_bool(tab._abort_button.visible).is_true()
	tab._on_delete()
	assert_str(tab._delete_dialog.dialog_text).contains("Delete shelf \"Before update\"")
	tab._delete_dialog.hide()
	assert_str(ShelvesTab.describe({"entries": [1, 2]})).is_equal("2 file(s)")
	assert_str(ShelvesTab.created_text(0)).is_empty()
	remove_child(tab)
	tab.free()


func test_shelves_tab_tracks_only_shelf_operations() -> void:
	var tab = ShelvesTab.new()
	add_child(tab)
	tab._stale = false
	tab._on_op_queued(1, "push")
	tab._on_op_queued(2, "shelve")
	tab._on_op_finished(1, true, "", PackedStringArray())
	assert_bool(tab._stale).is_false()
	tab.visible = false
	tab._on_op_finished(2, true, "", PackedStringArray())
	assert_bool(tab._stale).is_true()
	assert_bool(tab._ops.is_empty()).is_true()
	remove_child(tab)
	tab.free()


func test_bottom_panel_has_shelves_and_lock_request_actions() -> void:
	var panel: Control = load("res://addons/nipa/ui/bottom_panel.tscn").instantiate()
	add_child(panel)
	assert_object(panel.shelves_tab).is_not_null()
	assert_str(panel.shelves_tab.name).is_equal("Shelves")
	assert_bool(panel._ask_button.disabled).is_true()
	var watches = LockWatch.new()
	panel.watches = watches
	assert_dict(BottomPanel.theirs_actions({}, false)).contains_key_value("theirs", false)
	assert_dict(BottomPanel.theirs_actions({"mine": true}, false)).contains_key_value("theirs", false)
	assert_dict(BottomPanel.theirs_actions({"path": "a.png"}, true)).contains_key_value("watch_text", "Stop notifying")
	var root: TreeItem = panel._tree.get_root()
	var item: TreeItem = panel._tree.create_item(root)
	item.set_metadata(0, {"path": "a.png", "holder": "Ann", "mine": false})
	item.select(0)
	assert_bool(panel._ask_button.disabled).is_false()
	assert_str(panel._watch_button.text).is_equal("Notify me when free")
	var asked := []
	panel.release_requested.connect(func(paths, holders): asked.append([paths, holders]))
	panel._on_ask_pressed()
	assert_array(asked[0][0]).contains_exactly(["a.png"])
	assert_dict(asked[0][1]).contains_key_value("a.png", "Ann")
	panel._on_watch_pressed()
	assert_bool(watches.is_watched("a.png")).is_true()
	assert_str(panel._watch_button.text).is_equal("Stop notifying")
	panel._on_watch_pressed()
	assert_bool(watches.is_watched("a.png")).is_false()
	panel.watches = null
	remove_child(panel)
	panel.free()


class RefusedRequest extends RefCounted:
	signal completed

	func is_done() -> bool:
		return true

	func is_ok() -> bool:
		return false

	func get_message() -> String:
		return "session is not open"

	func get_result() -> Dictionary:
		return {}


class ClosedSession extends RefCounted:
	var calls := []

	func is_open() -> bool:
		return true

	func request_shelve(name: String, paths: PackedStringArray) -> RefCounted:
		calls.append(["shelve", name, paths])
		return RefusedRequest.new()

	func request_shelve_apply(id: String, keep: bool) -> RefCounted:
		calls.append(["apply", id, keep])
		return RefusedRequest.new()

	func request_shelves() -> RefCounted:
		calls.append(["list"])
		return RefusedRequest.new()


func test_shelve_and_continue_does_not_wait_on_a_refused_request() -> void:
	var dock: Control = load("res://addons/nipa/ui/dock.tscn").instantiate()
	add_child(dock)
	var session := ClosedSession.new()
	dock._session = session
	var ran := [false]
	dock._guard_action = func(): ran[0] = true
	dock._guard_label = "Update"
	dock._guard_paths = PackedStringArray(["a.gd"])
	await dock._on_shelve_confirmed()
	assert_array(session.calls).contains_exactly([["shelve", "Before update", PackedStringArray(["a.gd"])]])
	assert_bool(ran[0]).is_false()
	assert_bool(dock._guard_action.is_valid()).is_false()
	dock._session = null
	remove_child(dock)
	dock.free()


func test_shelves_tab_reports_refused_apply_and_list() -> void:
	var tab = ShelvesTab.new()
	add_child(tab)
	var session := ClosedSession.new()
	tab._session = session
	tab.show_shelves([{"id": "s1", "name": "wip", "entries": []}], "")
	tab._tree.get_root().get_first_child().select(0)
	var conflicts := [0]
	tab.conflicts_requested.connect(func(): conflicts[0] += 1)
	await tab._on_apply(true)
	assert_str(tab._summary.text).is_equal("Could not apply the shelf: session is not open")
	assert_int(conflicts[0]).is_equal(0)
	await tab.refresh_from_daemon()
	assert_str(tab._summary.text).is_equal("Could not list shelves: session is not open")
	assert_array(session.calls).contains_exactly([["apply", "s1", true], ["list"]])
	tab._session = null
	remove_child(tab)
	tab.free()
