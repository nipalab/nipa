# Nipa Client Integrations

Design notes for the **P4V-style desktop client** (`nipa-gui`), Windows File Explorer integration and game engine support (Unity, Unreal, Godot) — all built on one local daemon API.

---

## Core Architecture: local daemon

Every GUI surface (desktop client, Explorer, Unity, Unreal, Godot) talks to a single **local `nipa` daemon**, not spawn the CLI per event.

**Daemon responsibilities:**
- Expose a loopback gRPC service (reuses `internal/client/grpc` transport types and `internal/client/usecase` orchestration).
- Own authentication/session via `securestorage` (keyring-backed token store).
- **Proxy remote operations server-side** (branches, release tags, merge requests, reviews, file locks, ACL/permissions) so GUI clients only ever know the daemon, never the direct server protocol.
- Run a file-system watcher (`fsnotify` / ReadDirectoryChangesW) per clone root and maintain an incremental **status cache** (staged / Modified / Untracked / Missing). Status is read 1000x more often than it changes — this is the difference between a snappy UI and scanning 100k binary assets per frame.
- Serve async progress callbacks (server-streaming) so engine UIs, Explorer and the desktop client never block.

**Why not spawn the CLI per event:**
- One shared auth/session.
- One status cache (avoid repeated full scans).
- Async, non-blocking calls (critical: never freeze the game engine UI, Explorer or a rendering Qt view on a large download).
- Single surface to later add HTTP/REST/WebSocket clients.

**Wire format:** Loopback gRPC with a well-known auth token from `securestorage`. The proto surface is a new file (`internal/client/grpc/proto/daemon.proto`) reusing types from `server.proto` but scoped to the local daemon operations (status, stage, push, update, switch, branch, progress streaming).

---

## Desktop Client (`nipa-gui`, C++ / Qt Widgets)

A standalone GUI client in the spirit of **P4V** — the primary visual workflow for non-CLI users (artists, TDs, build engineers).

### Stack decision

**C++ / Qt Widgets** (Qt 6, CMake, top-level `desktop/`, built by `make desktop`). This is the exact P4V lineage:
- Native performance and responsiveness on 100k-file trees; no QML, Python, or web view.
- Qt's `QTreeView`/`QSqlTableModel`-style model/view is the best available toolkit for giant depots, pending lists and side-by-side diffs.
- Business logic stays in Go — the GUI is a thin, dumb view over the daemon API. No VCS logic in C++.

### Connection contract

- The client **requires the daemon**: it looks up the loopback endpoint + token (`securestorage`), and auto-spawns `nipa serve` when not running, waiting for readiness before showing the workspace.
- **No direct server connections from Qt code** — auth, status cache and progress all live in the daemon, so every GUI (client, Explorer, engine plugins) has one source of truth.

### Feature phases

**Phase A — MVP core:**
- Workspace/depot tree browser (lazy-loaded via `GetTreeManifest`).
- Status view (A/M/?/!) backed by the daemon status cache, not rescans.
- Stage/unstage → staged set; Submit (push) and Update (sync) with live progress.
- File history + unified diff viewer; binary-lock list / lock / unlock.

**Phase B — full parity ambition (target P4V feature coverage):**
- Merge-request lifecycle: create, review decisions, inline diff comments, merge/close (reuses the REST/MR model from the web UI).
- Revision graph (`WalkCommits` two-parent walk), branch management (create/switch/protect), release tags (list/create/delete, detached `switch --tag` checkout), pending multi-target revert control, sparse-checkout editor and ACL/admin panels.

### P4V ↔ Nipa mapping

| P4V concept | Nipa equivalent |
|-------------|-----------------|
| Depot / Workspace tree | Daemon's tree surface (`GetTreeManifest`, recursive + path scoped) |
| Pending Changelists | Local staged set (`staged_files` + `Snapshot`), per clone |
| Reconcile Offline Work | Daemon status cache — fsnotify + `stat_cache` fingerprints |
| Check Out / Lock | Binary file locks (`LOCKING.md`, server-enforced) |
| Submit | Push (`base_commit_id` delta) |
| Get Latest Revision | Update / Switch |
| Integrate Source | Merge request lifecycle (`BRANCH_MERGING.md`) |
| Label (release) | Release tags (`nipa tag`, `nipa switch --tag`) |
| Revision graph | `WalkCommits` (newest-first, both parents) |
| File History / Time-lapse view | `GetCommitLog` + commit diffs (`diff.TreeDiff`) |
| Restricted/offline workspaces | Sparse-checkout prefixes in `.nipa/config` |

---

## Windows File Explorer Integration

### Don't
- Don't write shell extensions in Go as in-process COM objects — Go's runtime/GC makes Explorer shell extensions brittle.

### Do
Mirror TortoiseGit's architecture:

