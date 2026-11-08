extends GdUnitTestSuite

const ConflictsTab := preload("res://addons/nipa/ui/conflicts_tab.gd")
const HistoryTab := preload("res://addons/nipa/ui/history_tab.gd")
const ImageDiffTab := preload("res://addons/nipa/ui/image_diff_tab.gd")
const Dock := preload("res://addons/nipa/ui/dock.gd")
const ContextMenu := preload("res://addons/nipa/context_menu.gd")

const MERGE := {
	"op": "merge",
	"ours": {"id": "o1", "label": "main"},
	"theirs": {"id": "t1", "label": "feature"},
	"open": 1,
	"can_finish": false,
	"files": [
		{"path": "level.tscn", "kind": "text", "resolved": false, "viewer": "scene", "actions": ["mine", "theirs", "merge"]},
		{"path": "hero.png", "kind": "binary", "resolved": true, "viewer": "image", "actions": ["mine", "theirs", "compare"]},
		{"path": "player.gd", "kind": "text", "resolved": true, "viewer": "text", "actions": ["mine", "theirs", "external"]},
	],
}


func _scene(path: String) -> Control:
	var node: Control = load(path).instantiate()
	add_child(node)
	return node


func _free(node: Node) -> void:
	remove_child(node)
	node.free()


func test_conflicts_summary_per_state() -> void:
	assert_str(ConflictsTab.summary_for({})).is_equal("No merge or revert in progress.")
	assert_str(ConflictsTab.summary_for(MERGE)).is_equal("Merging feature into main: 1 of 3 file(s) still conflicted.")
	var done := MERGE.duplicate(true)
	done.open = 0
	assert_str(ConflictsTab.summary_for(done)).is_equal("Merging feature into main: all 3 file(s) resolved.")
	var revert := {"op": "revert", "base": {"label": "fix jump"}, "open": 2, "files": [{}, {}]}
	assert_str(ConflictsTab.summary_for(revert)).is_equal("Reverting fix jump: 2 of 2 file(s) still conflicted.")


func test_conflicts_tab_lists_files_and_offers_their_actions() -> void:
	var tab := _scene("res://addons/nipa/ui/conflicts_tab.tscn")
	tab.show_conflicts(MERGE)
	var tree: Tree = tab.get_node("FileTree")
	var rows := tree.get_root().get_children()
	assert_int(rows.size()).is_equal(3)
	assert_str(rows[0].get_text(2)).is_equal("conflicted")
	assert_str(rows[1].get_text(2)).is_equal("resolved")
	assert_bool(tab.get_node("Actions/MergeButton").visible).is_false()

	rows[0].select(0)
	assert_bool(tab.get_node("Actions/MineButton").visible).is_true()
	assert_bool(tab.get_node("Actions/TheirsButton").visible).is_true()
	assert_bool(tab.get_node("Actions/MergeButton").visible).is_true()
	assert_bool(tab.get_node("Actions/ExternalButton").visible).is_false()

	rows[1].select(0)
	assert_bool(tab.get_node("Actions/CompareButton").visible).is_true()
	assert_bool(tab.get_node("Actions/MergeButton").visible).is_false()

	rows[2].select(0)
	assert_bool(tab.get_node("Actions/ExternalButton").visible).is_true()

	tab.show_conflicts(MERGE)
	assert_object(tree.get_selected()).is_not_null()
	tab.show_conflicts({})
	assert_int(tree.get_root().get_child_count()).is_equal(0)
	_free(tab)


func test_conflict_lines_name_section_and_key() -> void:
	var lines := ConflictsTab.conflict_lines({"conflicts": [
		{"label": "node name=\"A\" parent=\".\" · position", "message": "changed differently on both sides"},
		{"label": "", "message": "theirs deleted the file"},
	]})
	assert_array(lines).contains_exactly([
		"node name=\"A\" parent=\".\" · position: changed differently on both sides",
		"theirs deleted the file",
	])


