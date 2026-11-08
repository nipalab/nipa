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

## Install

1. Install the `nipa` CLI (`make build-client`, or a release build) and make
   sure the clone you work in was made with it (`nipa clone`).
2. Get the add-on, one of:
   - **Release zip:** download `nipa-godot-vX.Y.Z.zip` from the GitHub
     release tagged `godot-vX.Y.Z`. It holds
     `nipa-godot-vX.Y.Z/addons/nipa/` with the editor libraries for Windows
     x86_64, Linux x86_64 and arm64, and macOS (universal). Copy its `addons/`
     folder into your project. In the editor's **AssetLib › Import...**, keep
     "Ignore asset root" on so the files land in `res://addons/nipa/`.
   - **Asset Library:** search for "Nipa" in the AssetLib tab and install it
     (same zip).
   - **From source:** `make godot-build` (CMake ≥ 3.24, a C++17 compiler;
     godot-cpp 4.4 is fetched) writes the host library into
     `addons/nipa/bin/<platform>/`; copy or link (`ln -s`) `addons/nipa/` into
     your project. `make godot-pack` zips whatever libraries are present.
3. Restart the editor and enable the add-on in **Project › Project Settings ›
   Plugins** ("Nipa").
4. Open **Project › Tools › Nipa: Setup checker...** and apply what it offers
   (it selects `NipaVCSInterface` with autoload, see Setup checker below), then
   restart the editor. The Nipa dock appears on the right-hand side.

The add-on finds the `nipa` binary in this order: the `nipa/binary_path`
editor setting, `NIPA_BIN`, a `nipa` (`nipa.exe`) next to the editor library in
`addons/nipa/bin/<platform>/`, then `PATH`. Release zips can carry that
companion CLI (an opt-in of the release workflow); otherwise install the CLI
yourself.

The project folder can be the clone root or a subfolder of it (the folder that
holds `project.godot`). The add-on looks up `.nipa/config` up to 32 parents.

Settings:

| Setting | Where | Default |
|---|---|---|
| `nipa/binary_path` | Editor Settings (per user) | empty, which means `NIPA_BIN`, then the library folder, then `PATH` |
| `nipa/merge_tool` | Editor Settings (per user) | empty; e.g. `meld $LOCAL $BASE $REMOTE -o $MERGED` |
| `nipa/decorate_filesystem_dock` | Editor Settings (per user) | false |
| `nipa/poll_interval_sec` | Project Settings (shared) | 60, clamped to 15–3600 |
| `nipa/locking/auto_lock_on_edit` | Project Settings (shared) | true |
| `nipa/locking/strict_read_only` | Project Settings (shared) | false |
| `nipa/locking/extensions` | Project Settings (shared) | `tscn,tres,scn,res,glb,gltf,blend,fbx,png,jpg,exr,wav,ogg` |

## Using

- **Commit** (Version Control dock) stages the selected files and pushes them
  as one Nipa commit. Sidecars (`.import`, `.uid`) are staged with their asset.
- **Pull** runs `Update`. The Nipa dock's Update button asks to save all open
  scenes first; the native Pull and branch switch run without that prompt in
  Phase 1, so save before using them. Afterwards the filesystem is rescanned and
  changed scenes that are open are reloaded.
- **Push** only shows a notice: Nipa commits go to the server on Commit.
- **Remotes** are read-only; the clone's server is the only remote.
- **Branches**: the list comes from the server. Creating a branch forks from the
  current branch, then the dock switches to it.
- **Nipa dock**: connection state with Reconnect, the branch, operation
  progress, an out-of-date banner with Update, a note for staged files outside
  the project, and "Open in Nipa Desktop".
- Operations run in the background. Results appear as editor toasts; the
  editor never waits on the network beyond a 2 s start-up budget (diffs and
  line diffs wait at most 1 s).

- **Locks** (Phase 2): FileSystem and scene-tab context menus (Nipa: Lock,
  Nipa: Unlock, Nipa: Locked by X), a lock badge in the 2D/3D toolbars and the
  Inspector header, the Nipa bottom panel's Locks tab (Refresh, Release) and a
  lock count in the dock. Editing a lock-relevant file locks it in the
  background; a file locked by someone else gets a warning and, for scenes, an
  offer to reload. Commit checks locks first (see Locks below).

