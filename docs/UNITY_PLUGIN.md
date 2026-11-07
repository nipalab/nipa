# Nipa Unity plugin

`com.nipalab.nipa` is a Unity Editor package that adds Nipa as a version
control provider: status overlays in the Project window, a Pending Changes
window and stage/restore/push/update from the editor. It talks to the local
`nipa serve` daemon through its JSON/HTTP bridge (`docs/DAEMON.md`), never to
the server or the CLI directly. Plan and later phases (branches, conflicts,
setup checker): `docs/plans/UNITY_PLUGIN.md`.

Requires Unity 2022.3 LTS or newer (also targeted at Unity 6), the `nipa` CLI
(0.1+ with the JSON bridge) and a Unity project inside a Nipa clone. The
project may be a subfolder of the clone.

## Install (Phase 1: local path)

1. Open **Window › Package Manager › + › Add package from disk** and pick
   `plugins/unity/com.nipalab.nipa/package.json` (or add
   `"com.nipalab.nipa": "file:../../plugins/unity/com.nipalab.nipa"` to
   `Packages/manifest.json`). `com.unity.nuget.newtonsoft-json` is pulled in
   as a dependency.
2. Open **Project Settings › Editor › Version Control** and set **Mode** to
   **Nipa**.
3. Make sure `nipa` is installed. The plugin starts the daemon when none is
   running; the binary is resolved from the setting below, `NIPA_BIN`, then
   `PATH`.

Git URL, tarball and OpenUPM installs arrive with Phase 4.

## Using it

- **Overlays** in the Project window: orange modified, green staged/added, red
  conflict, purple deleted/missing, blue untracked. Folders show the
  aggregate of their content. An asset and its `.meta` share one icon.
- **Window › Nipa › Pending Changes**: Changes and Staged lists, Stage /
  Unstage / Restore, commit message with **Push**, **Update**, progress bar,
  `.meta` warnings banner, detached-tag, pending merge/revert and conflict
  banners, and **Open in Nipa Desktop** (`nipa://` deep link).
- **Assets › Nipa › Stage / Restore / Refresh** and the same entries in the
  icon right-click menu.
- Staging or restoring an asset always includes its `.meta`; update and restore
  call `AssetDatabase.Refresh()` when they finish.

## Locking (Perforce-style checkout)

Binary changes need a Nipa file lock. The plugin maps it onto Unity's
checkout model:

- A path covered by someone else's lock (exact path or directory prefix, in
  the current branch's scope: global locks on the default branch, branch locks
  on their branch) is **read-only**: `IsOpenForEdit` is false, saving it is
  dropped with a dialog, and moving or deleting it is refused.
- **Strict locking** (setting, off by default): assets whose extension is in the
  list (default: binaries plus `.unity`, `.prefab`, `.anim`, `.controller`,
  `.asset`, `.mat`) are read-only until you lock them. New (untracked or just
  added) files are exempt. When Unity asks to make such an asset editable
  (`MakeEditable`), the plugin locks it and its `.meta`; if that fails the
  dialog names the holder.
- An asset and its `.meta` are always locked and released together.
- Overlays: a teal square (top right) = locked by you, a red square = locked by
  someone else, a cyan triangle on the `Assets` folder = new commits upstream.
- The Inspector header shows the lock state with Lock / Unlock buttons.
- **Assets › Nipa › Lock / Unlock** and the icon menu do the same for the
  selection.
- **Window › Nipa › Pending Changes › Locks** tab lists the locks in scope;
  Release is enabled for your own locks, and for anyone's when you administer
  the project (`can_release_any`).
- **Push** first refreshes `LockStatus`: changed binaries locked by someone
  else block the push (holders are named); binaries that still need a lock
  trigger a "Lock N files" prompt and the push continues once they are locked.
- Teammate events (`WatchEvents`): a push marks the clone out of date and
  shows an Update button; a review request shows a notice and a link to Nipa
  Desktop; a released lock refreshes the lock cache. The lock cache also
  refreshes on window focus, after lock/unlock and on every poll tick.

## Branches, conflicts and history

- **Switcher:** a dropdown in the window toolbar and a Scene view overlay
  ("Nipa") list branches (`ProxyBranchList`) and tags (`ProxyTagList`).
  Choosing one runs `Switch` (`tag` set for tags) and refreshes the
  AssetDatabase. A detached tag shows a banner (`Status.head`) and blocks push.
- **Pending merge/revert banner:** a waiting merge offers Finish (push) and
  Abort; a waiting revert offers Continue, Skip and Abort. Both stay disabled
  for Finish/Continue while conflicts are open.