1. **C++ ATL/COM shell extensions** (context menu handler + column/overlay handler) — thin, zero-logic; they query the daemon for status and **launch `nipa-gui`** (or shell out to the CLI when the GUI isn't running) for actions.
2. **Overlay icons:** The OS allows only ~15 overlay icons system-wide across all apps. Don't register one per state. Plan for **one overlay icon** (e.g., "dirty/nipa") plus a **column handler** or context-menu `Nipa > Status` showing the real A/M/?/! detail — the full status detail lives in the desktop client.
3. **Lighter MVP:** `HKCU\Software\Classes\*\shell\Nipa` registry menu items that open `nipa-gui` (or run the CLI) with the folder as cwd/pinned path. No overlays, but ships fast — good step 1 to validate before investing in COM.

There is deliberately **no separate WinUI/WPF companion app**; the Qt client is the single GUI hub.

---

## Game Engine Integrations

All three engines load assets from files on disk — the materialized working copy fits directly. No virtual/VFS checkout layer needed; the win is **editor UI + status awareness**.

### Unity

**No standard VCS provider API** — Plastic and Git integrations are closed implementations.

- Build a custom **Editor extension (C#):** menu items, toolbar, an EditorWindow replacing `nipa status`, Project-view context menus, `AssetDatabase`-driven refresh after `update`/`switch`, and an import-time on-demand fetch hook for missing binary assets.
- Straightforward, self-contained, no ABI coupling.

### Unreal Engine

Unreal has a real abstraction: `ISourceControlProvider` + `FSourceControlOperation` (the same interface Perforce plugs into).

**Pragmatic path:**
1. **First:** a utility Editor plugin — context menus, a status panel, `nipa switch/update/push` actions. This ships value fast without implementing the full provider surface.
2. **Later:** graduate to a full `ISourceControlProvider` only when the integration needs to sit inside Unreal's native Source Control UI (e.g. the Source Control pane).

### Godot (first engine plugin)

Godot's built-in VCS abstraction `EditorVCSInterface` gives exactly the methods needed: `get_status`, `get_modified_files`, `stage_file`, `commit`, `push`, etc.

- Implement it in GDScript or C++ as an `EditorPlugin` that calls the daemon.
- Clean, documented, testable headless — proves the daemon API incl. binary assets.

---

## Decisions Locked In

| Decision | Rationale |
|----------|-----------|
| Keep the on-disk working copy model | Unity/Unreal/Godot load by file path; virtual VFS for assets is too risky. Content-address transfers (chunk dedupe) instead. |
| Async everywhere with progress callbacks | Engines block the main thread on sync IO, which reads as "Nipa crashed the editor." |
| Status via daemon cache + file watcher | Status is read 1000x more than it changes; scan-on-read at scale (100k+ files) is unusable. |
| Desktop client in C++ Qt Widgets | P4V lineage; native perf on huge trees; best model/view + diff widgets; no GUI logic outside the daemon API. |
| Qt client requires the daemon | One status cache, auth store and watcher shared by all GUIs; auto-spawn makes this invisible. |
| Qt client is the GUI hub | Explorer menus/overlays and engine plugins delegate to `nipa-gui`; no separate WinUI/WPF app. |
| Full-parity ambition for the client | MVP core first, then MR/revision-graph/sparse/ACL panels to reach P4V feature coverage. |
| Explorer overlays use one icon + column detail | 15-overlay system limit; dedicated overlay per state exhausts the budget. |
| Plugins built on a public daemon API | Keep the *core daemon + gRPC API* in the OSS client (`nipa`/`nipad`); plugins (Qt client, C++ Explorer COM, Unity C#, Godot, Unreal) build on this stable public interface. |
| Licensing | `desktop/` (`nipa-gui`) is **OSS** — it is the adoption surface, like P4V is free. Explorer COM shell extensions and engine plugins are natural `ee/` (enterprise) candidates; core daemon + gRPC API stays OSS. |
| Headless CI test mode | Daemon, Qt client (`QT_QPA_PLATFORM=offscreen`) and engine plugins expose a headless mode so e2e tests can drive them via the daemon API without GUIs/editors. |

---

## Roadmap

| Step | Deliverable | Value |
|------|-------------|-------|
| 1 | `nipa serve` daemon — loopback gRPC service, status watcher + cache, token-based auth, remote-op proxy | Enables everything else |
| 2 | `nipa-gui` MVP (Phase A) — tree browser, status, stage/submit/update, history/diff, locks | Flagship UX, ships early |
| 3 | Godot `EditorVCSInterface` plugin (calls daemon) | Proves the daemon API including binary asset workflows |
| 4 | Explorer registry-menu MVP → COM context menu + overlay icon | Windows dev UX |
| 5 | Unity C# Editor extension | Main game studio UX |
| 6 | Unreal utility plugin → full `ISourceControlProvider` | AAA studios |
| (ongoing) | `nipa-gui` Phase B parity — MR lifecycle, revision graph, branches, pending controls, sparse editor, ACL admin | P4V-level feature coverage |

---

## Notes for Later

- Status watcher: use `github.com/fsnotify/fsnotify` on Linux/macOS, `ReadDirectoryChangesW` (via goroutine + syscall) on Windows.
- Daemon stop/shutdown: signal `SIGTERM` / `WM_QUIT` / `WM_CLOSE`; use a grace period to drain in-flight operations.
- Daemon spawn/lifecycle: `nipa-gui` owns the daemon when it auto-spawns it (readiness probe, clean shutdown on exit, orphan handling when the GUI crashes).
- Qt client headless testing: build with `QT_QPA_PLATFORM=offscreen`, drive the models/tests against the same daemon API (fake or in-process daemon) in CI; no editor or real server needed.
- Packaging: Windows (MSI / Qt Installer Framework) bundles Qt libs but assumes `nipa`/`nipa serve` comes from the normal CLI install; document the spawn contract.
- Testing: daemon, GUI and plugins should be testable in headless CI mode without GUI editors.
- Overlay icon files: `.ico` assets for shell extension registration go in `ee/explorer/icons/`; document icon states.
