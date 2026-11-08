extends GdUnitTestSuite

const SetupChecker := preload("res://addons/nipa/ui/setup_checker.gd")
const SetupRow := preload("res://addons/nipa/ui/setup_row.gd")
const Dock := preload("res://addons/nipa/ui/dock.gd")
const Decorator := preload("res://addons/nipa/filesystem_decorator.gd")

const ROWS := [
	{"id": "vcs", "level": "ok", "title": "Version control plugin", "detail": "fine", "fix": "", "fix_label": "", "paths": []},
	{"id": "ignore", "level": "warning", "title": ".nipaignore", "detail": "Missing", "fix": "write_ignore",
		"fix_label": "Create .nipaignore", "paths": []},
	{"id": "sidecars", "level": "warning", "title": "Sidecars", "detail": "1 warning(s)", "fix": "stage_sidecars",
		"fix_label": "Stage sidecars", "paths": ["a.png.import"]},
	{"id": "version", "level": "error", "title": "nipa daemon not connected", "detail": "", "fix": "", "fix_label": "",
		"paths": []},
]


class FakeRequest:
	extends RefCounted
	signal completed(ok: bool, message: String)
	var result := {}

	func is_done() -> bool:
		return true

	func is_ok() -> bool:
		return true

	func get_message() -> String:
		return ""

	func get_result() -> Dictionary:
		return result


class FakeSession:
	extends RefCounted
	signal status_changed(paths: PackedStringArray)
	signal locks_changed(paths: PackedStringArray)
	signal warnings_changed
	var warnings: Array = []
	var decorations := {}
	var asked: Array = []
	var staged: Array = []
	var ignore_writes := 0
	var setup_checks := 0

	func is_open() -> bool:
		return true

	func get_sidecar_warnings() -> Array:
		return warnings

	func get_decorations(paths: PackedStringArray) -> Dictionary:
		asked.append(paths)
		var out := {}
		for path in paths:
			if decorations.has(path):
				out[path] = decorations[path]
		return out

	func request_setup_check() -> RefCounted:
		setup_checks += 1
		var request := FakeRequest.new()
		request.result = {"rows": ROWS}
		return request

	func apply_ignore_template() -> Dictionary:
		ignore_writes += 1
		return {"ok": true, "changed": true, "message": ".nipaignore updated."}

	func request_stage(add: PackedStringArray, unstage: PackedStringArray) -> RefCounted:
		staged.append([add, unstage])
		return FakeRequest.new()


func _scene(path: String) -> Node:
	var node: Node = load(path).instantiate()
	add_child(node)
	return node


func _free(node: Node) -> void:
	remove_child(node)
	node.free()


func test_setup_summary_counts_open_items() -> void:
	assert_str(SetupChecker.summary_for([])).is_empty()
	assert_str(SetupChecker.summary_for([ROWS[0]])).is_equal("Everything is set up.")
	assert_str(SetupChecker.summary_for(ROWS)).is_equal("3 item(s) need attention.")
	assert_str(SetupRow.level_text("error")).is_equal("Error")
	assert_str(SetupRow.level_text("bogus")).is_equal("Unknown")


func test_vcs_settings_are_left_alone_when_already_selected() -> void:
	assert_str(ProjectSettings.get_setting(SetupChecker.VCS_NAME_SETTING)).is_equal("NipaVCSInterface")
	assert_bool(SetupChecker.apply_vcs_settings()).is_false()


func test_setup_checker_renders_one_row_per_check() -> void:
	var dialog: AcceptDialog = _scene("res://addons/nipa/ui/setup_checker.tscn")
	dialog.show_rows(ROWS)
	var rows: Array = dialog.get_node("Body/Scroll/Rows").get_children()
	assert_int(rows.size()).is_equal(4)
	assert_str(rows[0].get_node("LevelLabel").text).is_equal("OK")
	assert_bool(rows[0].get_node("FixButton").visible).is_false()
	assert_str(rows[1].get_node("FixButton").text).is_equal("Create .nipaignore")
	assert_bool(rows[1].get_node("FixButton").visible).is_true()
	assert_bool(rows[3].get_node("Body/DetailLabel").visible).is_false()
	assert_str(dialog.get_node("Body/Summary").text).is_equal("3 item(s) need attention.")
	dialog.show_rows([])
	await get_tree().process_frame
	assert_int(dialog.get_node("Body/Scroll/Rows").get_child_count()).is_equal(0)
	_free(dialog)


