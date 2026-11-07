# Nipa Godot plugin — design

`addons/nipa` is a Godot 4 editor add-on that makes Nipa a first-class version
control system inside the editor. It plugs into Godot's own VCS integration
(**Project › Version Control**, the Commit dock and the Version Control bottom
panel), and adds what that integration lacks — file locks, conflicts, tags and
teammate activity — the way a Godot add-on does it: an `EditorPlugin`
with docks, context menus, an inspector plugin and toasts. Like the Unity
package (`docs/UNITY_PLUGIN.md`), it only talks to the local `nipa serve`
daemon through the JSON/HTTP bridge (`docs/DAEMON.md`), never to the server or
the CLI. Phased handoff: `docs/plans/GODOT_PLUGIN.md`.

Targets **Godot 4.4+** (`compatibility_minimum = "4.4"`, tested on 4.4 and the
latest stable). 4.4 is the floor because it adds `EditorContextMenuPlugin` and
the `.uid` sidecar files that the plugin must track. Godot 3 is out of scope.

---

## How Godot's VCS integration works (research)

Source: `editor/version_control/version_control_editor_plugin.cpp` and the
`EditorVCSInterface` class reference.

- **Registration is native only.** The editor lists plugins with
  `ClassDB::get_direct_inheriters_from_class("EditorVCSInterface")` and creates
  one with `ClassDB::instantiate(name)`. GDScript `class_name` classes are not
  in `ClassDB`, so **a VCS plugin must be a GDExtension (or engine module)
  class that directly extends `EditorVCSInterface`**. This is why
  `godot-git-plugin` is a godot-cpp GDExtension.
- **Selection** is stored in `project.godot`:
  `editor/version_control/plugin_name` and
  `editor/version_control/autoload_on_startup`. On the editor's `READY`
  notification it instantiates the plugin, calls `_initialize(res_dir)` and then
  `_set_credentials(...)`. The editor saves the username and SSH key paths to
  `EditorSettings` (`version_control/*`) but never stores the password or the
  passphrase.
- **Calls are synchronous, on the main thread**, straight from UI callbacks.
  There are no threads or async hooks, so a slow `_push` freezes the editor.

| Editor trigger | Calls |
|---|---|
| Plugin loaded | `_allow_amends`, then the refresh set |
| `EditorFileSystem.filesystem_changed`, Refresh button | `_get_modified_files_data`, `_get_previous_commits`, `_get_branch_list`, `_get_remotes` (+ `_get_current_branch_name`) |
| Commit button | `_commit(msg, amend)` |
| Stage / Unstage / Discard (Discard is only offered for files that are not new) | `_stage_file`, `_unstage_file`, `_discard_file` |
| Branch picker / create / remove | `_checkout_branch`, `_create_branch` + `_checkout_branch`, `_remove_branch` |
| Fetch / Pull / Push / Force push | `_fetch`, `_pull`, `_push(remote, force)` |
| Select a file or a commit | `_get_diff(path or commit id, area)` |
| Remote add / remove | `_create_remote`, `_remove_remote` |
| Shutdown | `_shut_down` |

- The data model is plain `Dictionary` values built with helpers:
  `create_status_file(path, ChangeType, TreeArea)`,
  `create_commit(msg, author, id, unix_ts, offset_min)`, and
  `create_diff_file` / `create_diff_hunk` / `create_diff_line` assembled with
  `add_diff_hunks_into_diff_file` / `add_line_diffs_into_diff_hunk`.
  `ChangeType` is NEW, MODIFIED, RENAMED, DELETED, TYPECHANGE or UNMERGED.
  `TreeArea` is COMMIT, STAGED or UNSTAGED. Errors are reported with
  `popup_error(msg)`.
- **The model is git-shaped:** local commits, remotes, then fetch, pull and
  push. It has no notion of locks, tags, conflict resolution or binary
  previews.