func test_compare_from_conflicts_uses_both_revisions() -> void:
	var tab := _scene("res://addons/nipa/ui/conflicts_tab.tscn")
	tab._session = RefCounted.new()
	tab.show_conflicts(MERGE)
	tab.get_node("FileTree").get_root().get_child(1).select(0)
	var monitor := monitor_signals(tab)
	tab._on_action("compare")
	await assert_signal(monitor).is_emitted("compare_requested", [
		{"source": "revision", "revision": "o1", "path": "hero.png"},
		{"source": "revision", "revision": "t1", "path": "hero.png"},
		"hero.png: main vs feature"])
	tab._session = null
	_free(tab)


func test_banner_state_for_merge_revert_and_tags() -> void:
	var merging := Dock.banner_state({"branch": "main", "pending": "merge"}, MERGE)
	assert_str(merging.pending).is_equal("merge")
	assert_bool(merging.can_finish).is_false()
	assert_str(merging.pending_text).is_equal("Merging feature into main. 1 conflict(s) left.")
	var resolved := MERGE.duplicate(true)
	resolved.open = 0
	assert_bool(Dock.banner_state({"branch": "main", "pending": "merge"}, resolved).can_finish).is_true()

	var reverting := Dock.banner_state({"pending": "revert", "pending_remaining": 2}, {"open": 0})
	assert_str(reverting.pending_text).is_equal("Revert in progress (2 step(s) left). All conflicts resolved.")
	assert_bool(reverting.can_finish).is_true()

	var detached := Dock.banner_state({"detached": true, "head_name": "v1.0"}, {})
	assert_bool(detached.detached).is_true()
	assert_str(detached.detached_text).contains("v1.0")
	assert_str(detached.pending).is_empty()
	assert_bool(detached.can_finish).is_false()


func test_ref_items_list_branches_then_tags() -> void:
	var items := Dock.ref_items(PackedStringArray(["main", "dev"]), PackedStringArray(["v1.0"]))
	assert_int(items.size()).is_equal(3)
	assert_dict(items[2]).is_equal({"text": "Tag: v1.0", "kind": "tag", "name": "v1.0"})


func test_dock_hides_banners_without_a_session() -> void:
	var dock := _scene("res://addons/nipa/ui/dock.tscn")
	assert_bool(dock.get_node("DetachedBanner").visible).is_false()
	assert_bool(dock.get_node("PendingBanner").visible).is_false()
	assert_bool(dock.get_node("Refs/MergeButton").disabled).is_true()
	_free(dock)


func test_history_tab_pages() -> void:
	var tab := _scene("res://addons/nipa/ui/history_tab.tscn")
	tab.add_page([
		{"id": "c3", "parent": "c2", "author": "Ann", "message": "Move the door\n\nbody", "created_at": "2026-11-08T10:11:12Z"},
		{"id": "c2", "parent": "", "author": "Bob", "message": "init", "created_at": ""},
	], "c1")
	var tree: Tree = tab.get_node("CommitTree")
	var rows := tree.get_root().get_children()
	assert_int(rows.size()).is_equal(2)
	assert_str(rows[0].get_text(0)).is_equal("Move the door")
	assert_str(rows[0].get_text(2)).is_equal("2026-11-08 10:11")
	assert_bool(tab.get_node("Toolbar/MoreButton").disabled).is_false()
	tab.add_page([], "")
	assert_bool(tab.get_node("Toolbar/MoreButton").disabled).is_true()
	assert_bool(tab.get_node("Actions/OpenButton").disabled).is_true()
	rows[0].select(0)
	assert_bool(tab.get_node("Actions/OpenButton").disabled).is_false()
	assert_bool(HistoryTab.is_image("art/Hero.PNG")).is_true()
	assert_bool(HistoryTab.is_image("level.tscn")).is_false()
	_free(tab)