- **Conflicts, tags, history and previews** (Phase 3):
  - The dock's switcher lists branches and tags. Picking a tag checks it out
    detached (`Switch{tag}`); a banner says so, and Commit refuses with a
    `nipa switch <branch>` hint until you switch back.
  - **Merge branch...** merges another branch into the current one. Conflicts
    open the bottom panel's Conflicts tab, and the dock shows the pending
    merge (Finish = commit, Abort) or revert (Continue, Skip, Abort) banner.
    Finish and Continue stay disabled while a file is still conflicted.
  - **Conflicts tab:** Mine / Theirs for every file; Merge for
    `.tscn`/`.tres`; External tool for other text; Compare for images.
  - **History tab** (FileSystem menu **Nipa: History**, or Whole project):
    the commits that changed the file, 30 per page with Load more. Open commit
    follows the `nipa://<server>/<org>/<project>/commits/<id>` link (a plugin
    cannot select a commit in Godot's own Version Control panel); for images,
    Compare with previous opens the Image diff tab.
  - **Image diff tab** (FileSystem menu **Nipa: Compare with last pushed**,
    History, Conflicts): both sides side by side, changed-pixel statistics and
    a threshold slider.

- **Setup and distribution** (Phase 4):
  - **Setup checker** (Project › Tools › Nipa: Setup checker..., or the dock's
    sidecar banner): one row per check with a one-click fix where one exists.
    Nothing is written until you click a fix, and every fix is idempotent.

    | Check | Fix |
    |---|---|
    | `editor/version_control/plugin_name = "NipaVCSInterface"` and `autoload_on_startup = true` | "Use NipaVCSInterface" sets both and saves `project.godot` (restart the editor to load it) |
    | `.nipaignore` exists at the clone root and ignores the project's `.godot/` | "Create .nipaignore" / "Add Godot patterns" appends the missing template lines (below), never duplicating one |
    | `.godot/` is not versioned | staged `.godot/` files: "Unstage them"; untracked ones: "Add Godot patterns"; versioned on the server: instructions only |
    | Sidecar warnings (`MetaWarnings{engine: "godot"}`) | "Stage sidecars" stages the `sidecar-not-staged` paths |
    | Daemon version against the connector minimum, plugin version, which `nipa` binary would be started | — |

    The template is `.godot/`, `/android/`, `*.tmp`,
    `export_credentials.cfg`, `.mono/`, `mono_crash.*.json`, `data_*/`
    (anchored entries are moved under the project folder when the project is
    a subfolder of the clone). The `.godot/` check reads the status cache
    (staged, changed and untracked paths below it) and asks the server with a
    non-recursive `ProxyTreeManifest{path: "<project>/.godot"}`: a 404 for the
    path means it is not versioned. It is one cheap call, and the local status
    alone cannot tell an ignored folder from a versioned, unchanged one.
  - **Sidecar banner** in the Nipa dock, fed by the same `MetaWarnings` data
    (refreshed after status changes, `warnings_changed`), with "Stage sidecars"
    and a link to the setup checker.
  - **FileSystem dock decoration** (opt-in, Editor Settings
    `nipa/decorate_filesystem_dock`): changed files get a status color and a
    "Nipa: ..." tooltip line (new, modified, deleted, conflicted, locked by you
    or by someone else), and folders summarize the changes below them. It only
    sets item colors and tooltips on the dock's own Tree and file list, puts
    the originals back when it is turned off or the add-on unloads, and
    re-applies when the dock rebuilds its items (checked on `draw`, no
    per-frame work). It needs Godot 4.4+ and the expected dock layout (one
    Tree, at most one ItemList, item metadata `res://...`); otherwise it turns
    itself off with one toast. Rich resource tooltips of the dock may hide
    the tooltip line; the color stays.

Not yet: the Activity tab.

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
   walking private dock nodes, so it is an opt-in, version-guarded setting
   (`nipa/decorate_filesystem_dock`).
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
  history, only when the scene has something to undo, so opening a scene is not
  an edit) and `EditorInspector.property_edited` (resources) resolve the
  edited file (`scene_file_path` / `resource_path`). If the file is
  lock-relevant and not locked by me, the plugin acts on its state:
  - **free:** `ProxyLockFile` runs in the background, and the toolbar badge
    turns "mine".
  - **theirs:** a warning toast and badge name the holder ("changes cannot be
    pushed"), with an offer to reload the scene from disk.

  Lock-relevant means the extension is in `nipa/locking/extensions`, or
  `LockStatus` reports the file as a changed binary. Auto-lock is on by
  default and is a project setting. A lock covers the asset plus its existing
  `.import`/`.uid` sidecars.
- **After save** (`resource_saved` / `scene_saved`): saving a file that is
  locked by someone else shows an error toast.
- **Strict read-only (opt-in):** files covered by someone else's lock get
  their write permission removed: `FileAccess.set_read_only_attribute` on
  Windows, `set_unix_permissions` without the write bits elsewhere. The
  permission comes back when the lock is released or the clone is switched.
  Godot then fails the save itself, which is the closest it gets to
  `IsOpenForEdit`. `core/locks/readonly_guard` does it with
  `std::filesystem::permissions` (the read-only attribute on Windows) after
  every lock refresh, expands directory locks to their files, never touches
  `.nipa/`, `.godot/` or files that were already read-only, and restores the
  original permission when the lock goes away, strict mode is turned off or the
  editor closes (a failed restore stays recorded and is retried). The guarded set is kept in `.godot/nipa/readonly.json`, so a crashed
  editor gives the permission back on the next start. Update/restore keep a
  guarded file read-only, and the client ignores a removed write bit on a
  file tracked writable, so the guard never shows in status or reaches the
  server (`docs/LOCKING.md`).
- **Commit pre-check:** `_commit` refreshes `LockStatus` within a 2 s budget
  (on timeout it pushes and the server stays authoritative), then:
  - `theirs` blocks the commit and names the holders (`popup_error`).
  - `needed` emits `commit_locks_needed`; the dock opens a `ConfirmationDialog`
    "Lock N files and commit", which locks them and then pushes.
- **Lock cache refresh:** on connect, on window focus
  (`NOTIFICATION_APPLICATION_FOCUS_IN`), after lock/unlock, push, update and
  switch, on every `WatchEvents` reply (including `lock_released`) and on a
  timer at the poll interval.

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

How the tab resolves (`MergeGodotResource` and `ResolveConflict`, see
`docs/DAEMON.md`):
- **Merge** asks the daemon for the merged scene. A clean result is submitted
  as `ResolveConflict{choice: content}`. Otherwise the tab lists each
  conflict as `section · key` with its reason, and you pick Mine or Theirs.
  Besides same-key edits, the merge reports a section one side removed and
  the other changed, and anything the merge itself would break (a removed
  parent node, resource or connection endpoint, a second root, a child or
  resource use placed before its definition): a false conflict is preferred
  to a broken scene.
- **External tool** writes `BASE`, `LOCAL`, `REMOTE` (the committed versions)
  and `MERGED` (the working copy) under `.godot/nipa/merge/`. The command from
  `nipa/merge_tool` is split like a shell command in `core/conflicts`, with
  the placeholders substituted per argument (a command without placeholders
  gets the four paths appended), and started with `OS.create_process`. The tab
  polls it; when it exits with 0 and `MERGED` has no markers left, the file is
  submitted as the resolution.
- The plugin never writes into a Godot file itself: every resolution goes
  through the daemon, then the FileSystem is rescanned and a resolved open
  scene is reloaded.

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

The dock shows them as a banner with "Stage sidecars" as the fix (it stages the
`sidecar-not-staged` paths, the only kind with a safe one-click fix), and the
setup checker lists them all.

---

## Settings

| Setting | Stored in | Default |
|---|---|---|
| `nipa/binary_path` | `EditorSettings` (per user) | empty → `NIPA_BIN` → binary next to the extension → `PATH` |
| `nipa/merge_tool` | `EditorSettings` | empty |
| `nipa/decorate_filesystem_dock` | `EditorSettings` | false |
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
    conflicts/           conflict set, per-file actions, Godot merge result, merge tool command
    history/             tags, paged commit log, nipa:// commit links
    preview/             Preview stream assembler (PNG per side, diff stats)
    setup/               setup checker rows, .nipaignore template/merge/matcher, MetaWarnings parsing
    decor/               FileSystem dock decorations (status/lock → color + tooltip, folder summaries)
    session/             session (worker thread, op queue, caches, stream loops, events)
  core/tests/            doctest + fake daemon (httplib server)
  extension/             register_types.cpp (EDITOR init level), nipa_vcs_interface.cpp, nipa_session.cpp (signals, deferred dispatch)
  addons/nipa/           what users install
    plugin.cfg  nipa_plugin.gd  nipa.gdextension  bin/<platform>/
    ui/  dock.tscn/.gd  bottom_panel.tscn/.gd  conflicts_tab  history_tab  image_diff_tab  lock_badge  setup_checker  setup_row
    context_menu.gd  inspector_plugin.gd  settings.gd  filesystem_decorator.gd
    LICENSE.md  CHANGELOG.md
  tests/project/         headless smoke project (gdUnit4) loading addons/nipa
  tools/pack.sh          reproducible release zip (make godot-pack)
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
- `locks_changed(guarded_paths)` (files strict mode just made read-only or writable)
- `commit_locks_needed(message, clone_paths)`
- `conflicts_changed()`, `tags_changed()`
- `activity(kind, actor, detail)`
- `warnings_changed()`

Its methods return `NipaRequest` handles (`RefCounted` with a `completed(ok,
message)` signal), so GDScript uses `await`. Data requests (conflicts,
`MergeGodotResource`, merge-tool files, history pages, previews) put their
result in `get_result()`: a `Dictionary`, with the merged file and the preview
PNGs as `PackedByteArray` values. Preview PNGs are assembled once in `core`,
decoded with `Image.load_png_from_buffer`, and the textures are released when
the tab shows another comparison or closes.

## Testing

Build prerequisites: CMake ≥ 3.24, a C++17 compiler, Python 3 (godot-cpp's
binding generator), git (FetchContent). `gcovr` for the coverage gate
(`pip install gcovr`).

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
- `make godot-pack`: `plugins/godot/tools/pack.sh` zips `addons/nipa/` with
  whatever `bin/<platform>/` libraries exist into
  `bin/nipa-godot-vX.Y.Z.zip` (version from `plugin.cfg`). Entries are sorted,
  timestamps and modes fixed, and build leftovers (`.lib`, `.exp`, `.pdb`)
  dropped, so the same inputs give the same bytes.
- CI `.github/workflows/godot-plugin.yml` runs the core tests and coverage on
  PRs that touch `plugins/godot/**`, builds and packs the Linux library, and
  runs the headless smoke on Godot 4.4 and the latest stable.

## Release

`.github/workflows/godot-release.yml` runs on a `godot-vX.Y.Z` tag (or by hand
with `tag`):

1. The tag, `plugin.cfg` and `core/version.h` must name the same version.
2. One editor library per platform: Windows x86_64 (MSVC, static runtime),
   Linux x86_64 and arm64 (Ubuntu 22.04 runners, for an older glibc), macOS
   universal (`CMAKE_OSX_ARCHITECTURES="arm64;x86_64"`, checked with `lipo`).
   Each build checks that its file name is the one `nipa.gdextension` lists.
3. Optional companion CLI: with the dispatch input `include_cli` (or the
   repository variable `GODOT_RELEASE_INCLUDE_CLI=true`) each job also builds
   `nipa` (`CGO_ENABLED=0`, a universal binary on macOS) into
   `bin/<platform>/`, where the connector finds it.
4. macOS signing and notarization run only when the secrets exist:
   `MACOS_CERTIFICATE` (base64 .p12), `MACOS_CERTIFICATE_PASSWORD`,
   `MACOS_SIGN_IDENTITY`, and for notarization `MACOS_NOTARY_KEY_P8`,
   `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID` (the same names as the
   desktop release). Libraries are signed with the hardened runtime and
   submitted with `notarytool`; a dylib cannot be stapled, so Gatekeeper
   checks the ticket online.
5. The package job puts every library under `addons/nipa/bin/`, fails when a
   `nipa.gdextension` path has no file, runs `tools/pack.sh`, writes a
   `.sha256`, and loads the packed add-on in Godot 4.4 (downloaded over HTTPS
   and checked against the release's `SHA512-SUMS.txt`) with `check_load.gd`.
6. The zip is attached to the GitHub release of the tag, created with
   `--latest=false` so `releases/latest` stays on the desktop app (its updater
   reads it). Tags such as `godot-v0.2.0-rc1` become prereleases.

Actions are pinned by commit SHA.

### Asset Library submission

The Godot Asset Library takes a repository URL plus a download URL and does
not host files. Submit (and update) the asset at
`https://godotengine.org/asset-library/asset/submit` by hand:

- Category *Tools*, Godot version 4.4, license Apache-2.0, repository
  `https://github.com/nipalab/nipa`, issues URL of the repository.
- Download provider *Custom*, download URL: the release asset
  `https://github.com/nipalab/nipa/releases/download/godot-vX.Y.Z/nipa-godot-vX.Y.Z.zip`,
  download commit: the tag. The zip's single root folder is stripped by the
  installer, so the files land in `res://addons/nipa/`.
- Version string: `X.Y.Z` (the same as `plugin.cfg`), icon URL: a raw 128×128
  PNG from the repository.
- Description: needs the `nipa` CLI and a Nipa server; lists the platforms the
  zip carries.

Each new release needs an edit of the asset with the new version and URL;
moderators review every edit.