- **There is no public API for FileSystem dock overlays**, and nothing like
  Unity's `IsOpenForEdit` (no veto before an edit or save). What does exist:
  - `EditorContextMenuPlugin` (FileSystem, Scene tree, script editor and
    Inspector-property slots)
  - `EditorInspectorPlugin`
  - the `EditorPlugin.resource_saved` / `scene_saved` signals, which fire
    after the save
  - `EditorUndoRedoManager.history_changed`, which fires on the first edit
  - `EditorToaster`
  - `EditorInterface.reload_scene_from_path` / `save_all_scenes`
  - `EditorFileSystem.scan()` / `update_file()`
- **Godot sidecar files** sit next to the source file and are versioned with
  it:
  - `foo.png.import`: import settings plus `uid="uid://…"`. Imported assets
    keep their UID here.
  - `foo.gd.uid` (4.4+): the UID of scripts, shaders and other non-imported
    resources.

  The FileSystem dock moves a file together with its sidecars. Losing or
  regenerating a sidecar changes the UID and breaks every `uid://` reference
  to it. That is the Godot equivalent of Unity's `.meta` GUID.
- `.godot/` is the import cache and editor state. It is never versioned, and
  neither are `/android/` build templates.
- Scenes and resources are text (`.tscn`/`.tres`, INI-like
  `[gd_scene]`/`[ext_resource]`/`[sub_resource]`/`[node]` sections) or
  binary (`.scn`/`.res`). Godot ships **no merge tool**, and git-style conflict
  markers corrupt a `.tscn`.

---

## Design decisions