func test_setup_checker_without_a_session_says_so() -> void:
	var dialog: AcceptDialog = _scene("res://addons/nipa/ui/setup_checker.tscn")
	dialog.refresh()
	assert_str(dialog.get_node("Body/Summary").text).contains("Not connected")
	_free(dialog)


func test_setup_checker_fixes_go_through_the_session() -> void:
	var dialog: AcceptDialog = _scene("res://addons/nipa/ui/setup_checker.tscn")
	var fake := FakeSession.new()
	dialog.session = fake
	dialog.refresh()
	assert_int(fake.setup_checks).is_equal(1)
	assert_int(dialog.get_node("Body/Scroll/Rows").get_child_count()).is_equal(4)

	var rows: Array = dialog.get_node("Body/Scroll/Rows").get_children()
	rows[1].get_node("FixButton").pressed.emit()
	await get_tree().process_frame
	assert_int(fake.ignore_writes).is_equal(1)
	assert_int(fake.setup_checks).is_equal(2)
	assert_str(dialog.get_node("Body/Summary").text).starts_with(".nipaignore updated.")

	rows = dialog.get_node("Body/Scroll/Rows").get_children()
	rows[2].get_node("FixButton").pressed.emit()
	await get_tree().process_frame
	assert_int(fake.staged.size()).is_equal(1)
	assert_array(fake.staged[0][0]).contains_exactly(["a.png.import"])
	assert_array(fake.staged[0][1]).is_empty()
	assert_int(fake.setup_checks).is_equal(3)
	dialog.session = null
	_free(dialog)


func test_dock_sidecar_banner_follows_the_warnings() -> void:
	var state := Dock.sidecar_banner([
		{"path": "a.png.import", "message": "is not staged", "stageable": true},
		{"path": "b.png", "message": "has no .import", "stageable": false},
		{"path": "a.png.import", "message": "dup", "stageable": true},
	])
	assert_bool(state.visible).is_true()
	assert_str(state.text).is_equal("3 sidecar warning(s). a.png.import is not staged")
	assert_array(state.stage_paths).contains_exactly(["a.png.import"])
	assert_bool(Dock.sidecar_banner([]).visible).is_false()

	var dock: Control = _scene("res://addons/nipa/ui/dock.tscn")
	var banner: Control = dock.get_node("SidecarBanner")
	assert_bool(banner.visible).is_false()
	var fake := FakeSession.new()
	dock._session = fake
	fake.warnings_changed.connect(dock._refresh_sidecars)
	fake.warnings = [{"path": "b.png", "message": "has no .import", "stageable": false}]
	fake.warnings_changed.emit()
	assert_bool(banner.visible).is_true()
	assert_bool(dock.get_node("SidecarBanner/Buttons/StageSidecarsButton").visible).is_false()
	fake.warnings.append({"path": "c.gd.uid", "message": "is not staged", "stageable": true})
	fake.warnings_changed.emit()
	dock.get_node("SidecarBanner/Buttons/StageSidecarsButton").pressed.emit()
	assert_array(fake.staged[0][0]).contains_exactly(["c.gd.uid"])
	fake.warnings_changed.disconnect(dock._refresh_sidecars)
	dock._session = null
	_free(dock)


func _fake_dock() -> Control:
	var dock := VBoxContainer.new()
	var tree := Tree.new()
	tree.name = "Tree"
	dock.add_child(tree)
	var root := tree.create_item()
	var res := tree.create_item(root)
	res.set_metadata(0, "res://")
	var art := tree.create_item(res)
	art.set_metadata(0, "res://art/")
	var hero := tree.create_item(art)
	hero.set_metadata(0, "res://art/hero.png")
	hero.set_tooltip_text(0, "hero")
	hero.set_custom_color(0, Color.RED)
	var clean := tree.create_item(res)
	clean.set_metadata(0, "res://clean.gd")
	var favorites := tree.create_item(root)
	favorites.set_metadata(0, "Favorites")
	var list := ItemList.new()
	list.name = "List"
	dock.add_child(list)
	list.add_item("hero.png")
	list.set_item_metadata(0, "res://art/hero.png")
	list.add_item("other.png")
	list.set_item_metadata(1, "res://art/other.png")
	var dialog := Window.new()
	dialog.visible = false
	dialog.add_child(Tree.new())
	dialog.add_child(ItemList.new())
	dock.add_child(dialog)
	add_child(dock)
	return dock


func _decorated_session() -> FakeSession:
	var fake := FakeSession.new()
	fake.decorations = {
		"res://art/": {"color": "#e3b341", "tooltip": "Nipa: 1 changed file(s)"},
		"res://art/hero.png": {"color": "#ff9f43", "tooltip": "Nipa: locked by Ann"},
	}
	return fake