- **Conflicts tab** (`Conflicts` / `ResolveConflict`): Mine and Theirs for any
  file. `.unity`, `.prefab`, `.asset`, `.controller`, `.mat` and `.anim` text
  conflicts also get **Merge**, which fetches base/ours/theirs with `ReadBlob`
  (`revision` source, ids from `Conflicts`) into a temp folder and runs
  `<Unity>/Tools/UnityYAMLMerge merge -p <base> <theirs> <ours> <out>`. A clean
  result is submitted with `choice=content`; a non-zero exit or leftover
  conflict markers fall back to a manual Mine/Theirs pick.
- **History tab:** `ProxyCommitLog` for the selected asset (or the whole
  project); **Diff** opens `nipa://<server>/<org>/<project>/commits/<id>` in
  Nipa Desktop.

## Settings

In **Project Settings › Editor › Version Control** (Nipa section):

| Setting | Stored in | Notes |
|---|---|---|
| nipa binary | `EditorUserSettings` (per user) | Empty = `NIPA_BIN`, then `PATH` |
| Poll interval | `ProjectSettings/NipaSettings.json` (shared) | Safety-net `Status` refresh when no file events arrive (e.g. a missed watcher event); also the `WatchEvents` server poll. Clamped to 15–3600 s, default 60; applies on reconnect |
| Strict locking | `ProjectSettings/NipaSettings.json` (shared) | Listed extensions are read-only until locked. Applies immediately |
| Locked extensions | `ProjectSettings/NipaSettings.json` (shared) | Comma separated list used by strict locking |

## Architecture

```
plugins/unity/com.nipalab.nipa/
  Editor/Core/   engine-free C# (asmdef noEngineReferences)
    Daemon/      Endpoint (daemon.json), DaemonClient (JSON + NDJSON), Ops,
                 Connector (attach, version check, spawn, poll), Messages
    Status/      StatusCache (path -> flags, folder aggregates, deltas)
    Paths/       ProjectPaths (project <-> clone paths, .meta pairing)
    Locks/       LockPolicy (covering-lock lookup, scope, strict mode,
                 push pre-check)
    Merge/       MergeTool (UnityYAMLMerge command, marker check, conflict
                 classification), SystemMergeProcess
    Session/     NipaSession (WatchRepo, Status, WatchChanges + WatchEvents
                 loops, ops, lock/unlock), NipaSettings
  Editor/        Unity glue: NipaVersionControl, NipaHost, NipaWindow,
                 NipaMenus, NipaIcons, NipaBranches, NipaSceneOverlay, MainThread, NipaAssetProcessor,
                 NipaInspectorBar, NipaLocks
  Tests/Editor/  EditMode smoke tests
plugins/unity/dotnet/   Core/Nipa.Core.csproj (netstandard2.1 build of Editor/Core)
                        + NUnit tests with a fake daemon over HttpListener
```

- All daemon I/O runs off the main thread; results are marshalled back with
  `MainThread.Post`. Streams are cancelled in
  `AssemblyReloadEvents.beforeAssemblyReload` and restarted by
  `[InitializeOnLoad]` when Nipa is the active provider.
- The Pending Changes window caches its rows per status version and renders at most 500 rows per list (the Stage all / Unstage all buttons still apply to every row).
- A running daemon from before the JSON bridge cannot be attached; the connector reports its pid and asks for a restart/update instead of spawning a second daemon.
- `WatchChanges` drives status: every notice triggers a `Status` call (cheap,
  read-through) and the cache reports exactly which paths changed.
- Lock decisions live in Core (`LockPolicy`, `NipaSession.IsOpenForEdit` /
  `WritablePaths`); `NipaAssetProcessor` only translates Unity paths and
  callbacks. It never touches `AssetDatabase`; `MakeEditable` blocks (20 s max)
  on the lock RPC because Unity expects a synchronous answer.
- The Inspector bar is drawn from `Editor.finishedDefaultHeaderGUI` rather than
  `IInspectorWindowExtension`, which keeps it independent of that interface's
  signature across Unity versions.
- `VersionControlObject` is never serialized and holds no state; everything
  lives in `NipaHost`.

## Tests

`make unity-test` runs `dotnet test plugins/unity/dotnet` (NUnit, coverlet,
needs the .NET 8 SDK) over `Editor/Core`. The Unity glue is covered by the
EditMode tests in `Tests/Editor` (Window › General › Test Runner).

CI: `.github/workflows/unity-plugin.yml` runs `make unity-test` on every PR and
main push that touches `plugins/unity/**` and fails when `Editor/Core` line
coverage drops below 80%. The EditMode tests are not run in CI yet.
