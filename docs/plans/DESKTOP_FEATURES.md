# Nipa Desktop — next features plan (phase handoff)

Each phase is a self-contained brief for a separate agent and one PR. Every agent reads **Shared context** first, then only its own phase. Phases are ordered by value per effort; the dependency column says what must land first, everything else can be reordered.

| # | Phase | Size | Touches | Depends on | Status |
|---|---|---|---|---|---|
| 1 | Pre-push checks (budgets, broken references) | S | daemon, desktop | — | Done |
| 2 | Generate push message and MR description | M | client, daemon, desktop | — | Done |
| 3 | Storage and cleanup | S | daemon, desktop | — | Done |
| 4 | Files tab (project browser) | L | daemon, desktop | — | Done |
| 5 | Where used and provenance | M | daemon, desktop | 4 | Done |
| 6 | Search, command palette, shortcuts | M | daemon, desktop | 4 | Done |
| 7 | Lock watch and release requests | M | server, daemon, desktop, web | — | Done |
| 8 | Shelves | XL | client, CLI, daemon, desktop | — | Done |
| 9 | Member pickers and project activity feed | M | server, daemon, desktop | — | Done |
| 10 | Table filters and `.xlsx` diff | M | preview, desktop | — | Done |
| 11 | Blame, open at line, Godot scene merge | M | client, daemon, desktop | — | Done |
| 12 | Video, font and embedded-thumbnail viewers | L | preview, daemon, desktop | — | Done |
| 13 | Simple mode and first-run tour | M | desktop | 4 | Done |
| 14 | Localization | L | desktop | after 1–13 | To do (after 13) |
| 15 | Review comments on image regions and audio ranges (deferred) | XL | server, web, daemon, desktop | owner decision | Deferred |

---

## Shared context (read before any phase)

**Goal.** The desktop app (`desktop/`, design in `docs/DESKTOP.md`) already covers the version-control loop: changes, asset diffs, locks, history, branches, tags, merge requests, conflicts, tray, updates. This plan adds what artists, designers, audio, programmers and leads still miss around that loop.

