# Nipa Unity plugin

`com.nipalab.nipa` is a Unity Editor package that adds Nipa as a version
control provider: status overlays in the Project window, a Pending Changes
window and stage/restore/push/update from the editor. It talks to the local
`nipa serve` daemon through its JSON/HTTP bridge (`docs/DAEMON.md`), never to
the server or the CLI directly. Plan and later phases (locking, branches,
conflicts, setup checker): `docs/plans/UNITY_PLUGIN.md`.

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

## Settings

In **Project Settings › Editor › Version Control** (Nipa section):

| Setting | Stored in | Notes |
|---|---|---|
| nipa binary | `EditorUserSettings` (per user) | Empty = `NIPA_BIN`, then `PATH` |
| Poll interval | `ProjectSettings/NipaSettings.json` (shared) | Clamped to 15–3600 s |

## Architecture

```
plugins/unity/com.nipalab.nipa/
  Editor/Core/   engine-free C# (asmdef noEngineReferences)
    Daemon/      Endpoint (daemon.json), DaemonClient (JSON + NDJSON), Ops,
                 Connector (attach, version check, spawn, poll), Messages
    Status/      StatusCache (path -> flags, folder aggregates, deltas)
    Paths/       ProjectPaths (project <-> clone paths, .meta pairing)
    Session/     NipaSession (WatchRepo, Status, WatchChanges loop, ops),
                 NipaSettings
  Editor/        Unity glue: NipaVersionControl, NipaHost, NipaWindow,
                 NipaMenus, NipaIcons, MainThread
  Tests/Editor/  EditMode smoke tests
plugins/unity/dotnet/   Core/Nipa.Core.csproj (netstandard2.1 build of Editor/Core)
                        + NUnit tests with a fake daemon over HttpListener
```

- All daemon I/O runs off the main thread; results are marshalled back with
  `MainThread.Post`. Streams are cancelled in
  `AssemblyReloadEvents.beforeAssemblyReload` and restarted by
  `[InitializeOnLoad]` when Nipa is the active provider.
- `WatchChanges` drives status: every notice triggers a `Status` call (cheap,
  read-through) and the cache reports exactly which paths changed.
- `VersionControlObject` is never serialized and holds no state; everything
  lives in `NipaHost`.

## Tests

`make unity-test` runs `dotnet test plugins/unity/dotnet` (NUnit, coverlet,
needs the .NET 8 SDK) over `Editor/Core`. The Unity glue is covered by the
EditMode tests in `Tests/Editor` (Window › General › Test Runner).
