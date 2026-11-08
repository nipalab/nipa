extends SceneTree


func _init() -> void:
	var listed := ClassDB.class_exists("NipaVCSInterface") and ClassDB.is_parent_class("NipaVCSInterface", "EditorVCSInterface")
	var singleton := Engine.has_singleton("Nipa")
	print("NIPA_LOAD vcs_listed=%s singleton=%s" % [listed, singleton])
	quit(0 if listed and singleton else 1)