1. **Two halves, one add-on folder.**
   - **`nipa` GDExtension (C++17, godot-cpp):** registers the
     `NipaVCSInterface` class (extends `EditorVCSInterface`, so it appears in
     the editor's VCS plugin list as "NipaVCSInterface") and the `NipaSession`
     engine singleton (`Engine.get_singleton("Nipa")`). It also holds the
     daemon client, caches and policies.
   - **GDScript `EditorPlugin`:** everything visual (dock, bottom panel,
     context menus, inspector plugin, toolbar badge, toasts, settings),
     written the idiomatic way: `@tool` scripts, `.tscn` UI scenes and
     signals.
2. **Engine-free C++ core plus a thin godot-cpp glue layer.** This mirrors
   Unity's `Editor/Core`. `core/` builds without godot-cpp, and is tested
   with doctest against a fake daemon, with gcovr enforcing ≥80% coverage.
   - Rejected: GDScript core. Godot has no maintained coverage tool for 4.4,
     `HTTPClient` must be polled on the main thread, and the VCS class has to
     be native anyway.
   - Rejected: Go GDExtension (graphics.gd). Immature bindings, cgo
     cross-compiles for macOS/Windows, and the risk of a second Go runtime in
     the editor process.
   - Rejected: godot-rust. It brings in a new toolchain for no gain.
3. **Transport:** the existing JSON bridge (`json_port`). The client is
   cpp-httplib (MIT, header-only, plain HTTP is enough on loopback) plus
   nlohmann/json (MIT), both vendored in `plugins/godot/thirdparty`. No
   godot-cpp types cross into `core/`.
4. **The main thread never waits on the network.** Read virtuals
   (`_get_modified_files_data`, `_get_branch_list`, …) answer from caches that
   a worker thread keeps fresh. Only a cold-start `Status` may block, for at
   most 2 s against the local daemon. Write virtuals (`_commit`, `_pull`,
   `_checkout_branch`) queue a daemon operation and return at once. Progress
   goes to the Nipa dock and `EditorToaster`, and when the operation finishes
   the plugin calls `EditorFileSystem.scan()`. Godot then emits
   `filesystem_changed`, which refreshes the native dock without any private
   API. Worker results reach the main thread through
   `callable_mp(...).call_deferred()` on `NipaSession`.
5. **Map Nipa's centralized model onto the git-shaped dock honestly.**
   - **Commit = push to the server:** stage, then `Push(message)`.
   - **Push:** there is nothing local to push; it toasts "Nipa commits go to
     the server on Commit".
   - **Pull = `Update`.**
   - **Fetch** = poll for teammate activity now.
   - **Remotes:** one read-only remote named after the clone URL.
   - **Amend** is disabled.
6. **Locks are Perforce-style checkout, adapted to Godot's after-the-fact
   hooks:**
   - auto-lock on the first undoable edit
   - warn and offer a reload when the file is locked by someone else
   - a push pre-check
   - opt-in strict mode that sets the on-disk read-only attribute, so Godot
     itself refuses to save
7. **Sidecars travel together.** An asset, its `.import` and its `.uid` are
   always staged, restored, locked and unlocked as one unit, the same rule
   Unity applies to `.meta`.
8. **No FileSystem dock hacks by default.** Status lives in the native Commit
   dock and the Nipa dock. Painting status onto the FileSystem tree means
   walking private dock nodes, so it ships later as an opt-in, version-guarded
   setting.
9. **License and location:** `plugins/godot/` (Apache 2.0, OSS, like Unity).
   It ships as a zip of `addons/nipa/` with prebuilt editor binaries, through
   GitHub releases and the Godot Asset Library.

---

## Mapping `EditorVCSInterface` → daemon

Paths are clone-relative on the daemon side and project-relative in the dock.
`core/paths` maps between them, because the Godot project (the folder holding
`project.godot`) can sit in a subfolder of the clone. Files outside the
project are hidden from the dock but still pushed, and the Nipa dock warns
when the staged set contains any.

| Virtual | Nipa behaviour | Daemon |
|---|---|---|
| `_initialize(path)` | connect or spawn the daemon, find the clone root (up to 32 parents), register, first status, start streams | `Ping`, `WatchRepo`, `Status`, `WatchChanges`, `WatchEvents` |
| `_get_vcs_name` | `"Nipa"` | — |
| `_set_credentials(user, pwd, pub, priv, pass)` | log in only when `AccountStatus` says the session is missing: a private key path uses SSH, a password uses password login; nothing is stored by the plugin | `AccountStatus`, `LoginSSH` (+ new `key_path`/`passphrase`), `Login` |
| `_get_modified_files_data` | status cache → dictionaries (table below) plus sidecar pairing | `Status` |
| `_stage_file` / `_unstage_file` | path plus its existing sidecars | `Stage{add}` / `Stage{unstage}` |
| `_discard_file` | path plus sidecars, then `EditorFileSystem.update_file` and reload of open clean scenes | `Restore` |
| `_commit(msg, amend)` | save prompt for dirty scenes → lock pre-check → `Push`; `amend` is always false | `LockStatus`, `Push` (stream) |
| `_pull` | save prompt → `Update` → scan + reload changed open scenes | `Update` (stream) |
| `_push` | info toast only | — |
| `_fetch` | immediate activity poll, out-of-date badge | `WatchEvents` |
| `_get_current_branch_name` | `Status.branch`, or `tag:<name>` while detached | `Status` |
| `_get_branch_list` / `_create_branch` / `_remove_branch` | cached list; create forks from the pinned commit, then the dock calls checkout | `ProxyBranchList` / `ProxyBranchCreate` / `ProxyBranchDelete` |
| `_checkout_branch` | save prompt → `Switch`; returns true once queued | `Switch` (stream) |
| `_get_remotes` | `["<host>/<org>/<project>"]` | `ListRepos` |
| `_create_remote` / `_remove_remote` | `popup_error("not supported by Nipa")` | — |
| `_get_previous_commits(n)` | cached project log | `ProxyCommitLog` |
| `_get_diff(id, COMMIT)` | commit against its first parent | `DiffFiles{revisions:[id], show}` |
| `_get_diff(path, STAGED/UNSTAGED)` | working copy against the snapshot (Nipa has no separate staged content) | `DiffFiles{paths:[path]}` |
| `_get_line_diff(path, text)` | snapshot blob vs the given buffer, diffed in `core` (Myers) | `ReadBlob{snapshot}` |
| `_allow_amends` | false | — |
| `_shut_down` | cancel streams, `UnwatchRepo` | `UnwatchRepo` |

**Status → `create_status_file`:**

| Daemon list | ChangeType | TreeArea |
|---|---|---|
| `added` | NEW | STAGED |
| `staged` ∩ `deleted` | DELETED | STAGED |
| other `staged` | MODIFIED | STAGED |
| `modified` (not staged) | MODIFIED | UNSTAGED |
| `untracked` | NEW | UNSTAGED |
| `missing` | DELETED | UNSTAGED |
| `conflicts` − `resolved` | UNMERGED | UNSTAGED |
| `resolved` | MODIFIED | STAGED |

**Diff → dictionaries:**
- Each `DiffFile` becomes `create_diff_file(new.path, old.path)` with its
  hunks and lines. Line status is `"+"`, `"-"` or `" "`, and Godot expects
  `-1` for a missing line number.
- Binary or omitted files (`kind != text`, `hunks_omitted`,
  `content_unavailable`) get a single synthetic hunk that reads
  `Binary <kind>: <old size> → <new size>, open in the Nipa dock to compare`.
  The Nipa dock shows image diffs through `Preview` (PNG →
  `ImageTexture`), and anything richer opens in Nipa Desktop via a
  `nipa://` link.

---

## Godot surfaces beyond the VCS interface

The GDScript `EditorPlugin` (`addons/nipa/plugin.cfg`, enabled in
**Project Settings › Plugins**) is idiomatic Godot throughout:

| Surface | Godot API | Purpose | Daemon |
|---|---|---|---|
| **Nipa dock** (right, next to Inspector) | `add_control_to_dock`, `.tscn` scene | connection state, branch/tag switcher, operation progress, out-of-date banner with Update, sidecar warnings, detached-tag and pending merge/revert banners, staged-outside-project note | `Status`, `OpEvent`, `WatchEvents`, `MetaWarnings` |
| **Nipa bottom panel** | `add_control_to_bottom_panel` | tabs Locks, Conflicts, History (per file), Activity, Image diff | `ProxyListFileLocks`, `Conflicts`, `ProxyCommitLog{path}`, `Preview` |
| FileSystem context menu | `EditorContextMenuPlugin` (`CONTEXT_SLOT_FILESYSTEM`) | Nipa › Stage, Restore, Lock, Unlock, History, Open in Nipa Desktop | as named |
| Scene tabs menu | `CONTEXT_SLOT_SCENE_TABS` | Lock / Unlock this scene | `ProxyLockFile` |
| Inspector header | `EditorInspectorPlugin._parse_begin` | lock state of the edited resource's file, Lock / Unlock buttons | `LockStatus` |
| Scene toolbar badge | `add_control_to_container(CONTAINER_CANVAS_EDITOR_MENU / CONTAINER_SPATIAL_EDITOR_MENU)` | 🔒 mine / 🔒 holder for the open scene | lock cache |
| Notifications | `EditorToaster.push_toast` | op results, teammate push, review requests, lock conflicts | `WatchEvents` |
| Menu | `add_tool_menu_item` (Project › Tools › Nipa) | Open Nipa Desktop, Setup checker, Reconnect | — |
| Settings | `ProjectSettings` (shared, `nipa/…`) and `EditorSettings` (per user, `nipa/…`) registered with `add_property_info` + `set_initial_value` | see Settings | — |

### Locks (Perforce-style checkout)

Scope matches Unity and the server. Covering = exact path or a directory
prefix. Global locks apply on the default branch, branch locks on their own
branch. `core/locks` ports `LockPolicy`, and an asset and its sidecars are
one lock unit.

- **Checkout on first edit.** `EditorUndoRedoManager.history_changed` (scene
  history) and `EditorInspector.property_edited` (resources) resolve the
  edited file (`scene_file_path` / `resource_path`). If the file is
  lock-relevant and not locked by me, the plugin acts on its state:
  - **free:** `ProxyLockFile` runs in the background, and the toolbar badge
    turns "mine".
  - **theirs:** a warning toast and badge name the holder ("changes cannot be
    pushed"), with an offer to reload the scene from disk.

  Lock-relevant means the file is binary, or its extension is in the strict
  list (default `.tscn .tres .scn .res .glb .blend .png .wav` plus every
  binary). Auto-lock is on by default and is a project setting.
- **After save** (`resource_saved` / `scene_saved`): saving a file that is
  locked by someone else shows an error toast.
- **Strict read-only (opt-in):** files covered by someone else's lock get
  their write permission removed: `FileAccess.set_read_only_attribute` on
  Windows, `set_unix_permissions` without the write bits elsewhere. The
  permission comes back when the lock is released or the clone is switched.
  Godot then fails the save itself, which is the closest it gets to
  `IsOpenForEdit`. This needs the daemon to replace read-only files during
  update (Phase 0).
- **Commit pre-check:** `LockStatus.files`:
  - `theirs` blocks the commit and names the holders (`popup_error`).
  - `needed` opens a `ConfirmationDialog` "Lock N files and commit".
- **Lock cache refresh:** on window focus (`NOTIFICATION_APPLICATION_FOCUS_IN`),
  after lock/unlock, on `lock_released` events and on every poll tick.

### Update, switch and open scenes

Before `Update`/`Switch`/`Merge`/`Revert`, unsaved scenes trigger "Save all and
continue / Cancel" (`EditorInterface.save_all_scenes`). Afterwards:
1. `EditorFileSystem.scan()` runs, so Godot reimports changed assets.
2. Each changed `.tscn`/`.scn` that is open in a tab and clean is reloaded with
   `reload_scene_from_path`.
3. Dirty tabs are left alone, with a toast.
4. Scripts are reloaded by the script editor's own "file changed on disk"
   prompt.

### Conflicts

The Conflicts tab offers **Mine** and **Theirs** for every file (`ResolveConflict`).
- **`.tscn`/`.tres`:** **Merge** runs the Godot-aware three-way merge. It is
  implemented in Go (`internal/godotres`), reached through a new daemon RPC
  and reusable by Nipa Desktop:
  - Each section is keyed by identity: `[node name= parent=]`,
    `[ext_resource id=]`, `[sub_resource id=]`, `[connection signal= from= to= method=]`.
  - Property lines inside a section are merged per key.
  - Header counters are recomputed.
  - When both sides change the same key, the merge falls back to a manual
    pick (no markers are ever written into a scene).
- **Other text:** an optional external merge tool (`EditorSettings`
  `nipa/merge_tool`, with git's `$BASE $LOCAL $REMOTE $MERGED` placeholders).
- **Binaries:** Mine/Theirs plus an image compare through `Preview`.

A pending merge offers Finish and Abort; a pending revert offers Continue,
Skip and Abort. Finish/Continue stay disabled while conflicts remain open,
matching Unity.

### Sidecar hygiene (`.import` / `.uid`)

`MetaWarnings` gains `engine: "godot"`, backed by `internal/godotmeta`, a pure
`fs.FS` package next to `internal/unitymeta`. It reports four kinds of
warning:

- `missing-sidecar`: a new importable asset or script whose `.import`/`.uid`
  does not exist (not opened in the editor yet)
- `orphan-sidecar`: a sidecar whose asset is gone or deleted
- `sidecar-not-staged`: the asset is staged, its sidecar is not
- `uid-changed`: the `uid="uid://…"` in `.import`, or the content of `.uid`,
  differs from the snapshot, which breaks `uid://` references

The dock shows them as a banner with "Stage sidecars" as the fix.

---

## Settings

| Setting | Stored in | Default |
|---|---|---|
| `nipa/binary_path` | `EditorSettings` (per user) | empty → `NIPA_BIN` → binary next to the extension → `PATH` |
| `nipa/merge_tool` | `EditorSettings` | empty |
| `nipa/decorate_filesystem_dock` | `EditorSettings` | false (later phase) |
| `nipa/poll_interval_sec` | `ProjectSettings` (`project.godot`, shared) | 60, clamped 15–3600 |
| `nipa/locking/auto_lock_on_edit` | `ProjectSettings` | true |
| `nipa/locking/strict_read_only` | `ProjectSettings` | false |
| `nipa/locking/extensions` | `ProjectSettings` (`PackedStringArray`) | `tscn,tres,scn,res,glb,gltf,blend,fbx,png,jpg,exr,wav,ogg` |

`editor/version_control/plugin_name = "NipaVCSInterface"` and
`autoload_on_startup = true` are the editor's own keys. The setup checker
writes them.

---

## Layout

```
plugins/godot/
  CMakeLists.txt         core lib, core tests (doctest + gcovr), extension (godot-cpp via FetchContent, pinned godot-4.4-stable)
  core/                  engine-free C++17, no godot-cpp includes
    daemon/              endpoint (daemon.json), client (JSON + NDJSON stream), connector (attach, MinVersion, spawn, poll), messages
    status/              status cache, VCS mapping (status → change type/area)
    paths/               project ↔ clone paths, sidecar pairing
    locks/               lock policy (covering lock, scope, strict list, commit pre-check)
    diff/                DiffFile → hunk/line model, Myers for _get_line_diff
    session/             session (worker thread, op queue, caches, stream loops, events)
  core/tests/            doctest + fake daemon (httplib server)
  extension/             register_types.cpp (EDITOR init level), nipa_vcs_interface.cpp, nipa_session.cpp (signals, deferred dispatch)
  addons/nipa/           what users install
    plugin.cfg  nipa_plugin.gd  nipa.gdextension  bin/<platform>/
    ui/  dock.tscn/.gd  bottom_panel.tscn/.gd  lock_badge.tscn/.gd  setup_checker.tscn/.gd
    context_menu.gd  inspector_plugin.gd  settings.gd  icons/*.svg
    LICENSE.md  CHANGELOG.md
  tests/project/         headless smoke project (gdUnit4) loading addons/nipa
  thirdparty/            cpp-httplib, nlohmann/json, doctest
```

`nipa.gdextension`: `entry_symbol = "nipa_library_init"`,
`compatibility_minimum = "4.4"`, `reloadable = true`. The libraries are
`editor`-only builds for `windows.x86_64`, `linux.x86_64`, `linux.arm64` and
`macos` (universal, signed). The `NipaSession` destructor stops threads and
streams, so hot reload is safe.

`NipaSession` signals consumed by GDScript:

- `connected(version)`
- `disconnected(reason)`
- `status_changed(paths: PackedStringArray)`
- `op_queued(id, kind)`, `op_progress(id, phase, done, total)`,
  `op_finished(id, ok, message, changed_paths)`
- `locks_changed()`
- `activity(kind, actor, detail)`
- `warnings_changed()`

Its methods return `NipaRequest` handles (`RefCounted` with a `completed(ok,
result)` signal), so GDScript uses `await`.

## Testing

- `make godot-test`: CMake + ctest over `core/` with a fake daemon, gcovr
  fails below 80% line coverage.
- `make godot-build`: the extension for the host platform.
- `internal/godotmeta`, `internal/godotres` and the new RPC fields follow the
  Go rules (`go test ./...`, ≥80%).
- Headless smoke tests:
  - `godot --headless --editor --quit` checks that the extension loads and
    `NipaVCSInterface` is listed.
  - A gdUnit4 suite (`--headless`) drives the GDScript glue against
    `nipa serve` plus a real `nipad`, as in `tests/suite`.
- CI `.github/workflows/godot-plugin.yml` runs the core tests and coverage on
  PRs that touch `plugins/godot/**`. The headless smoke runs on Godot 4.4 and
  the latest stable, and a tag `godot-v*` builds the platform matrix and
  attaches the Asset Library zip.
