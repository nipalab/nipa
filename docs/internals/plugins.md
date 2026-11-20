# Engine and editor plugin internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

- **Unity plugin** (`plugins/unity/com.nipalab.nipa`, Apache 2.0, design in
  `docs/UNITY_PLUGIN.md`): UPM package
  over the daemon JSON bridge. `Editor/Core` is engine-free C# (`DaemonClient`
  JSON/NDJSON over `HttpClient`, `Connector` attach-or-spawn, `StatusCache`,
  `ProjectPaths`, `NipaSession`, `LockPolicy` for covering-lock checks,
  strict mode and the push pre-check, `MergeTool` for UnityYAMLMerge conflict
  merges, `LockWatches` (notify-when-free paths, `EditorUserSettings`
  `Nipa.LockWatches`), `OpSummary`, `SetupChecker` (Force Text, visible meta, `.nipaignore` template;
  Setup tab), `make unity-pack` tarball + `.github/workflows/unity-plugin.yml`, branch/tag/history/conflict calls on `NipaSession` plus merge
  (`Merge{source_branch}`), `ProxyBranchCreate`/`ProxyBranchDelete`, shelves
  (`ShelveCreate`/`ShelveApply`/`ShelveList`/`ShelveDrop`, Shelves tab, pending
  `shelf` banner), `SyncPlan` behind `NipaHost.Sync` ("Shelve and continue"
  before Update/switch/tag checkout, open scenes saved first through
  `NipaHost.SaveOpenScenes`) and `ProxyLockRequestRelease` (Ask to release,
  `lock_requested` prompt), maintenance (`RepoActivity.maintenance` banner,
  `MaintenanceNotes`, `NipaHost.Allowed` gate, `DaemonException.Maintenance`),
  `PushPlan` push checks (`PushChecks`), lock TTL (`LockTtlHours`, `LockExpiry`,
  Renew), paged history (`HistoryPageAsync`, next = last `parent_1_id`) and
  `Preview` image compare for conflicts and Compare with Last Pushed
  (`NipaImageCompare`, `snapshot` vs `working`, `ImageFiles`), Setup tab
  `.meta` row with Stage N files (`SetupChecker.StagePaths`, `meta-not-staged`)
  and daemon/plugin/CLI versions (`SetupChecker.VersionText`); locks surface through
  `AssetModificationProcessor`, overlays, the Inspector header and a Locks
  tab, fed by `LockStatus`/`ProxyLockFile`/`ProxyUnlockFile`/`WatchEvents`) compiled as `netstandard2.1` by
  `plugins/unity/dotnet/Core` and tested there (`make unity-test`, NUnit +
  coverlet, fake daemon over `HttpListener`); `Editor/` holds the thin Unity
  glue (`[VersionControl("Nipa")]` provider with overlays/menus/settings,
  `NipaHost`, `NipaWindow`). Keep Core free of `UnityEngine`/`UnityEditor`
  and C# 9 compatible.
