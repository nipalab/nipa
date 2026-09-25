# Nipa Client Integrations

Design notes for Windows File Explorer integration and game engine support (Unity, Unreal, Godot).

---

## Core Architecture: local daemon

All integration points (Explorer, Unity, Unreal, Godot) talk to a single **local `nipa` daemon**, not spawn the CLI per event.

**Daemon responsibilities:**
- Expose a loopback gRPC service (reuses `internal/client/grpc` transport types and `internal/client/usecase` orchestration).
- Own authentication/session via `securestorage` (keyring-backed token store).
- Run a file-system watcher (`fsnotify` / ReadDirectoryChangesW) per clone root and maintain an incremental **status cache** (staged / Modified / Untracked / Missing). Status is read 1000x more often than it changes — this is the difference between a snappy UI and scanning 100k binary assets per frame.
- Serve async progress callbacks so engine UIs and Explorer never block.

**Why not spawn the CLI per event:**
- One shared auth/session.
- One status cache (avoid repeated full scans).
- Async, non-blocking calls (critical: never freeze the game engine UI or Explorer on a large download).
- Single surface to later add HTTP/REST/WebSocket clients.

**Wire format:** Loopback gRPC with a well-known auth token from `securestorage`. The proto surface is a new file (`client/grpc/daemon.proto`) reusing types from `server.proto` but scoped to the local daemon operations (status, stage, push, update, switch, branch, progress streaming).

---

## Windows File Explorer Integration

### Don't
- Don't write shell extensions in Go as in-process COM objects — Go's runtime/GC makes Explorer shell extensions brittle.

### Do
Mirror TortoiseGit's architecture:

1. **C++ ATL/COM shell extensions** (context menu handler + column/overlay handler) — thin, zero-logic; they query the daemon for status and shell out for actions.
2. **Overlay icons:** The OS allows only ~15 overlay icons system-wide across all apps. Don't register one per state. Plan for **one overlay icon** (e.g., "dirty/nipa") plus a **column handler** or context-menu `Nipa > Status` showing the real A/M/?/! detail.
3. **Lighter MVP if COM is too heavy:** `HKCU\Software\Classes\*\shell\Nipa` registry menu items that run the CLI with the folder as cwd, plus a small WinUI/WPF window. No overlays, but ships fast — good step 1 to validate before investing in COM.

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

### Godot (do first)

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
| Explorer overlays use one icon + column detail | 15-overlay system limit; dedicated overlay per state exhausts the budget. |
| Plugins built on a public daemon API | Keep the *core daemon + gRPC API* in the OSS client (`nipa`/`nipad`); plugins (C++ Explorer COM, Unity C#, Godot, Unreal) build on this stable public interface. |
| Licensing | Explorer/engine plugins are natural `ee/` (enterprise) candidates; core daemon + gRPC API stays OSS. |
| Headless CI test mode | Engine plugins expose a headless mode so e2e tests (existing `go test`-style) can drive them via the daemon API without editors. |

---

## Roadmap

| Step | Deliverable | Value |
|------|-------------|-------|
| 1 | `nipa serve` daemon — loopback gRPC service, status watcher + cache, token-based auth | Enables everything else |
| 2 | Godot `EditorVCSInterface` plugin (calls daemon) | Proves the daemon API including binary asset workflows |
| 3 | Explorer registry-menu MVP → WinUI window → COM context menu + overlay icon | Windows dev UX |
| 4 | Unity C# Editor extension | Main game studio UX |
| 5 | Unreal utility plugin → full `ISourceControlProvider` | AAA studios |

---

## Notes for Later

- Status watcher: use `github.com/fsnotify/fsnotify` on Linux/macOS, `ReadDirectoryChangesW` (via goroutine + syscall) on Windows.
- Daemon stop/shutdown: signal `SIGTERM` / `WM_QUIT` / `WM_CLOSE`; use a grace period to drain in-flight operations.
- Testing: daemon and plugins should be testable in headless CI mode without GUI editors.
- Overlay icon files: `.ico` assets for shell extension registration go in `ee/explorer/icons/`; document icon states.
