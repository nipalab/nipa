# Changelog

## Unreleased

### Lock requests, lock watching and shelves

- Ask to release (context menu, lock badge, Locks tab) with an optional note;
  the holder gets a Release requested prompt.
- Notify me when free: watched paths (kept in the editor's project metadata)
  raise a Lock now prompt when their lock is released.
- Update, branch switch and tag checkout from the dock check `SyncPlan` and
  offer Shelve and continue when local changes would be replaced; the new
  Shelves tab applies, keeps, deletes and aborts shelves, and the dock shows a
  pending shelf apply.
- Staging from the Version Control panel no longer rescans the project or
  toasts per file (failures still toast).
- Activity polls refresh branches and tags; branch and tag events fire only
  when the lists changed.

### Fixes

- Godot strings passed to the core no longer end in a NUL byte, which made
  the setup checker read the clone root as `.nipaignore` ("Is a directory")
  and every clone lookup stop at the project folder.
- Godot's Version Control panel no longer needs a manual Refresh after a
  push, update, switch, merge or revert: operations reload status, commits
  and branches before they finish, and daemon-side status, commit and branch
  changes re-emit `filesystem_changed` (once per frame, not during a scan).
- The out-of-date banner hides after a successful Update or Switch, the
  History tab reloads when new commits arrive, the dock's progress label
  stays up while other operations are queued, and Fetch refreshes locks.

### Phase 4: setup and distribution

- Core `setup` (setup checker rows, `.nipaignore` template merge and matcher,
  `MetaWarnings` parsing) and `decor` (FileSystem dock decorations); session
  `submit_setup_check` and `submit_meta_warnings` (`warnings_changed`).
- Setup checker dialog (Project › Tools › Nipa: Setup checker...), sidecar
  banner in the dock, opt-in `nipa/decorate_filesystem_dock`.
- Linux arm64 and macOS universal library names, `NIPA_WERROR`, MSVC static
  runtime; `make godot-pack` (`tools/pack.sh`) and the `godot-release.yml`
  release workflow.

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
