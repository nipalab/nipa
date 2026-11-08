# Changelog

## Unreleased

### Phase 3: conflicts, tags, merges, previews

- Core `conflicts` (conflict set, per-file actions, merge tool command),
  `history` (tags, paged log, `nipa://` links) and `preview` (PNG assembly);
  session ops for merge, revert steps, conflicts, resolve, the daemon's
  `MergeGodotResource`, history and previews, with results in `get_result()`.
- Dock branch/tag switcher, Merge branch..., detached-tag and pending
  merge/revert banners; Conflicts, History and Image diff tabs; FileSystem
  menu History and Compare with last pushed; `nipa/merge_tool` setting.

### Phase 1: extension MVP

- Engine-free C++17 core (`core/`): daemon client and connector, project paths
  and sidecars, status mapping, diff model with Myers line diff, and the
  session (worker thread, stream loops, events).
- godot-cpp GDExtension (`extension/`): `NipaVCSInterface` (native VCS plugin),
  the `Nipa` engine singleton and `NipaRequest`.
- GDScript add-on (`addons/nipa/`): plugin, settings, and the Nipa dock.
- Headless smoke project and gdUnit4 spec (`tests/project/`).