- **Godot plugin** (`plugins/godot`, Apache 2.0, design in `docs/GODOT_PLUGIN.md`):
  Godot 4.4+ add-on `addons/nipa` over
  the daemon JSON bridge. `core/` is engine-free C++17 (`daemon` client,
  connector and messages, `paths` project/clone mapping and sidecars, `status`
  VCS mapping and cache, `diff` model and Myers, `locks` (`LockPolicy` port of
  Unity's covering/scope/push-check rules with asset+sidecar lock units, and
  `ReadOnlyGuard` for strict read-only mode, state in `.godot/nipa/readonly.json`),
  `session` worker, stream loops, lock cache/lock/unlock/blocking push check,
  merge/revert/conflicts/resolve/`MergeGodotResource`, merge-tool temp files,
  tags, paged history and assembled `Preview` PNGs (data ops finish with
  `Event.data`/`bytes`, surfaced by `NipaRequest.get_result()`), `conflicts`
  (conflict set, per-file actions, `merge_tool` command split/substitution),
  `history` (tags, pages, `nipa://` commit links), `preview` (stream
  assembler), `setup` (setup checker rows, `.nipaignore` Godot template merge
  and gitignore-subset matcher, `MetaWarnings` parsing; the session's
  `submit_setup_check` adds the `.godot/` probe via status + non-recursive
  `ProxyTreeManifest` and `submit_meta_warnings` feeds `warnings_changed`),
  `decor` (FileSystem dock color/tooltip per status and lock, folder
  summaries) and events; tested with doctest against `core/tests/fake_daemon`, gcovr gate
  80%, `make godot-test`). `extension/` is the godot-cpp GDExtension
  (`NipaVCSInterface` extends `EditorVCSInterface`, singleton `Nipa` =
  `NipaSession`, `NipaRequest`; editor-only, `make godot-build`). `addons/nipa/`
  holds the GDScript `EditorPlugin` (auto-lock on edit, save warnings, lock
  refresh on focus/timer, Project › Tools › Nipa: Setup checker...),
  `settings.gd` (`nipa/locking/*`, `nipa/merge_tool`,
  `nipa/decorate_filesystem_dock`), `filesystem_decorator.gd` (opt-in,
  version/shape-guarded colors and tooltips on the FileSystem dock's own
  Tree/ItemList, restored on disable),
  `context_menu.gd` (FileSystem/scene-tab Lock/Unlock, History, Compare with
  last pushed), `inspector_plugin.gd`, and the dock (branch/tag switcher,
  Merge branch..., detached-tag, pending merge/revert and sidecar banners),
  `setup_checker`/`setup_row` scenes,
  `bottom_panel` (Locks, Conflicts, History, Image diff tab scenes) and
  `lock_badge` scenes; `_commit` runs the lock pre-check and refuses while
  detached at a tag. The client treats a removed write bit on a file tracked writable as
  no mode change (`trackedModeFromPerm`/`pushPerm`), so the guard never leaks
  into status or pushes.
  `tests/project` is the headless smoke project (`check_load.gd`) and the
  vendored gdUnit4 spec. Vendored code and versions: `plugins/godot/thirdparty/README.md`.
  `make godot-pack` (`plugins/godot/tools/pack.sh`) builds a reproducible
  `bin/nipa-godot-vX.Y.Z.zip` from `addons/nipa` with whatever libraries exist;
  `.github/workflows/godot-release.yml` (tag `godot-v*`) builds Windows MSVC,
  Linux x86_64/arm64 and macOS universal libraries (names must match
  `nipa.gdextension`; optional companion `nipa` CLI, macOS signing/notarization
  gated on secrets), smoke-loads the zip in Godot 4.4 and attaches it to the
  release with `--latest=false` (the desktop updater owns `releases/latest`).
- **VS Code extension** (`plugins/vscode`, Apache 2.0, design in
  `docs/VSCODE_EXTENSION.md`): TypeScript client of the daemon JSON bridge
  (`src/daemon`: endpoint, connector (attach or start `nipa serve`), NDJSON
  client, shared `DaemonSession`) and a native `vscode.scm` provider per clone
  (`src/scm`: staged/changes/conflicts groups, push with `PushPlan` +
  `LockStatus` checks, update, `nipa-blob:` quick diff over `ReadBlob`,
  file decorations with lock states, `WatchChanges` refresh; phase 2:
  `BlameController` editor decorations over `Blame`, Lock/Unlock/Ask to
  Release with covering-lock lookup from `LockStatus.locks` and one-time
  `needed`/`theirs` warnings, conflict rows with Take Mine/Theirs, Mark as
  Resolved (refused with markers) and Compare Sides per `ConflictSides`, a
  branch/tag picker, `SyncPlan` + `ShelveCreate` "Shelve and Continue" before
  Update/Switch, `WatchEvents` incoming count and maintenance item, `nipa://`
  links). Never calls
  `UnwatchRepo` (not ref-counted, it would unwatch the desktop's clone).
  vitest against `test/fakes/vscode.ts` + a fake bridge, 80% gate,
  `NIPA_VSCODE_DAEMON_BIN` runs the real-daemon test; `make vscode-test` /
  `vscode-pack`, `.github/workflows/vscode-extension.yml`.

