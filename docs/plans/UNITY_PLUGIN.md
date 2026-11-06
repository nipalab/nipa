# Nipa Unity plugin — implementation plan (phase handoff)

Each phase below is a self-contained brief for a separate agent. Every agent must read **Shared context** first, then only its own phase. Phases run in order: 0 → 1 → 2 → 3 → 4 (each depends on the previous).

---

## Shared context (read before any phase)

**Goal.** Nipa needs editor plugins for Unity, Godot and Unreal. Unity comes first. `docs/INTEGRATIONS.md` sets the architecture: every GUI talks to the local `nipa serve` daemon (`internal/client/daemon`, design in `docs/DAEMON.md`). The daemon owns the status cache, auth and file watcher. Plugins never spawn the CLI per event and never talk to the server directly.

**Decisions already made (don't revisit):**
- **Transport:** a loopback **JSON/HTTP-1.1 bridge** in the daemon. Today the daemon only speaks gRPC over h2c on `127.0.0.1`, which Unity's Mono `HttpClient` can't use. Godot and Unreal will reuse the same bridge.
- **Location and license:** the package lives in `plugins/unity/com.nipalab.nipa/` (OSS, Apache 2.0). The bridge stays in `internal/` (OSS).
- **Unity version:** minimum **2022.3 LTS**, also tested on **Unity 6**.
- **Unity API:** the managed VCS API introduced in Unity 2021.2:
  - `UnityEditor.VersionControl.VersionControlObject` + `[VersionControl("Nipa")]`. This shows up in Project Settings › Editor › Version Control › Mode.
  - Extensions `IIconOverlayExtension`, `IPopupMenuExtension`, `IInspectorWindowExtension` and `ISettingsInspectorExtension`.
  - Combined with `AssetModificationProcessor` (`IsOpenForEdit`, `MakeEditable`, `OnWillSaveAssets`, `OnWillMoveAsset`, `OnWillDeleteAsset`).
  - Checkout = Nipa binary file lock (Perforce-style).

**Daemon facts:**
- Discovery file is `daemon.json` at `os.UserConfigDir()/nipa/` with `{pid, port, token, version}` (`internal/client/daemon/token.go`):
  - Windows: `%AppData%\nipa\`
  - macOS: `~/Library/Application Support/nipa/`
  - Linux: `$XDG_CONFIG_HOME` or `~/.config/nipa/`
- Every RPC needs metadata `x-nipa-daemon-token`, checked by `unaryAuth`/`streamAuth` in `internal/client/daemon/server.go`.
- Working-copy RPCs take an absolute `root` that must first be registered with `WatchRepo`.
- `Status` returns path lists: `staged`, `added`, `modified`, `deleted`, `untracked`, `missing`, `conflicts`, `resolved`, plus `branch`, `head` and `pending`.
- Long operations (`Update`, `Switch`, `Push`, `Merge`, `Revert`, `SetSparse`) stream `OpEvent`s: queued, started, progress, result and failure.
- `WatchChanges` streams `RepoChange{paths}`. Empty `paths` means "refresh everything".
- `WatchEvents` streams teammate events: `pushed`, `lock_released`, `review_requested`.
- `LockStatus` returns `user_id`, `branch`, `default_branch`, `can_release_any`, every visible lock (`locks[]`), and per-changed-binary `files[]` with state `mine`, `theirs`, `needed` or `free`.
- Proxy lock RPCs: `ProxyLockFile`, `ProxyUnlockFile`, `ProxyListFileLocks`.
- Other RPCs: `Conflicts`, `ResolveConflict`, `ReadBlob` (sources `working`, `snapshot`, `revision`), `ProxyCommitLog`, `ProxyBranchList`, `ProxyTagList`.
- The desktop app's daemon attach/spawn logic is in `desktop/internal/connector/connector.go`.
- Unity `.meta` linting is in `internal/unitymeta` (`Lint`, `GUID`). Its only caller today is `desktop/internal/service/unity.go`.

**Repo rules for every agent:**
- Never commit to git.
- New code needs ≥80% test coverage.
- Keep comments to a minimum.
- Go files must be gofmt-clean, and `go vet ./...` must stay clean.
- Update `AGENTS.md` and the relevant `docs/*.md` for anything you add.
- Read `AGENTS.md` before starting.

**Target UX map (all phases):**

| Unity surface | API | Daemon backing |
|---|---|---|
| Mode "Nipa" | `VersionControlObject` (`OnActivate`/`OnDeactivate`/`isConnected`/`Refresh`) | `Ping`, `WatchRepo` |
| Project/Hierarchy icons | `IIconOverlayExtension.DrawOverlay` | `Status` + `WatchChanges`, `LockStatus`, `WatchEvents` |
| Icon right-click menu | `IPopupMenuExtension` | stage, restore, lock, unlock, history |
| Inspector bar | `IInspectorWindowExtension.OnVersionControlBar` | `LockStatus`, `ProxyLockFile`, `ProxyUnlockFile` |
| Settings | `ISettingsInspectorExtension` | binary path, lock policy, poll interval |
| Read-only locked assets | `AssetModificationProcessor.IsOpenForEdit` / `MakeEditable` | someone else's covering lock ⇒ read-only; `MakeEditable` ⇒ lock asset + `.meta` |
| Pending changes window | `EditorWindow`, `MenuItem` | `Status`, `Stage`, `Restore`, `Push`, `Update`, `Switch` |
| After sync | `AssetDatabase.Refresh()` on the main thread | `OpEvent.result` |
| Meta hygiene | warning banner | new `MetaWarnings` RPC |
| Rich diff / merge requests | "Open in Nipa Desktop" | `nipa://` deep links (`docs/DESKTOP.md:219`) |

**Cross-cutting Unity rules:**
- An asset and its `.meta` are always staged, locked and restored together.
- Never call `AssetDatabase` inside the move/delete callbacks.
- All daemon I/O is async off the main thread; marshal results back with `EditorApplication.delayCall`.
- Cancel streams in `AssemblyReloadEvents.beforeAssemblyReload` and restart them from `[InitializeOnLoad]`.
- `VersionControlObject` is never serialized. Settings go to `EditorUserSettings` and `ProjectSettings/NipaSettings.json`.
- The Unity project may be a subfolder of the clone root.

---

## Phase 0 — daemon JSON bridge + MetaWarnings RPC (Go)

**Deliverables**

1. **`internal/client/daemon/jsonbridge.go` (+ `_test.go`)**
   - A second loopback listener on `127.0.0.1:<port>`, started by `Server.Serve` next to gRPC and stopped with it.
   - `Endpoint` (`token.go`) gains a `json_port` field; old readers ignore it.
   - `nipa serve` flag `--json-port` / env `NIPA_DAEMON_JSON_PORT` (0 = random) in `cmd/nipa/serve.go`.
   - **Generic, reflection-driven bridge.** It needs no per-RPC code, so future RPCs are exposed automatically:
     - Walk the `NipaDaemon` service descriptor from `daemonpb`.
     - Route `POST /v1/<Method>`: `protojson`-unmarshal the body into the input type, then invoke through an in-process `grpc.ClientConn` to the daemon's own gRPC listener (`conn.Invoke`, or `conn.NewStream` for server streams).
     - Forward `x-nipa-daemon-token` so `unaryAuth`/`streamAuth` stay the single auth point.
   - **Responses:**
     - Unary RPCs answer `200 application/json`.
     - Server streams answer `application/x-ndjson`: one protojson message per line, flushed per event. A client disconnect cancels the context.
     - `protojson` options: `UseProtoNames: true`, `EmitUnpopulated: true`.
   - **Errors and guards:**
     - Map the gRPC code to an HTTP status; the body is `{"code","message"}`. Reuse `toStatusError` semantics.
     - Reject a non-loopback `Host` or `Origin` with 403 (DNS-rebinding guard).
     - Non-POST requests get 405; unknown methods get 404.
2. **New RPC `MetaWarnings{root} → {warnings[]{path, kind, related, message}}`**
   - Add it to `internal/client/grpc/proto/daemon.proto`, then run `make proto`.
   - Implement it in `internal/client/daemon/unitymeta.go` by moving the logic from `desktop/internal/service/unity.go` (`MetaWarnings`, `maxGUIDChecks`): the daemon's own status, plus snapshot blob reads through `internal/unitymeta.Lint`.
   - Switch the desktop service to the RPC, and regenerate the desktop bindings if their signature changes.
3. **Docs**
   - `docs/DAEMON.md`: JSON bridge section and the new endpoint field.
   - `docs/INTEGRATIONS.md`: rewrite the Unity section to this design (drop "no standard VCS provider API") and make Unity roadmap step 3.
   - `AGENTS.md`: daemon flow paragraph.

**Acceptance**
- `go test ./internal/client/daemon/...` covers:
  - unary calls (`Ping`, `Status`)
  - a stream (`Update` NDJSON events, client cancel)
  - bad token → 401, wrong Host → 403, unknown method → 404, error mapping
  - the ported `MetaWarnings` tests
- Coverage of the new files is ≥80%; `go vet ./...` and `make lint` are clean.
- New spec in `tests/suite` (run with `make test-client`): start `nipa serve`, read `json_port` from `daemon.json`, call `/v1/WatchRepo` and `/v1/Status`, and stream `/v1/Push`.
- `cd desktop && make desktop-test` passes.

---

## Phase 1 — Unity package MVP (connection, status, changes window)

**Depends on:** Phase 0 (`json_port`, `MetaWarnings`).

**Layout:** `plugins/unity/com.nipalab.nipa/`
```
package.json            name com.nipalab.nipa, unity "2022.3", dependency com.unity.nuget.newtonsoft-json
Editor/Core/            Nipa.Editor.Core.asmdef (noEngineReferences: true; pure C#)
  Daemon/Endpoint.cs        daemon.json discovery (per-OS paths above)
  Daemon/Connector.cs       port of desktop/internal/connector: attach → Ping → MinVersion check → spawn `nipa serve` detached (setting → NIPA_BIN → PATH), poll every 100 ms up to 10 s
  Daemon/DaemonClient.cs    HttpClient on json_port: CallAsync<TReq,TRes>, StreamAsync<T> (NDJSON → IAsyncEnumerable), token header, CancellationToken everywhere
  Daemon/Messages.cs        hand-written Newtonsoft DTOs (snake_case) for the RPC subset used
  Status/StatusCache.cs     path → state (staged/added/modified/deleted/untracked/missing/conflict); applies Status snapshots and WatchChanges deltas; change event
  Paths/ProjectPaths.cs     Unity project path ↔ clone-relative path; asset ↔ .meta pairing
Editor/                 Nipa.Editor.asmdef (Editor only, references Core)
  NipaVersionControl.cs     [VersionControl("Nipa", "Nipa")] VersionControlObject + overlay, popup and settings extensions
  NipaWindow.cs             pending changes: staged/unstaged lists, stage/unstage, restore, push with message, update; progress bar from OpEvent; MetaWarnings banner; "Open in Nipa Desktop"
  NipaMenus.cs              Window/Nipa and Assets/Nipa/{Stage, Restore, Refresh}
  MainThread.cs             main-thread dispatcher
  Icons/                    overlay icons (one per state)
Tests/Editor/           Unity EditMode smoke tests (activation, menu validation)
Documentation~/index.md  CHANGELOG.md  LICENSE.md (Apache 2.0)
plugins/unity/dotnet/Nipa.Core.Tests.csproj   NUnit + coverlet; compiles Editor/Core/**/*.cs; fake daemon over HttpListener
```

**Behaviour**
- On activate: connect or spawn the daemon, `WatchRepo(clone root)`, initial `Status`, then subscribe to `WatchChanges`.
- `Refresh()` (editor focus) and `OnDeactivate` close the streams.
- Staging an asset auto-stages its `.meta`; the same applies to restore.
- After any `Update`/`Switch` result, call `AssetDatabase.Refresh()`.
- Makefile target `unity-test`: `dotnet test plugins/unity/dotnet --collect "XPlat Code Coverage"`.

**Acceptance**
- `make unity-test` passes with ≥80% coverage on `Editor/Core`.
- Manual check, in a sample Unity 2022.3 project inside a nipa clone:
  1. Add the package by local path and set Mode = Nipa.
  2. Overlays appear.
  3. Edit → stage → push from the window.
  4. Update refreshes the assets.
- The same flow works on Unity 6.
- `docs/UNITY_PLUGIN.md` is added (install, settings, architecture) and linked from `docs/INTEGRATIONS.md`.

---

## Phase 2 — locking (Perforce-style checkout)

**Depends on:** Phase 1.

**Deliverables**
- **`Editor/Core/Locks/LockPolicy.cs`**
  - A covering-lock check: exact path or directory prefix, mirroring `domain.PrefixCovers`.
  - Scope: global locks apply on the default branch, branch locks on their branch, using `LockStatus.default_branch` and `branch`.
  - States: mine, theirs or free per path, with asset + `.meta` treated as a unit.
  - Strict mode: listed extensions are read-only until locked. Default list: binaries plus `.unity` and `.prefab`.
- **`Editor/NipaAssetProcessor.cs`**
  - `IsOpenForEdit` returns false when someone else holds a covering lock, or when strict mode applies and the path is not locked by me.
  - `MakeEditable` calls `ProxyLockFile` for the asset and its `.meta`; on failure it reports the holder.
  - `OnWillSaveAssets` drops paths locked by others, with a dialog.
  - `OnWillMoveAsset`/`OnWillDeleteAsset` refuse paths locked by others.
- **Inspector bar** (`IInspectorWindowExtension`): lock state and holder name, Lock/Unlock buttons.
- **Overlays:** locked-by-me and locked-by-other icons.
- **Window "Locks" tab:** list of locks, release mine, release any when `can_release_any`.
- **Push pre-check:** `LockStatus.files` with state `needed` or `theirs` blocks the push and offers "Lock N files".
- **Lock cache:** refresh on focus, after lock/unlock, and on `WatchEvents` `lock_released`.
- **Teammate events:** `pushed` shows an "out of date" overlay plus a notification bar with an Update button; `review_requested` shows a notice.

**Acceptance**
- `LockPolicy` and processor-logic unit tests in `dotnet` (keep the logic in Core; the Unity glue stays thin), ≥80% coverage.
- Manual two-user check:
  1. User B locks a prefab, so it is read-only in user A's editor.
  2. A's Lock button fails and names B.
  3. B unlocks, and A's editor updates within the poll interval.
  4. A's lock and unlock succeed.

---

## Phase 3 — branches, tags, conflicts, history

**Depends on:** Phase 2.

**Deliverables**
- **Branch/tag switcher:**
  - A toolbar dropdown in the window, plus a Scene view `ToolbarOverlay`, fed by `ProxyBranchList` and `ProxyTagList`.
  - Switching uses `Switch`, with `tag` set for tags.
  - A detached-tag banner reads `Status.head`.
- **Conflicts tab** (`Conflicts` / `ResolveConflict`):
  - Take mine or theirs for any file.
  - For `.unity`, `.prefab`, `.asset`, `.controller`, `.mat` and `.anim`, run **UnityYAMLMerge** from `EditorApplication.applicationContentsPath + "/Tools/UnityYAMLMerge"` as `merge -p <base> <theirs> <ours> <out>`.
    - Fetch base/ours/theirs with `ReadBlob` (source `revision`, using the revision ids from the `Conflicts` response) into a temp directory.
    - Submit the result with `choice=content`; if markers remain, fall back to a manual pick.
  - Merge/revert pending banner with Finish (push), Abort and Continue/Skip for reverts.
- **Asset history:**
  - `ProxyCommitLog` with `path` in a History panel.
  - "Open diff in Nipa Desktop" through a `nipa://` deep link.

**Acceptance**
- Unit tests for the merge-tool command building and conflict-state mapping, ≥80% coverage.
- Manual check: two branches edit the same prefab, a merge produces a conflict, and UnityYAMLMerge resolves it in-editor.

---

## Phase 4 — project setup and distribution

**Depends on:** Phase 3.

**Deliverables**
- **Setup checker panel:**
  - Asset Serialization = Force Text and visible meta files.
  - A missing `.nipaignore` gets an offer to write the Unity template: `Library/ Temp/ Logs/ Obj/ Build/ Builds/ UserSettings/ MemoryCaptures/ *.csproj *.sln .vs/ .idea/`.
- **Install docs** in `docs/UNITY_PLUGIN.md`:
  - Git URL `https://<repo>.git?path=/plugins/unity/com.nipalab.nipa#unity-vX.Y.Z`.
  - Tarball install.
  - OpenUPM (later).
- **Makefile:** `unity-pack` builds the tarball with `npm pack` in the package directory.
- **CI `.github/workflows/unity-plugin.yml`:**
  - `dotnet test` with coverage on every PR that touches `plugins/unity/**`.
  - An optional game-ci EditMode job, gated on a Unity license secret.
- **Docs:** `README.md` mentions the plugin; `AGENTS.md` gets an architecture bullet for `plugins/unity`.

**Acceptance:** CI green; a fresh Unity 6 project installs the package from the git URL and passes the setup checker.

---

## Later (not in scope)

Godot and Unreal plugins reuse the Phase 0 JSON bridge. Port the `Editor/Core` design (connector, status cache, lock policy) as their template.