func test_decorator_finds_only_the_docks_own_tree_and_list() -> void:
	var dock := _fake_dock()
	var targets := Decorator.find_targets(dock)
	assert_str(targets.error).is_empty()
	assert_object(targets.tree).is_same(dock.get_node("Tree"))
	assert_object(targets.list).is_same(dock.get_node("List"))
	dock.add_child(Tree.new())
	assert_str(Decorator.find_targets(dock).error).contains("unexpected layout")
	assert_str(Decorator.find_targets(null).error).contains("missing")
	assert_bool(Decorator.version_supported({"major": 4, "minor": 4})).is_true()
	assert_bool(Decorator.version_supported({"major": 4, "minor": 3})).is_false()
	assert_bool(Decorator.version_supported({"major": 5, "minor": 0})).is_false()
	var decorator := Decorator.new()
	assert_str(decorator.enable(dock, FakeSession.new(), {"major": 4, "minor": 2})).contains("4.4")
	assert_bool(decorator.is_enabled()).is_false()
	_free(dock)


func test_decorator_colors_items_and_restores_them() -> void:
	var dock := _fake_dock()
	var tree: Tree = dock.get_node("Tree")
	var list: ItemList = dock.get_node("List")
	var fake := _decorated_session()
	var decorator := Decorator.new()
	assert_str(decorator.enable(dock, fake)).is_empty()
	assert_bool(decorator.is_enabled()).is_true()
	await get_tree().process_frame
	var art := tree.get_root().get_child(0).get_child(0)
	var hero := art.get_child(0)
	assert_that(art.get_custom_color(0)).is_equal(Color.html("#e3b341"))
	assert_str(art.get_tooltip_text(0)).is_equal("Nipa: 1 changed file(s)")
	assert_that(hero.get_custom_color(0)).is_equal(Color.html("#ff9f43"))
	assert_str(hero.get_tooltip_text(0)).is_equal("hero\nNipa: locked by Ann")
	assert_that(list.get_item_custom_fg_color(0)).is_equal(Color.html("#ff9f43"))
	assert_str(list.get_item_tooltip(0)).is_equal("Nipa: locked by Ann")
	assert_str(list.get_item_tooltip(1)).is_empty()
	assert_array(fake.asked[0]).contains(["res://", "res://art/", "res://clean.gd", "res://art/other.png"])
	assert_array(fake.asked[0]).not_contains(["Favorites"])

	fake.decorations.erase("res://art/")
	fake.status_changed.emit(PackedStringArray())
	await get_tree().process_frame
	assert_that(art.get_custom_color(0)).is_equal(Color())
	assert_str(art.get_tooltip_text(0)).is_empty()
	assert_str(hero.get_tooltip_text(0)).is_equal("hero\nNipa: locked by Ann")

	decorator.disable()
	assert_bool(decorator.is_enabled()).is_false()
	assert_that(hero.get_custom_color(0)).is_equal(Color.RED)
	assert_str(hero.get_tooltip_text(0)).is_equal("hero")
	assert_str(list.get_item_tooltip(0)).is_empty()
	assert_bool(fake.status_changed.is_connected(decorator._on_status_changed)).is_false()
	assert_bool(fake.locks_changed.is_connected(decorator._on_status_changed)).is_false()
	assert_bool(tree.draw.is_connected(decorator._on_drawn)).is_false()
	assert_bool(list.draw.is_connected(decorator._on_drawn)).is_false()
	_free(dock)


func test_decorator_reapplies_after_the_dock_rebuilds_its_items() -> void:
	var dock := _fake_dock()
	var tree: Tree = dock.get_node("Tree")
	var fake := _decorated_session()
	var decorator := Decorator.new()
	decorator.enable(dock, fake)
	await get_tree().process_frame
	var calls: int = fake.asked.size()
	decorator._on_drawn()
	await get_tree().process_frame
	assert_int(fake.asked.size()).is_equal(calls)

	tree.clear()
	var root := tree.create_item()
	var hero := tree.create_item(root)
	hero.set_metadata(0, "res://art/hero.png")
	decorator._on_drawn()
	await get_tree().process_frame
	assert_int(fake.asked.size()).is_equal(calls + 1)
	assert_that(hero.get_custom_color(0)).is_equal(Color.html("#ff9f43"))
	decorator.disable()
	assert_that(hero.get_custom_color(0)).is_equal(Color())
	_free(dock)