**Architecture rules (don't revisit):**
- The app never talks to the server and never runs the CLI per action. Everything goes through the `nipa serve` daemon (`internal/client/daemon`, proto `internal/client/grpc/proto/daemon.proto`, design in `docs/DAEMON.md`).
- A new capability is a vertical slice:
  1. logic in a client usecase or pure package (`internal/client/usecase`, `internal/client/assetscan`, `internal/preview`, ...);
  2. a daemon RPC (`make proto`), shared slot for reads and exclusive slot (`runOp`, `OpEvent` stream) for anything that rewrites the working copy;
  3. a method on `desktop/internal/service` (plain Go behind seams, unit-tested with a fake daemon client);
  4. regenerated TS bindings in `desktop/frontend/bindings` (committed, CI checks freshness), a wrapper in `frontend/src/api/desktop.ts`, demo data in `frontend/src/demo`;
  5. React UI with TanStack Query, tests with vitest.
- Server-facing calls from the daemon are `Proxy*` RPCs forwarding `greet.*` messages over `Client.ServiceClient()`.
- The JSON bridge exposes every daemon RPC automatically, so engine plugins get new RPCs for free. Keep request and response messages protojson-friendly.
- `internal/preview`, `internal/assetdiff`, `internal/assetrefs`, `internal/provenance` stay free of client imports.
- `internal/` never imports `ee/`. Server changes must work on sqlite and postgres (`make migrate-create`, `make sqlc`).

**Existing daemon RPCs:** `Status`, `Stage`, `Restore`, `Update`, `Switch`, `Push`, `Merge`, `Revert`, `Conflicts`, `ResolveConflict`, `MergeGodotResource`, `Diff`, `DiffFiles`, `ReadBlob`, `Preview`, `WatchChanges`, `WatchEvents`, `LockStatus`, `MetaWarnings`, `AccountStatus`, `Login`, `LoginSSH`, `ListProjects`, `RemoteBranches`, `RemoteTree`, `Clone`, `SetSparse`, and `Proxy*` for branches, tags, tree manifest, commit log/get/walk, merge base, merge requests (list, create, get, update, merge, close, reopen, reviews, threads, comments, checks, timeline) and file locks.

**Client logic that exists but has no daemon RPC yet:**
- `usecase.Push.Plan(ctx, root)` → `domain.Plan` (the `push --dry-run` change list).
- `assetscan.CheckChanges` (budgets), `assetscan.Impact` / `Refs` / `Graph` (references), `assetscan.ScanProvenance`.
- `cli/assets.go` `pushImpact` and `cli/dryrun.go` `planReport` glue the three together for `push --dry-run`.
- `cli/generate.go` (`collector`, `generateText`, `generateCommitMessage`, `generateMergeRequest`) over `aicontext`, `aiprompt`, `llm`, `config.ResolveLLM`.
- `usecase.GC` (`nipa gc`).

**Desktop layout:** tabs are declared in `frontend/src/components/RepoView.tsx` (`TabId`); the push box is `components/changes/CommitBox.tsx`; settings live in `desktop.json` through `internal/workspace` and `service/settings.go`; team events arrive through `service/activity.go`.

**Repo rules for every agent:**
- Never commit to git. Never create branches or worktrees.
- New code needs ≥80% test coverage (Go and TypeScript).
- Keep comments to a minimum. Go files gofmt-clean, `go vet ./...` clean.
- `make desktop-test`, plus `go test ./...` for anything outside `desktop/`.
- Every new screen gets demo data so `npm run screenshots` keeps working.
- Update `AGENTS.md`, `docs/DESKTOP.md` (add the phase to the roadmap as done) and `docs/DAEMON.md` for new RPCs.

---

## Phase 1 — Pre-push checks

**Why.** Budget violations and broken references are only visible in `nipa push --dry-run`. Artists and designers push from the app and never see them.

**Deliverables**
1. Move the report assembly out of the CLI: `assetscan.PushReport(ctx, diff, root, plan)` returning budget violations and `assetrefs.Impact`. `cli/assets.go` and `cli/dryrun.go` call it; CLI output stays byte-identical.
2. Daemon unary `PushPlan(root)` under the shared slot → `{changes[], asset_violations[], asset_impact{broken[], newly_unused[], truncated}, warnings[]}`. A budget or impact failure is a warning string, never an error (same as the CLI).
3. Service `PushChecks(root)`; frontend query keyed on the staged set, refetched on `repo:changed`, debounced.
4. UI: a collapsible "Checks" block above the Push button in `CommitBox` with two groups (Over budget, Broken references / No longer used). Each row names the file, the rule and the limit, and clicking selects the file in the list. Warn only: the Push button stays enabled and its label becomes "Push anyway" while violations exist.
5. Hidden when the clone has no `.nipaassets.yml` and no engine assets changed.

**Tests.** Daemon handler with stub runners; service with fake client; `CommitBox` rendering for none, violations, impact, truncated.

---

## Phase 2 — Generate push message and MR description

**Why.** Non-programmers write the weakest messages; the generator already exists for the CLI.

**Deliverables**
1. New package `internal/client/generate` holding the non-interactive part of `cli/generate.go` (`Commit(ctx, root)`, `MergeRequest(ctx, root, source, target)` → text + model). The CLI keeps only flags and the `y/N` confirmation.
2. Daemon unary `Generate(root, kind=commit|mr, source, target)` → `{title, text, model}`. Not configured → `FailedPrecondition` with a clear message.
3. API key for a GUI process: env vars are not available to an app started from the dock. Add an optional key in the OS keyring through `securestorage` (`llm-api-key`), resolved after `NIPA_LLM_API_KEY`. The config file still never stores a key.
4. Settings dialog section "AI assistance": endpoint, model, key (write-only field, "Saved" state, Clear), Test button. Saving goes through a `SetLLM` service method writing the shared client config, like `SetUploadWorkers`.
5. UI: a "Generate" button in the push box and in the MR create dialog. The draft fills the textarea for editing; it never pushes by itself. The `Nipa-Generated: <model>` trailer is added only if the user keeps the text unchanged or edited, removed when they clear it.
6. Buttons hidden while no endpoint is configured, with a one-line hint in Settings.

**Tests.** `generate` package with stub differ and LLM; daemon handler; settings round trip; button states.

---

## Phase 3 — Storage and cleanup

**Deliverables**
1. Daemon unary `DiskUsage(root)` → working tree bytes (sum of snapshot sizes, no walk), `.nipa/objects` bytes and object count, pinned paths. `GC(root)` → `{objects, bytes, skipped}` over `usecase.GC`.
2. Details tab card "Storage": the three numbers, "Clean up" button, the freed amount as a toast, "Repository is busy, try again" for `skipped`.
3. Sparse hint: when the clone is a full checkout larger than a threshold, link to the sparse editor.

**Tests.** Daemon handlers with temp repos; card states.

---

## Phase 4 — Files tab (project browser)

**Why.** The app only shows pending changes. Artists and audio need to see the project, lock before editing and look at a file's history without changing it first.

**Deliverables**
1. Daemon unary `ListFiles(root, dir)` from the local snapshot plus status: one directory level, entries `{name, path, is_dir, size_bytes, is_binary, kind (preview.Classify), status, lock{state, held_by_name}, children_changed}`. Must not load the whole snapshot per call on 10k+ file clones: add a localrepo query by `tree_path`, and join status from the cached `WorkingCopy` state.
2. Daemon `Preview` already takes one side; confirm a nil `old` returns just the new image and use it for thumbnails (`max_size` 128). Thumbnails are lazy (visible rows only, TanStack Virtual) and cached by file hash.
3. Service `Files(root, dir)`, `FileInfo(root, path)` (snapshot metadata + last commit via `ProxyCommitLog` with `path`, limit 1).
4. UI: new `files` tab, first in the tab list.
   - Left: lazy folder tree. Right: list or thumbnail grid toggle, sortable columns (name, kind, size, status, lock).
   - Detail pane for the selected file: single-version viewer (reuse the diff viewers with only the new side), size, last change, lock owner.
   - Actions (toolbar and context menu): Open, Reveal in file manager, Lock / Unlock, History (jumps to the History tab filtered by path), Copy path, Copy `nipa://` link.
   - "Get this version": from the file's history, write a chosen revision over the working file (daemon `RestoreRevision(root, path, commit_id)` under the exclusive slot, content through the commit-scoped download; the file then shows as modified). Confirmation when the file has local changes.
5. Sparse clones: folders outside the sparse set are listed greyed out from `RemoteTree` with "Download this folder" (`SetSparse` add).
6. Deep link `nipa://.../file/<path>` opens the tab at that file.

**Tests.** localrepo query, daemon handlers, `RestoreRevision` usecase (clean, dirty, binary, missing revision), tree and grid components, virtualization with 10k entries.

---

## Phase 5 — Where used and provenance

**Deliverables**
1. Daemon unary `AssetRefs(root, path)` over `assetscan.Refs` → `{uses[], used_by[], unknown[], truncated, partial}`. Cache the graph per root in the daemon and drop it on any `WatchChanges` notice touching a source or sidecar.
2. Daemon unary `Provenance(root, paths[])` → per path `{status (recorded|modified|edited|missing), tool, model, seed, source, recorded_at, required}`.
3. UI in the Files tab detail pane and the Changes screen side panel:
   - "References" section: Used by / Uses lists, each row navigates to that file. "Used by 0" on a deletable asset reads "Not referenced anywhere".
   - "Origin" section when a record exists or a rule requires one. Missing-but-required shows a warning and an "Add record…" form (`provenance set` equivalent through a `SetProvenance` RPC that writes and stages `.nipaprovenance.yml`).
4. Delete safety: discarding or staging a deletion of an asset with referrers shows the referrer count in the confirmation.

**Tests.** Daemon cache invalidation; service mapping; panels for sparse (`partial`) and truncated graphs.

---

## Phase 6 — Search, command palette, shortcuts

**Deliverables**
1. Daemon unary `SearchFiles(root, query, limit)` over snapshot paths (case-insensitive subsequence match, ranked by basename match first; SQL `LIKE` prefilter then rank in Go; limit 50).
2. Palette (Ctrl/Cmd+K) with sections: Files (all opened clones), Branches and tags, Merge requests (server `search` filter that already exists), Commits (message match within the loaded history page plus exact id/hash lookup), Actions (Update, Push, Switch branch…, New merge request, Lock file…, Settings, each tab).
3. Shortcut map in one module (`lib/shortcuts.ts`): tab switching Ctrl+1..7, Update, focus push message, stage/unstage selected (Space), next/previous file (J/K or arrows), refresh. A "Keyboard shortcuts" sheet under `?`.
4. Results navigate through the existing `Intent` mechanism in `RepoView`.

**Tests.** Ranking function, palette keyboard navigation, shortcut conflicts with text inputs.

---

## Phase 7 — Lock watch and release requests

**Part A, client only**
1. "Notify me when free" on any lock held by someone else: watched paths stored per clone in `desktop.json`; the existing `lock_released` team event raises a notification with a "Lock it now" action, then drops the watch.

**Part B, server**
1. Table `file_lock_requests(id, lock_id, requester_id, message, created_at)`, unique per (lock, requester), removed with the lock. Both dialects.
2. `usecase.FileLock.RequestRelease` (read permission on the path, not the holder, rate limited through the existing `api_user` scope). REST `POST .../locks/{id}/requests`, gRPC `RequestFileLockRelease`; `ListFileLocks` carries `requests[]`.
3. Emits a `lock.release_requested` webhook and an email to the holder through `EmailNotifier` (respects `notify_email`).
4. Daemon: `ProxyLockRequestRelease`; `WatchEvents` adds `lock_requested` for locks the caller holds.
5. Desktop: "Ask to release" with an optional message on locks held by others; the holder gets a notification and a badge on their lock row with "Release". New notification kind in settings.
6. Web SPA Locks page shows the request count and the same button. CLI `nipa lock request <path> [-m]`.

**Tests.** Usecase (gomock), both repositories, e2e spec in `tests/suite`, daemon events, desktop views.

---

## Phase 8 — Shelves

**Why.** Switching branches or pulling with half-finished binary work has no safe answer today.

**Design (local only, v1)**
- A shelf lives in `.nipa/shelves/<id>/`: `manifest.json` (`name`, `created_at`, `branch`, `base_commit_id`, `base_tree_hash`, entries `{path, status, mode, hash, encoding, chunks[], staged}`) and the content as loose chunks in the shelf's own `objects/` directory (so the cache GC never has to know about it, and deleting a shelf is deleting a folder).
- `usecase.Shelve.Create(root, name, paths)` chunks the selected modified/added/deleted files with `chunker.Encode`, writes the shelf, then restores those paths (tracked → `Restore`, untracked-added → removed, staged entries unstaged). All or nothing: the working copy is touched only after the shelf is fully written and fsynced.
- `Apply(root, id, keep)` runs the shared three-way core (`threeway.go`: base = shelf base tree, ours = current snapshot + working state, theirs = shelf). Conflicts go through a new pending state `shelf_state`, reusing the Conflicts tab. `keep=false` deletes the shelf after a clean apply.
- `List`, `Drop`, `Show` (changes of a shelf as `diff` entries so the viewers can render shelf vs base).
- Refuse while a merge or revert is pending. Sparse clones are supported (paths are inside the set by construction). Locks are untouched by shelving; the manifest records which paths the user held locks on only for display.
- Server-side shared shelves are out of scope.

**Deliverables**
1. Usecase + localrepo helpers, `.nipa/lock` shared for create/apply.
2. CLI `nipa shelve [-m name] [-- paths]`, `nipa shelve list|show|apply [--keep]|drop`.
3. Daemon: `ShelveCreate` / `ShelveApply` (exclusive, `OpEvent`), `ShelveList` / `ShelveDrop` / `ShelveFiles`.
4. Desktop:
   - Changes screen: "Shelve…" for the selection or everything.
   - "Shelves" section (collapsible, under the file list) with Apply, Apply and keep, Delete, and a preview using the existing viewers.
   - Switching branch, checking out a tag or updating with local changes that would be overwritten offers "Shelve and continue".
5. e2e specs in `tests/suite` (shelve, switch, apply, conflict path) and MCP is left alone.

**Tests.** Crash safety (failure between write and restore leaves work intact), binary and text round trips, conflict path, apply on a different branch.

---

## Phase 9 — Member pickers and project activity feed

**Member pickers.** The MR create dialog takes raw user ids today (`MergeRequestsView.tsx`, "user ids" inputs).
1. Server gRPC `ListProjectMembers(context, query, limit)` → users with read access (org members + rule subjects, name + id only, read-gated). Reuse the REST query if one exists for the SPA picker; otherwise add both.
2. Daemon `ProxyProjectMembers`; desktop `MemberPicker` (search, avatars by initials, multi-select) used for reviewers and assignees at create time and in the MR sidebar (assign, request review).

**Activity feed.** Leads want "what happened today" across branches.
1. Service `ProjectActivity(root, since)` composed client-side: commit log of each recently updated branch (cap 10 branches × 30 commits), open and recently merged MRs, current locks. No server change.
2. UI: an "Activity" tab with a day-grouped timeline, filters by person and kind, each row linking into History, MRs or Locks.

**Tests.** Usecase + handler for the new RPC on both repositories; feed merge and ordering logic; picker.

---

## Phase 10 — Table filters and `.xlsx` diff

1. Frontend only: in `TableDiff`, "Changed rows only" toggle, column filter, text filter, jump to next/previous change, sticky header and key column, changed-cell count per column.
2. `internal/preview`: `ParseWorkbook` for `.xlsx` (stdlib `archive/zip` + `encoding/xml`: `workbook.xml`, `sharedStrings.xml`, sheet XML; cached values, no formula evaluation; caps on sheets, rows, cells and decompressed size, bomb tests, fuzzed). `Classify` maps `.xlsx` to the table kind.
3. `assetdiff` table result gains `sheets[]`; daemon and service pass a `sheet` selector; UI sheet tabs with a changed marker per sheet.

---

## Phase 11 — Blame, open at line, Godot scene merge

1. **Blame.** `usecase.Blame(root, path, rev)`: walk `GetCommitLog` with `path` (capped at the server's 500-commit scan), read each version through the commit-scoped download and attribute lines with `diff.Lines`, newest first, stopping when every line is attributed. Text files only, size cap 2 MiB. CLI `nipa blame <path>`, daemon server-streaming `Blame` (progress then result), desktop gutter toggle in the text viewer (author, age, click → commit).
2. **Open at line.** External tool templates in settings accept `{file}` and `{line}`; text diff rows get "Open in editor" using the row's new-side line.
3. **Godot scene merge.** Conflicts tab offers "Merge scene" for conflicted `.tscn`/`.tres` through the existing `MergeGodotResource`, falling back to the text resolver when it reports conflicts (`docs/DESKTOP.md` already names this as adoptable).

Reporting merge-request status checks from the app is intentionally left out: checks are reported by CI with service tokens.

---

## Phase 12 — Video, font and embedded-thumbnail viewers

1. **Video.** `preview.Classify` kind `video`. Playback through the webview for what it decodes (MP4/H.264, WebM); the Go side serves the blob over the Wails asset handler with range requests from a temp file (never a data URL). Side-by-side with synced play/seek, frame step, and container stats (duration, resolution, codec, bitrate) parsed in Go for MP4/MOV and WebM/MKV. Unsupported codecs show stats and "Open externally".
2. **Fonts.** `preview.DecodeFont` (TTF/OTF via `golang.org/x/image/font/sfnt`): family, style, glyph count, coverage ranges, and a rendered glyph sheet + sample line as PNG. Diff: added/removed code points and metric changes.
3. **Embedded thumbnails.** `.blend` (`TEST` block; zstd/gzip-compressed files handled with size caps) and `.uasset` (package thumbnail table, best effort, clear "no embedded thumbnail" state). Rendered through the image viewer's single-image mode with a "thumbnail only" note.
4. All decoders bounded and fuzzed like the rest of `internal/preview`; fixtures generated under `testdata/gen`.

---

## Phase 13 — Simple mode and first-run tour

1. Setting `mode: simple|full` (default full; asked once on first run: "I mostly work on art, audio or design" → simple).
2. Simple mode: tabs reduced to Files, Changes, Locks, History; Update reads "Get latest", Push reads "Submit"; branch switcher read-only unless the project has more than one branch the user touched; revert, tags, merge and sparse editing hidden. Conflicts still appear when they exist. One click in Settings switches back.
3. First-run tour: four dismissible coach marks after the first clone (get latest, lock before editing, review changes, submit), stored as seen in `desktop.json`, replayable from Help.
4. Empty states for every tab explain the next action in plain words.

---

## Phase 14 — Localization

1. i18n layer (`i18next` + `react-i18next`, JSON catalogs under `frontend/src/locales`), language from the OS with an override in Settings; dates, sizes and plurals through `Intl`.
2. Extract every user-facing string; a lint test fails on hard-coded JSX text in `components/`.
3. Go-side strings shown to users (tray menu, notifications) take the same language from settings.
4. Languages: English, Indonesian (`id`), Japanese (`ja`), Simplified Chinese (`zh-CN`), Korean (`ko`). English is the source catalog; a test fails when another catalog misses a key. Check CJK rendering: font fallback stack, no letter-spacing or uppercase transforms on translated text, line breaking in narrow columns and buttons. Server error messages stay English.

Runs after every other planned phase so they are not slowed down; anything landing after it (phase 15 included) must add its strings to all catalogs.

---

## Phase 15 — Review comments on image regions and audio ranges (deferred)

**Status.** Deferred by the owner: do not start it. It needs a server schema and API change on both editions, and the decision is taken after the other phases shipped.

**Why.** MR threads anchor to text lines only, so feedback on art and audio lands in a top-level comment with a description of where to look.

**Server**
1. `merge_request_threads` gains a nullable `anchor` (JSON TEXT / JSONB): `{"kind":"region","x","y","w","h"}` normalized 0..1, or `{"kind":"range","start_ms","end_ms"}`; requires `file_path`, excludes `old_line`/`new_line`. Validation in `merge_request_review.go`: the path must be in the current diff; a thread is outdated once the file's new-side hash differs from the stored `anchor_hash`.
2. REST model, gRPC `anchor` field on the thread and comment messages, client `grpc` conversion, MCP `nipa_mr_comment` passthrough.
3. Web SPA: such threads render under their file as file-level comments with the region/range described in text (no viewer on the web yet).

**Desktop**
1. Image viewer in MR context: drag a rectangle to comment; existing threads appear as numbered pins, clicking opens the `ThreadCard`.
2. Audio viewer: drag a range on the waveform to comment; ranges render as markers, clicking one seeks and plays the range.
3. Outdated anchors are listed but dimmed.

**Tests.** Usecase validation and outdated logic, both repositories, REST and gRPC handlers, e2e spec, viewer interactions.

---

## Decisions taken by the owner

1. Phase 2: the LLM key is stored in the OS keyring; `NIPA_LLM_API_KEY` still wins and the config file never stores a key.
2. Phase 8: shelves are local only. Shared server-side shelves are not planned.
3. Phase 15 (anchored comments on binaries): deferred, decided after the other phases.
4. Phase 14: English, Indonesian, Japanese, Simplified Chinese, Korean.
5. Phase 8: applying a shelf refuses files that have local changes (ours is always the synced snapshot, so the conflict editor reads every side offline and an abort restores the exact previous state).
6. Phase 8: the user picks which changed files go into a shelf (the desktop dialog lists every change, untracked included); the CLI takes tracked and staged changes by default, untracked files when named or with `-u`.
7. Phase 8: "Shelve and continue" is driven by a precise check (daemon `SyncPlan`) of the local changes an update, switch or tag checkout would replace; only those files are shelved.