func test_image_diff_tab_shows_both_sides_and_frees_them() -> void:
	var tab := _scene("res://addons/nipa/ui/image_diff_tab.tscn")
	var image := Image.create(4, 2, false, Image.FORMAT_RGBA8)
	image.fill(Color.RED)
	var png := image.save_png_to_buffer()
	tab.apply_result({
		"old_png": png,
		"new_png": png,
		"old": {"width": 4, "height": 2, "format": "png"},
		"new": {"width": 4, "height": 2, "format": "png"},
		"diff": {"changed_pixels": 2, "total_pixels": 8, "changed_ratio": 0.25, "max_delta": 40, "size_changed": true},
	})
	var old_rect: TextureRect = tab.get_node("Sides/Old/Texture")
	assert_object(old_rect.texture).is_not_null()
	assert_int(old_rect.texture.get_width()).is_equal(4)
	assert_str(tab.get_node("Stats").text).is_equal("2 of 8 pixels changed (25.00%), max channel delta 40, size changed")
	assert_str(tab.get_node("Sides/New/Label").text).is_equal("After: 4x2 png")
	tab.clear()
	assert_object(old_rect.texture).is_null()
	assert_object(ImageDiffTab.texture_from_png(PackedByteArray([1, 2, 3]))).is_null()
	assert_object(ImageDiffTab.texture_from_png(PackedByteArray())).is_null()
	assert_str(ImageDiffTab.side_text("Before", {})).is_equal("Before: none")
	assert_str(ImageDiffTab.stats_text({})).is_empty()
	_free(tab)


func test_bottom_panel_has_the_phase_3_tabs() -> void:
	var panel := _scene("res://addons/nipa/ui/bottom_panel.tscn")
	assert_int(panel.get_tab_count()).is_equal(4)
	assert_str(panel.get_tab_title(1)).is_equal("Conflicts")
	assert_str(panel.get_tab_title(3)).is_equal("Image diff")
	panel.show_image_diff({}, {"source": "working", "path": "a.png"}, "a.png")
	assert_int(panel.current_tab).is_equal(3)
	assert_str(panel.get_node("Image diff/Toolbar/Title").text).is_equal("a.png")
	_free(panel)


func test_context_menu_offers_history_and_compare() -> void:
	assert_array(ContextMenu.file_entries(PackedStringArray(["res://art/hero.png"]))).contains_exactly([ContextMenu.HISTORY, ContextMenu.COMPARE])
	assert_array(ContextMenu.file_entries(PackedStringArray(["res://level.tscn"]))).contains_exactly([ContextMenu.HISTORY])
	assert_array(ContextMenu.file_entries(PackedStringArray(["res://art/"]))).is_empty()
	assert_array(ContextMenu.file_entries(PackedStringArray(["a.png", "b.png"]))).is_empty()


class FakeRequest:
	extends RefCounted
	signal completed(ok: bool, message: String)
	var ok := false
	var message := ""

	func is_ok() -> bool:
		return ok

	func get_message() -> String:
		return message


class FakeHistorySession:
	extends RefCounted
	var pending: FakeRequest

	func is_open() -> bool:
		return true

	func request_history(_path: String, _start: String, _limit: int) -> RefCounted:
		pending = FakeRequest.new()
		return pending


class FakeResolveSession:
	extends RefCounted
	var resolved := []

	func has_conflict_markers(_content: PackedByteArray) -> bool:
		return false

	func request_resolve(path: String, choice: String, content: PackedByteArray) -> RefCounted:
		resolved.append([path, choice, content])
		return FakeRequest.new()


func test_history_load_more_recovers_after_a_failure() -> void:
	var tab := _scene("res://addons/nipa/ui/history_tab.tscn")
	var fake := FakeHistorySession.new()
	tab._session = fake
	var more: Button = tab.get_node("Toolbar/MoreButton")
	tab._load_page()
	assert_bool(more.disabled).is_true()
	fake.pending.message = "offline"
	fake.pending.completed.emit(false, "offline")
	await get_tree().process_frame
	assert_bool(more.disabled).is_false()
	assert_str(tab.get_node("Actions/Detail").text).is_equal("History failed: offline")
	tab._session = null
	_free(tab)


func test_external_tool_without_a_result_resolves_nothing() -> void:
	var tab := _scene("res://addons/nipa/ui/conflicts_tab.tscn")
	var fake := FakeResolveSession.new()
	tab._session = fake
	var pid := OS.create_process("true", [])
	assert_int(pid).is_greater(0)
	while OS.is_process_running(pid):
		await get_tree().process_frame
	tab._tool_pid = pid
	tab._tool_path = "player.gd"
	tab._tool_merged = "user://nipa-missing/player.MERGED.gd"
	tab._poll_tool()
	assert_array(fake.resolved).is_empty()
	assert_str(tab.get_node("Detail").text).contains("left no result")
	tab._session = null
	_free(tab)
