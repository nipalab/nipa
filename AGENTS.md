# AGENTS.md

Project context for opencode. Read this before working in this repo.

## What this project is

**Nipa** is a centralized version control system (Go/gRPC) targeting
binary-heavy projects (game dev, 3D assets, media) and large monorepos. It
blends Git (lightweight branches, release tags, merge requests), Perforce
(binary streaming), and SVN (path-based access control). Client-server monorepo: `nipa` (client,
CLI at `cmd/nipa`) plus two server binaries: the free `nipad` (`cmd/nipad`;
SQLite + local chunk storage) and the enterprise `nipad-ee` (`ee/cmd/nipad`;
PostgreSQL + S3-only chunk storage, see `docs/POSTGRES.md`). Both multiplex
gRPC (h2c), the REST API and the embedded SPA on one port through
`internal/serverapp` (`isAPIPath` routes `/auth`, `/docs`, `/api`; the SaaS
marketing landing is mirrored at `/` — Caddy proxies it from Netlify, and the
embedded fallback answers `/` and `/web/*`; everything else falls through to
the web UI handler); the mains only compose repositories and call `serverapp.BuildUsecases`
(`usecase.Repositories` + `UsecaseDeps`), the single place that wires the
usecase graph, also used by the `internal/e2e` harness.
Dual-licensed (Apache 2.0; everything under `ee/` is enterprise).

## Architecture

- **Server** (`internal/`): `domain` (entities/errors), `usecase` (business
  logic, gomock-tested, depends only on repository interfaces; `browser.go` /
  `browser_history.go` power the web UI tree/commit browsing,
  `merge_request.go` the MR lifecycle, `merge_request_review.go` reviews and
  `tag.go` release tags), `repository/sqlite` (free implementation over
  sqlc-generated `internal/repository/sqlc/sqlite`), `serverapp` (shared
  registry + HTTP/gRPC/SPA lifecycle used by both mains), `grpc` (proto + `pb`
  generated + `server` handlers), `http` (REST API).
- **Enterprise** (`ee/`): `db` (postgres opener/migrator + consolidated
  migrations), `repository/postgres` (the same 13 repositories over
  `ee/repository/postgres/sqlc`, queries in `ee/repository/postgres/queries`),
  `storage/s3`, `cmd/nipad`/`cmd/migrate`, `e2e` (testcontainers smoke). All
  postgres and S3 code lives here; details in `docs/POSTGRES.md`.
- **Web UI** (`web/`): Vite/React/TypeScript SPA in `web/`, built with Primer
  React (`@primer/react` + `@primer/primitives` + `@primer/octicons-react`).
  UI code lives in `web/src/` (components use v38 API: `Stack`/`Banner`; the
  old `Box`/`Flex`/`sx` API is gone). Pin `react-is` to the same major as
  React (18) — react-is 19 checks `Symbol.for("react.transitional.element")`
  and silently breaks every Primer visual (`Button` `leadingVisual` etc.) with
  React 18 → black screen. `Banner` REQUIRES a `title` prop (or
  `<Banner.Title>`), children-only usage throws and unmounts the app. Theme is
  dark-only for now: `index.html` sets `data-color-mode="dark"` +
  `data-dark-theme="dark"` and a `html { background-color:#0d1117;
  color-scheme:dark }` inline style (without it the shell is transparent, so the
  bg comes from the browser canvas and text looks dark-on-dark), and
  `main.tsx` passes `colorMode="dark"`; only `dark.css` is imported. Built
  with `make web` into
  `web/server/dist`, embedded into the `nipad` binary via
  `//go:embed all:dist` in `web/server/webui.go` (package `webui`) and served
  for non-API paths in `internal/serverapp` (`isAPIPath`).
- **Desktop app** (`desktop/`, nested module `github.com/nipalab/nipa/desktop`,
  design in `docs/DESKTOP.md`): Wails v3 + React/Vite/Tailwind GUI over the
  `nipa serve` daemon. `internal/connector` attaches to or starts the daemon
  (`NIPA_BIN` → binary next to the app → PATH), `internal/workspace` persists
  opened clones (`<config>/nipa/desktop.json`) and `internal/service` is the
  Wails-bound API, plain Go behind seams (Changes screen: status/stage/
  discard/`FileDiff`/push/update, image compare via `ImageDiff` over the daemon
  `Preview` stream, 3D model compare via `ModelDiff` over `ReadBlob` +
  `preview.DecodeModel` rendered with three.js, audio compare via `AudioDiff`
  (`preview.DecodeAudio` waveforms/spectrograms/LUFS + WAV playback through
  WebAudio), CSV/TSV cell diffs via `TableDiff` (`preview.DiffTables`),
  Unity YAML scene/prefab/asset structural diffs via `SceneDiff`
  (`preview.ParseUnityYAML` + `DiffScenes`, GUIDs resolved from a cached
  per-clone `.meta` index dropped on `.meta` change notices; Godot
  `.tscn`/`.tres`/`project.godot`/`.import` through
  `preview.ParseGodotResource`),
  Unity `.meta` warnings via `MetaWarnings` (`internal/unitymeta`),
  clone with sign-in via `AccountStatus`/`SignIn`/`SignInSSH`/
  `ListProjects`/`RemoteBranches`/`RemoteTree`/`Clone` (dialog: server →
  SSH key or password → project → branch, folder tree, location) and sparse
  editing via `SetSparse` (Details tab),
  History tab via `History`/`CommitFiles` (path-filtered log, image
  thumbnail strip, commit detail with the asset viewers on
  `oldRevision`/`newRevision`, Branch…/Tag…/Revert…), Branches & tags tab via
  `Branches`/`CreateBranch`/`DeleteBranch`/`SwitchBranch`/`Tags`/
  `CreateTag`/`DeleteTag`/`CheckoutTag`, detached-tag and pending-revert
  banners (`Revert` continue/skip/abort), Merge requests tab via
  `MergeRequests`/`MergeRequest`/`MergeRequestActivity`/`MergeRequestCommits`/
  `RevisionFiles` (merge base vs source head in the asset viewers) and the
  `CreateMergeRequest`/`EditMergeRequest`/`SetDraft`/`MergeMergeRequest`/
  `Close`/`Reopen`/`SubmitReview`/`Comment`/`Reply`/`ResolveThread` actions
  (inline line threads through `TextDiff`'s `comments` prop), Conflicts tab
  via `Merge`/`AbortMerge`/`Conflicts`/`ConflictTexts`/`ConflictDiff`/
  `ResolveConflict` (three-way text editor over the marker blocks, take
  mine/theirs with the asset viewers for binaries, merge banner with Finish
  merge/Abort),
  team activity via the daemon `WatchEvents` poll (`Activity`, sidebar and
  Update badges, desktop notifications through the Wails notifications
  service, `internal/tray` menu/tooltip), `nipa://` deep links
  (`OpenLink`/`TakeNavigation`, `app:navigate`, single instance), settings
  in `desktop.json` (`Settings`/`SaveSettings`: keep in tray, start at login
  via `internal/autostart`, notification kinds, poll interval, external
  tools per extension used by `OpenFile`; `SetUploadWorkers` writes the
  shared client config, `RestartDaemon`; `IgnoreRules`/`SaveIgnoreRules`),
  file locks via `LockStatus`/`Lock`/`Unlock` (row lock badges, push box
  blocks on someone else's lock and offers "Lock N files", Locks tab with
  release and "Lock & edit" through `PickFile`/`OpenFile`),
  `repo:changed` + `op:progress` events fed by
  the daemon's `WatchChanges` and `OpEvent` streams), packaging and in-app
  updates (`internal/update`: release version + signed Wails update manifest
  via `tools/updatemanifest`; `internal/companion` embeds the `nipa` CLI in
  Windows/Linux builds, the `.app` carries it on macOS; connector
  `MinVersion` retires an older daemon; `UpdateStatus`/`CheckForUpdate`/
  `InstallUpdate`/`RestartToUpdate`, `update:state`; `wails3 task release`
  and `.github/workflows/desktop-release.yml`). Generated TS bindings in
  `desktop/frontend/bindings` are committed. Root `./...` never includes it;
  use `make desktop`/`desktop-dev`/`desktop-test` (needs the `wails3` CLI;
  Linux falls back to `-tags gtk3` without WebKitGTK 6.0).
- **Client** (`internal/client/`): `cli` (cobra commands: login (password or SSH
  key), clone, branch
  (`-a` list, `-c` create+switch, `-d` delete), tag (list, `-c` create, `-d`
  delete), add, remove, restore, status, push, update, switch (branch or `--tag`),
  merge, revert, log, diff, assets check, acl, group, sparse-checkout, mr, lock, unlock, gc, serve, mcp), `usecase`
  (clone/login, push, update/switch (incl. sparse and detached-tag checkout),
  merge, revert, diff, log, tag management,
  permission, merge-request orchestration, file locks over small interfaces; `threeway.go`
  holds the shared materialize/stage/delete core), `grpc` (gRPC transport,
  converts pb→server domain types; `Client.ServiceClient()` exposes the typed
  pb client for the daemon proxy), `merge` (three-way tree decisions + diff3
  text merge), `securestorage` (keyring-backed token store), `config`
  (`~/.config/nipa/config.json`: `diffExternal`, `uploadWorkers`, `llm`), `difftool`
  (external diff runner), `ignore` (root `.nipaignore` + local `.nipa/ignore`
  pattern matcher for untracked working files), `domain` (client
  config/url/errors/commit/pending state), `localrepo` (`.nipa/` local metadata
  + SQLite),
  `daemon` (the loopback gRPC service behind `nipa serve`, see the flow below),
  `mcp` (the stdio MCP server behind `nipa mcp`), `assetscan` (asset change
  runner), `aiprompt`/`aicontext`/`llm` (generation; flows below).
  `localrepo.FindRepoRoot`
  searches up to 32 parent directories for a `.nipa/config` so every in-repo
  command works from subdirectories.
- **Diff engine** (`internal/diff/`): pure package shared by client and server
  candidates — Myers line diff (`Lines`/`LinesWith` with an equality hook for
  whitespace ignores), tree-state comparison
  (`Entry`/`Change`/`Compare`/`FromTree`/`LoadContent`), rename detection
  (`DetectRenames`), content assembly (`AttachContents`), a server-facing
  tree-vs-tree entry point (`TreeDiff`) and unified/stat/name rendering.
  `internal/client/merge` delegates its line diff to `diff.Lines`. Keep this
  package free of client imports: the server merge-request diff reuses it.
- **Asset previews** (`internal/preview/`): pure package for the desktop GUI's
  visual diffs (design and PR roadmap in `docs/DESKTOP.md`) — `Classify`
  (viewer kind by extension + binary flag), `DecodeImage`/`DecodeImageAt`
  (PNG/JPEG/GIF/BMP/TIFF/WebP via stdlib + `golang.org/x/image`; own decoders
  for TGA, DDS and KTX2 with BC1–BC7/BC6H and mip/face `Select`, EXR, Radiance
  HDR and PSD/PSB composite + layer list; float sources keep `Image.HDR`;
  `DecodeModel` glTF/GLB/OBJ stats with world bounds and referenced files;
  `DecodeAudio` WAV/AIFF/FLAC/Vorbis/MP3 analysis (peaks, spectrogram,
  BS.1770 loudness, playback WAV); `ParseTable`/`DiffTables` CSV/TSV cell
  diffs; `ParseUnityYAML` (Force Text Unity files → `Scene` objects with
  flattened props, GUIDs through a caller-supplied resolver) and
  `DiffScenes` (ID-matched object/prop/dependency changes, `KindScene`);
  `ParseGodotResource` (Godot text resources over `internal/godotres`,
  nodes keyed by path, ext/sub resource references rendered);
  `MaxPixels` checked before allocating, half of it for float), `Downscale`/
  `DownscaleFloat`/`PackHDR` and `CompareImages` (changed pixels/ratio/max
  delta/bbox; transparent pixels compare equal). Block decoders are checked
  against bcdec goldens (`testdata/bcn`, generators in `testdata/gen`).
  Fuzzed; keep it free of client imports like `internal/diff`.
  `internal/assetdiff` (pure, same rule) builds on it: `Compare(ctx, Loader,
  old, new *Ref, Options)` loads both versions through a caller `Loader`
  (`ErrMissing` = added/removed side, `ErrTooLarge` per-kind limits) and
  returns a JSON-friendly `Result` per kind (scene changes capped by
  `MaxChanges`, image stats + optional PNG thumbnails, model/audio stats and
  deltas, table rows capped by `MaxRows`, text line counts, sizes otherwise);
  `Summarize` turns items into sorted one-line `Headline`s capped by
  `MaxFiles`. It also owns `SceneEngine`/`UnityProject`/`BuildGUIDIndex`/
  `ReadGUID`/`WorkingGUIDs` (the desktop keeps its cached index) and
  `MergeGodot` (the daemon's `MergeGodotResource` core over `godotres.Merge3`);
  `unitymeta.ChangesFromStatus` is the status → lint change adapter behind
  `MetaWarnings`.
  `internal/unitymeta` (pure, `fs.FS` based) lints Unity `.meta` sidecars
  for pending changes: missing, orphaned, not staged, GUID changed.
  `internal/godotmeta` is its Godot twin for `.import`/`.uid` sidecars
  (nearest `project.godot`, `.gdignore`/hidden dirs skipped, `UID` parser).
  `internal/godotres` (pure) parses `.tscn`/`.tres`/`project.godot`/
  `.import` into header, ordered sections (attrs + raw multi-line props) and
  `Section.Identity()`, and `Write` round-trips the input byte for byte
  (fuzzed). `Merge3(base, ours, theirs)` merges sections by identity and
  properties/attributes by key (ours' order, theirs' additions after their
  predecessor), renumbers colliding new ext/sub resource ids with their
  `ExtResource()`/`SubResource()` references, shifts `load_steps`, and flags
  conflicts (same key changed differently, delete vs modify, references,
  parents or order the merge broke) instead of writing markers; fuzzed for
  `Merge3(x,x,x)==x` and `Merge3(b,o,b)==o`. The daemon's
  `MergeGodotResource` exposes it.
- **Unity plugin** (`plugins/unity/com.nipalab.nipa`, Apache 2.0, design in
  `docs/UNITY_PLUGIN.md`, plan in `docs/plans/UNITY_PLUGIN.md`): UPM package
  over the daemon JSON bridge. `Editor/Core` is engine-free C# (`DaemonClient`
  JSON/NDJSON over `HttpClient`, `Connector` attach-or-spawn, `StatusCache`,
  `ProjectPaths`, `NipaSession`, `LockPolicy` for covering-lock checks,
  strict mode and the push pre-check, `MergeTool` for UnityYAMLMerge conflict
  merges, `SetupChecker` (Force Text, visible meta, `.nipaignore` template;
  Setup tab), `make unity-pack` tarball + `.github/workflows/unity-plugin.yml`, branch/tag/history/conflict calls on `NipaSession`; locks surface through
  `AssetModificationProcessor`, overlays, the Inspector header and a Locks
  tab, fed by `LockStatus`/`ProxyLockFile`/`ProxyUnlockFile`/`WatchEvents`) compiled as `netstandard2.1` by
  `plugins/unity/dotnet/Core` and tested there (`make unity-test`, NUnit +
  coverlet, fake daemon over `HttpListener`); `Editor/` holds the thin Unity
  glue (`[VersionControl("Nipa")]` provider with overlays/menus/settings,
  `NipaHost`, `NipaWindow`). Keep Core free of `UnityEngine`/`UnityEditor`
  and C# 9 compatible.
- **Godot plugin** (`plugins/godot`, Apache 2.0, design in `docs/GODOT_PLUGIN.md`,
  plan in `docs/plans/GODOT_PLUGIN.md`): Godot 4.4+ add-on `addons/nipa` over
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
Flow for `nipa clone <url> <target>`: `cli/clone.go` parses the URL →
`usecase.Repo.Clone` → ensure empty target → login → resolve default branch (or
`GetBranchByName` when a branch is given) → fetch tree manifest (gRPC
`GetTreeManifest`, recursive, with the URL path) → `localRepo.Init(target)` →
`SaveConfig` (`.nipa/config` json: url + branch + sparse prefixes) →
`syncWorkingCopy` (shared with update/switch: download missing chunks through
the commit-scoped `ChunkScope`, materialize files, record `stat_cache`
fingerprints; raw files no pin covers whose manifest chunk sizes add up to
the file size are written directly (`direct.go`): a truncated
`.nipa-materialize-*` temp file per file, chunks already in `.nipa/objects`
copied locally, the rest written by `downloadChunks`' hash-verifying workers
at every offset they occupy (open/close per write), a file is chmodded
(same read-only rule as `materializeFile`) when its last chunk landed, and
the working files are `replaceFile`d (bounded pool) only after the whole
download and `materializeAll` succeeded; any failure removes the unreplaced
temp files, removals still run last, and everything else (zstd text, pinned raw, unknown chunk sizes)
goes through the object cache and `materializeAll`) → `SaveTree` → `SaveCommit` (branch head commit id). Clone,
update and switch record the branch head commit id locally so `HEAD`/`@` (and
branch creation via `nipa branch -c`, which forks from the pinned commit)
resolve offline.

Flow for `nipa sparse-checkout <list|set|add|remove|disable>` (and `nipa clone
--sparse a,b`): `Update.SetSparse` normalizes the prefixes
(`domain.NormalizePathPrefix`, dedup, empty = full checkout) into
`.nipa/config`'s `sparse` field, then runs a regular update. The manifest
request and the chunk download scope carry the prefixes, so only the sparse
set is materialized and saved; files outside the set are removed from disk and
swept from the local DB but stay tracked on the server. Push from a sparse (or
permission-filtered) clone sends the pinned head commit as
`base_commit_id` (preferred over `base_tree_hash`) so the server applies the
delta to the full base tree. Merge and revert still require a full checkout;
`Push.Run` refuses to run mid multi-target revert sequence.

Flow for ignore rules (`.nipaignore` at the repo root, versioned; `.nipa/ignore`
per clone, never tracked): `internal/client/ignore.New` compiles both files
(local rules last, so they win) with a pragmatic gitignore subset (`#` comments,
`!` negation, `*`/`?`, `**`, leading/mid `/` root anchoring, trailing `/` =
directory-only; last match wins). `WorkingCopy.Status` and `Diff.scanWorking`
walk the working tree through `walkWorkingFiles`, which drops ignored paths
unless they are tracked or staged (tracking always wins over ignores) and prunes
ignored directories that contain no protected paths. `nipa add` errors on an
explicitly ignored target with a `use -f` hint (`AddOptions{Force}` / `nipa add
-f` stages it anyway); directory and whole-repo adds skip ignored files
silently. Add stages only tracked paths that are new, modified or missing:
files whose fresh `stat_cache` fingerprint matches the snapshot hash are
skipped (no content is read), and the remaining paths are staged in one
transaction (`StageAddMany`, client DB runs WAL + `synchronous=NORMAL`), so
`nipa add *` on a large clone is cheap. The matcher is client-side only: a
hostile client can still push such paths, and a tracked file that later matches
a rule stays tracked until explicitly removed. Sparse manifests always include the root `.nipaignore`
(`loadTreeManifest`'s `keepRootTeamFile`, still behind the read filter), so
partial checkouts apply the team rules too. The pattern compiler itself lives
in the pure `internal/pathmatch` (`Parse`/`Compile`/`Join`/`Matches`), shared
with asset budgets.

Flow for `nipa mcp` (reference in `docs/AI.md`): `internal/client/mcp` is a
go-sdk MCP server over stdio (`Serve`/`NewServer`; `cmd/nipa/mcp.go` wires a
`nonInteractiveInput` prompter, since a login prompt would corrupt stdout).
`Options.NewUseCases` (`newMCPUseCases`) builds a fresh `UseCases` graph per
call (one gRPC client binds one host) whose `Close` releases the client and
localrepo handles; the working copy is the `repo` argument, then `--repo`,
then the cwd (`FindRepoRootFrom`). Read tools (`tools_read.go`,
`tools_mr.go`, `tools_asset.go`) are always registered; write tools only per
capability in `Options.Allow` (`ParseAllow`, unknown names fail; `--allow-write`
= `AllCaps`): `stage` add/restore, `push`, `sync` update/switch, `branch`
branch/tag create, `lock` lock/unlock, `mr` create/update/comment/reply/
resolve/review/request-review/check, `merge` merge/resolve-conflict/
`nipa_godot_merge`/`nipa_mr_merge`; an unregistered tool is a JSON-RPC
error. Writes are serialized by `writeMu` and annotated `DestructiveHint`
when they discard or overwrite work. `withAgentTrailer` appends
`Nipa-Agent: <clientInfo.name>/<version>` from the initialize handshake to
push messages, MR descriptions, comments, replies and review bodies (not
titles; skipped for blank text, an existing trailer or `--no-agent-trailer`).
Budgets (`budget.go`): `capDiff`/`capMRDiff` trim `nipa_diff`/`nipa_mr_diff`
to `max_bytes` (default 256 KiB, max 2 MiB; `truncated` per file,
`omitted_files`), `nipa_log` 50/500, `nipa_mr_list` ≤200, summaries 50/200
files, asset tables 200/2000 rows, scene changes 500/5000. Asset tools read
both sides through `assetscan.BlobLoader` over `usecase.Diff.ReadBlob`
(working/snapshot offline, revisions through the commit-scoped download) with
`assetdiff.WorkingGUIDs`; `nipa_meta_warnings` lints status through
`unitymeta`/`godotmeta`; `nipa_godot_merge` runs `assetdiff.MergeGodot` on the
pending merge's base/ours/theirs and resolves only a clean result. Prompts
(`prompts.go`) are always registered and call no LLM. `toolError` appends the
domain `hint:`/`next:` to failures. Unit tests stub the usecases
(`mcp_test.go`, `phase1_test.go`, `tools_asset_test.go`, `prompts_test.go`);
e2e in `tests/suite/mcp_test.go` + `mcp_workflow_test.go`.

Flow for asset budgets (`.nipaassets.yml` at the repo root, versioned and kept
in sparse manifests like `.nipaignore`; reference in `docs/AI.md`): the pure
`internal/assetbudget` parses it (`go.yaml.in/yaml/v3` with `KnownFields`,
`version` 1, rules of `paths` globs in `pathmatch` syntax plus
`max_bytes` (int or human size), `max_texture_size`, `power_of_two`,
`max_triangles`, `max_vertices`, `max_audio_seconds`, `max_peak_dbfs`,
`name_pattern`), merges every matching rule in file order (a later rule
overrides only the keys it sets; violations name the rule that set the limit)
and `Check`s `assetdiff.Result`s on the new side only (removed files skipped,
undecoded files only checked for size/name; sorted by path, limit).
`internal/client/assetscan` is the shared client runner: `Enumerate` (the
`nipa diff`-style change list as `assetdiff.Item`s, also used by
`nipa_describe_changes`), `BlobLoader`/`SideRef`/`ChangeItem`, and
`Check`/`CheckChanges`, which decode only covered images/models/audio with
matching limits (`Config.NeedsDecode`). Surfaces: `nipa assets check [<rev>
[<rev>]] [--staged] [--json] [-- <path>...]` (`cli/assets.go`, exit 2 via
`cli.ErrBudgetViolations`), the MCP read tool `nipa_asset_check`, and
`nipa push --dry-run` (`asset_violations` in JSON, warning block in text;
offline over the plan's staged changes). The budget always comes from the
working copy; push itself does not enforce it.

Flow for generation (reference in `docs/AI.md`; the server never calls an
LLM): `internal/client/aiprompt` (pure, deterministic) builds
`Prompt{System, User}` for `CommitMessage`, `MRDescription` and `ReviewMR`
from a `Changeset` (`FileText` patch lines via `FromFileDiffs`/`FromMRDiff`,
`assetdiff` summaries, `assetbudget` violations, diff text capped at
`DefaultDiffBytes` 64 KiB with `[... N more files omitted]` markers; lines with
NUL/invalid UTF-8 become `[non-text content omitted]`, binary files only show
their path), plus `CleanOutput`/`SplitTitle`/`AddTrailer`
(`Nipa-Generated: <model>`). `internal/client/aicontext.Collector` gathers the
input over small interfaces (`Differ`, `MRReader`, `History`, `Connector`):
`Commit` (staged diff, offline), `MRDescription` (merge-base diff + source
commits since the merge base, default target = the branch with `IsDefault`)
and `Review` (MR view/diff/commits/threads, sides from `MRSides`, shared with
the MCP asset tools). The MCP prompts `nipa_commit_message`,
`nipa_mr_description` and `nipa_review_mr` (`mcp/prompts.go`, always
registered) return one user message (system text, `---`, user text).
`nipa push --generate` and `nipa mr create --generate` (`cli/generate.go`)
call `internal/client/llm` — one non-streaming OpenAI-compatible
`POST <endpoint>/chat/completions` (endpoint used as-is, trailing `/` trimmed,
60 s timeout, 1 MiB response cap, key redacted from errors) — configured by
`config.ResolveLLM` (`llm: {endpoint, model, apiKeyEnv}` in the client config,
`NIPA_LLM_ENDPOINT`/`NIPA_LLM_MODEL`/`NIPA_LLM_API_KEY` win, keys never stored);
the draft is confirmed with `y/N` (`--yes` skips, non-TTY refuses) and
`push --dry-run --generate` only prints it (`generated_message` in JSON).

Flow for release tags (`nipa tag`, `nipa tag -c <name> [-m msg] [--branch <b> |
--commit <id|hash>]`, `nipa tag -d <name>`, `nipa switch --tag <name>`): a tag
is an immutable project-scoped name pointing at one commit, with an optional
annotation message (`tags` table, hard `UNIQUE(project_id, key)`; deleting frees
the name). Server `usecase.Tag` resolves the target
`commit_id > commit_hash > branch head > default branch head`, read-gates
list/get, write-gates create/delete (409 on a live duplicate) and emits
`tag.created` / `tag.deleted` webhooks. `nipa tag` lists (keyset cursor followed
across pages); `-c` defaults to the locally pinned HEAD commit (offline-safe)
and `--commit` accepts a base36 id or hex hash. `nipa switch --tag` is a
Perforce-style detached checkout: resolve the tag, fetch the tagged commit's
manifest with `GetCommitTreeManifest` (`GetTreeManifest` + `commit_id`, sparse
and read-filtered, project-scoped), sync the working copy, pin the tag commit
and record `{"head":{"kind":"tag","name":...}}` in `.nipa/config` — the branch
identity stays configured. While detached, `nipa update` re-syncs the pinned
commit (so sparse-checkout edits keep the checkout), `nipa status` prints
`HEAD detached at tag "x"`, `nipa log` walks from the pinned commit, `nipa diff`
accepts tag names as revisions, and push/merge/revert refuse with a
`nipa switch <branch>` hint; switching to any branch clears the marker (and
`branch -c` forks from the pinned commit and re-attaches). The `/tags` REST
routes (`internal/http/api/tag.go`, handlers/DTOs in
`internal/http/{handler,model}/tag.go`) mirror list/create/get/delete, and the
web API client (`web/src/api/endpoints.ts`) already exposes
`listTags`/`createTag`/`deleteTag`/`getTag`; the SPA Tags page is `/:org/:project/tags`.

Flow for project service tokens and commit ancestry (payloads and scopes in
`docs/WEBHOOKS.md`): `project_service_tokens` rows (name, SHA-256 `token_hash`,
`hint`, comma-joined `scopes` `read`/`checks:write`/`webhooks:write`,
`created_by`, optional `expires_at`, `last_used_at`, `revoked_at`) are managed
by `usecase.ServiceToken` (project admin, audited as
`service_token.created`/`revoked`) over `GET/POST
/orgs/{org}/projects/{project}/service-tokens` and `DELETE .../{id}`; the
plaintext `nipa_st_...` is returned once. The REST `AuthFilter` sends
`Bearer nipa_st_...` to `ServiceToken.Authenticate` only for the
`serviceTokenRoutes` allowlist (method + selected route path → scope; every
other route is 403), which checks the token is active, the `{org}/{project}`
path names its project and the scope matches, the creator is active, verified
and still a project admin (else 403), touches `last_used_at` at most once a
minute and returns the creator's claims tagged with `Claims.ServiceTokenID`, so
the usual usecase permission checks still apply; the tag puts the token id in
audit entries (`ActorLabel` `service_token:<id>`), gives the token its own
`api_user` rate-limit bucket (`token:<id>`) and exempts it from the "author
cannot report required checks" rule. Creation counts the 50 active tokens
inside the insert transaction behind `LockProject` (postgres advisory lock,
sqlite immediate tx). `GET .../commits/{commit}/contains?ref=`
(`Branch.Contains`, `WithTags` for tag refs) resolves `ref` as tag, branch,
base36 commit id or hash and walks the ref's parents (no id-order pruning,
capped at 10000 commits, then 400) to answer `{contains}`; read permission
required. Service tokens are REST only.

Flow for SSH key login (design in `docs/SSH.md`: `nipa login`, the implicit
login behind every command, the profile page card): an SSH key is an alternate
credential that bootstraps the normal JWT session over the same gRPC transport;
the private key never leaves the client. Public keys live in `ssh_public_keys`
(name, type, blob, globally unique `SHA256:` fingerprint, comment; max 10 per
user) and are managed over REST only (`GET/POST /api/v1/keys`,
`DELETE /api/v1/keys/{id}`, rendered by the SPA profile "SSH keys" card);
`internal/sshkey` parses authorized_keys lines (ed25519/ecdsa/RSA ≥ 2048, no
DSA or certificates) and verifies signatures. Login is a two-RPC
challenge–response: `BeginLoginSSHKey(email?)` stores a 32-byte nonce (60 s TTL,
single-use, expired rows swept on insert) in `ssh_login_challenges`, the client
signs `nipa-login:v2:<origin>:<nonce>` (origin = `sshkey.CanonicalOrigin`: lowercase host
without trailing dot, port only when not 80/443, no scheme or path) and
`CompleteSSHKeyLogin` consumes the challenge,
resolves the account from the key fingerprint (key-only, git-style) or checks
the optional email against the key owner, verifies and returns the usual
access/refresh pair; every failure is the generic 400
`invalid email or SSH key` and neither call reveals whether an account exists.
The server's expected origin comes from config, never from the request:
`PUBLIC_BASE_URL`, else the host of `EMAIL_BASE_URL` (`Config.SSHLoginAudience`,
`Auth.WithSSHAudience`); with neither, both RPCs answer 400 `SSH key login is not
enabled` and startup logs a warning. `BeginLoginSSHKey` returns it as `audience`
and the client refuses to sign unless it equals the canonical origin of the
address it dialed, so a hostile server can neither obtain nor relay a usable
signature; v1 (`nipa-login:v1:`) is gone and RSA must sign `rsa-sha2-256/512`
(`ssh-rsa` SHA-1 is rejected). Both RPCs are in the interceptor's public list. Client-side
`internal/client/sshkey.Resolve` resolves signers in order: `--ssh-key`,
`NIPA_SSH_KEY`, config `sshKeyPath`, ssh-agent keys, then
`~/.ssh/id_ed25519|id_ecdsa|id_rsa` (configured keys fail loudly with a
passphrase prompt, auto-detected keys are skipped); `nipa login` tries SSH
before any prompt and falls back to the password flow (flag/env failures are
hard errors), `MakeSureLoggedIn` does the same for every command, and
`Session` re-authenticates with the key — email-bound when the stored session
has one, key-only otherwise. Covered by unit tests plus the
`tests/suite/ssh_test.go` e2e specs (key-only login, lazy clone login through
`NIPA_SSH_KEY`, unregistered-key rejection, username binding).

Flow for email notifications (design in `docs/EMAIL.md`): `internal/mail` is
the sender layer (`EMAIL_SENDER` = `smtp|sendgrid|scaleway|http|log|off`), one message
per recipient is persisted in the `email_deliveries` outbox and
`mail.Dispatcher` claims due rows, retries with exponential backoff
(`EMAIL_MAX_ATTEMPTS`/`EMAIL_RETRY_BACKOFF_SECONDS`/`EMAIL_POLL_SECONDS`) and
sweeps expired rows; both mains build the sender + dispatcher from
`internal/config` (an `off` sender leaves the notification seam unwired).
`usecase.EmailNotifier` is attached through `HookEmitter.WithNotifier`: on the
MR events (created/ready/review-requested/review-submitted/comment/
synchronized/merged/closed/reopened and failed `mr.check_reported`, carried by
`EmitMergeRequestCheck`) it resolves the participants (author, requested
reviewers, assignees, commenters), skips the actor, deleted users, invalid
addresses and `users.notify_email = false`, and enqueues one rendered message
per recipient (deep link from `EMAIL_BASE_URL`; the first message to a
recipient uses a per-recipient thread `Message-ID`, later messages reference
it with `In-Reply-To`/`References` via the `thread_key` column). The
dispatcher is kicked after each enqueue. Project admins inspect and redeliver
through `usecase.EmailDelivery`: `GET .../emails/deliveries`
(`state`/`after`/`limit` keyset, `next_cursor`) and
`POST .../emails/deliveries/{id}/redeliver` (delivered/failed only), surfaced
by the SPA project settings "Email deliveries" tab. The harness runs `nipad`
with `EMAIL_SENDER` set to `sendgrid`, `scaleway` or `http` (via
`NIPA_TEST_EMAIL_TRANSPORT`, one transport per edition in CI) pointed at an
in-suite receiver (and 1s retry/poll knobs) and covers opt-out, participant
emails, thread replies, synchronized/check/merge events, failed retries and
redelivery.

Flow for organization creation (`POST /api/v1/orgs`, "New organization" dialog
on the SPA home page and the `/onboarding` step): any authenticated user creates
an org; `usecase.Org.Create`
trims the name, derives the slug from it when omitted, rejects invalid or
duplicate slugs (409) and calls `OrgRepository.CreateWithOwner`, which in one
transaction inserts the row with `created_by_user_id` and upserts the creator as
an `owner` member — so project creation (`Project.Create` requires org owner)
works immediately, and `usecase.Permission` grants org owners `PermissionAll`
on every project in the org (`load` resolves the project's org through
`pbacRepository.ProjectOrgID` + `orgAuthorizer.IsOrgOwner`), so the creator can
read/write/admin the project without seeding rules; plain org members still
need PBAC rules. The free version tracks no storage usage; cloud builds attach
quota to `created_by_user_id` through the `usecase.StorageLedger` seam. The SPA
forms (`OnboardingPage`, `CreateOrgDialog`, `NewProjectPage`) share
`web/src/slug.ts` for `slugify`/`SLUG_PATTERN`, require a valid non-empty slug,
auto-derive it until the user edits it, and surface a taken slug (409,
`ApiError.status`) at the slug field.

Flow for SaaS onboarding (see docs/SAAS_ONBOARDING.md; `nipad-ee` only, gated by
`SAAS_MODE=1`, free edition answers 404): `ee/registration` serves
`POST /api/v1/auth/register` `{name,email}` (creates an inactive, password-less
`email_verified=false` account via `RegistrationRepository.CreateUnverifiedUser`, seeds the free-tier
`owner_quota` from `SAAS_FREE_QUOTA_BYTES`, stores a 72 h one-time token in
`email_verifications` and queues the confirmation email in the existing outbox
using `EMAIL_BASE_URL`), `POST /api/v1/auth/register/resend` (always 200) and
`POST /api/v1/auth/verify` `{token,password}` (the mailbox owner chooses the
password here, so a squatter's registration can never set one; activates the
account, consumes the token and returns the standard login result via the exported `usecase.Auth.LoginResult`).
Login gates live in `internal/usecase/auth.go`: password, refresh-token and SSH
logins refuse unverified accounts with a 403 `email not confirmed`. `ee/oauth`
serves the social flow: signed-HMAC state (JWT key, relative redirects only)
through `GET /api/v1/auth/oauth/{provider}/start`, Google userinfo / GitHub
`/user` + verified-email exchange in the callback, account resolution (linked
identity → verified-email match — an unconfirmed account is activated — →
`oauth_error=email_conflict|email_required` → new verified account with free
quota), a one-time `oauth_login_codes` handoff (token never in a URL) and
`POST /api/v1/auth/oauth/complete`; authenticated `GET/DELETE
/api/v1/me/identities` plus `GET /api/v1/me/identities/{provider}/start`
(returns `{url}`) manage links, refusing to unlink the last login method of a
passwordless account (409). Both OAuth flows bind the callback to the starting browser: `Start` sets an
HttpOnly `nipa_oauth_binding` cookie (Lax, path `/api/v1/auth/oauth`) whose hash is
in the signed state, and `Callback` refuses a state without the matching cookie.
Linking an existing unconfirmed account clears its password, pending tokens and
sessions first. Registering an address that already has an account always looks
like a success (an unconfirmed one is just re-issued a token), and password
login spends a hash comparison on unknown emails so timing does not reveal
registrations. Both flows end on the SPA `/onboarding` step
(workspace name + editable URL slug → existing `POST /orgs`); the public SPA pages `/register`,
`/verify` and `/oauth/callback` plus the profile "Connected accounts" card do
the client side, and `GET /api/v1/auth/capabilities` (registered by
`ee/registration`, providers list supplied by `ee/oauth`) lets the SPA hide the
entry points on servers without SaaS mode. Registration/social write through
`postgres.NewRegistrationRepository` (dbtx-aware, joins the caller's
transaction); the public routes attach to the unfiltered `/api/v1/auth`
WebService through `api.WithPublicRoutes`/`serverapp.WithPublicRoutes`, and
`api.Wrap` is the exported AppContext route adapter. Monthly/yearly billing
lives in `ee/billing` inside `nipad-ee` (design in `docs/BILLING.md`; see
the billing flow below).

Flow for binary file locks (`nipa lock <path> [--branch]`, `nipa unlock <path>`,
`nipa lock list`): binary changes are mandatory-lock gated. `FileLock.Acquire`
resolves the scope from the branch — the default branch is a project-global lock
(`file_locks.branch_id NULL`), any other branch a lock of that branch — and
rejects a path already covered (exact or directory prefix, `domain.PrefixCovers`)
by another holder; the same holder is idempotent. `Push.Push` classifies the
touched binaries against the head tree (`headBinaryPaths`): tracked binaries
being modified or removed must be covered by the pusher's lock (409 `binary file
%q requires a lock`), while new binary files only conflict with someone else's
covering lock (409 `%q is locked by <holder>`) — adding a brand-new asset needs
no lock unless a directory or pre-emptive lock guards it. Acquiring needs write on the path itself, and on `nipad-ee` grants run in a
transaction holding a per-project advisory lock (`FileLock.WithSerializer`,
covering both `Acquire` and the merge-request lock path; `dbtx.WithinTx` joins
a caller's transaction) so overlapping server processes cannot grant
overlapping locks. Binary
classification is forced server-side for known asset extensions
(`markLockableBinaries`, the client flag can only add). `EnsureLocks` enforces
both lists in the target scope; after `ApplyPush`, `ReleaseLanded` drops the
pusher's own exact-path, non-request locks while directory locks span the
editing pass and stay. Client side, a lock this client takes is also pinned
locally (`usecase.LockPins`, used by `FileLock.Lock` and the daemon's
`ProxyLockFile` through `RepoOps.Pins`, after the server granted the lock):
the path goes into `pinned_paths` and the raw snapshot files it covers are
captured with `ensureSnapshotContent`, so offline compare and restore keep the
old version while the file is edited; a pin or capture failure is a
`FileLock.Warning` (`nipa lock` prints `warning: ...`, the daemon fills
`ProxyLockFileResponse.warning`) and never undoes the lock. Unlock unpins and
prunes (a local unpin failure is only logged: the server lock is already
gone); a successful push drops the exact-path pins among the pushed and
removed paths (`landedPins`, no DB write without one; directory pins stay,
like `ReleaseLanded`). Pins are a best-effort cache hint keyed by path only,
not by lock scope: the same path locked on two branches (or held by a merge
request) shares one pin, and a push on either branch drops it while the other
lock is still held. Locks taken elsewhere are not pinned either; in both cases
reads use the missing-content fallback (download when online). Merge requests acquire locks for their changed binary
paths at creation (re-checked and topped up at merge — `branchMerger.
BinaryChangesBetween` is the permission-filter-free three-dot enumeration —
accepting locks held by the request owner so an admin can merge on their
behalf), release them on merge/close, and re-acquire on reopen. Creation writes
the request row and its locks in one transaction through
`dbtx.Transactor.WithinTx` (repositories built with `dbtx.New` resolve the
transaction from the context), so a lock failure rolls the row back. Plain
`FastForward` enforces/releases
via `BinaryLockPlan` (required vs checked paths); `Branch.Delete` releases the
branch's scoped locks (soft delete ⇒ no FK cascade). The `/locks` REST routes,
`LockFile`/`UnlockFile`/`ListFileLocks` RPCs and the web Locks page expose
acquire/release/list.

Flow for merge request reviews: a review is one decision per reviewer per source
head round (`approved`/`changes_requested`/`commented`); a review for an older
head is stale and stops counting, and the latest per reviewer+head wins.
Submitting a review can carry inline comments that become anchored threads
(`file_path` empty = top-level conversation; otherwise `old_line`/`new_line`
must be a line the current diff shows). `POST .../reviews` on your own request
is rejected; a project writer can `POST .../reviews/{id}/dismiss` (kept in
history, `dismissed_reason=new_commits|manual`) while a reviewer can `DELETE`
their own; dismissing leaves comment threads intact (`review_id` FK is
`ON DELETE SET NULL`). A thread anchored to a line the new head no longer shows
is outdated (top-level threads never are). Review requests are answered by that
reviewer's next review and re-requesting keeps the original requester. Every
push to the source branch dismisses decisions for the old head and appends a
`pushed` timeline event: `Push` calls `usecase.Push.WithReviews`, which runs
after the apply succeeded and only logs bookkeeping failures. The timeline
events carry a `subject` actor (e.g. the requested reviewer). REST routes live
in `internal/http/api/merge_request_review.go`, the matching RPCs in
`internal/grpc/server/merge_request_review.go`; MR list/detail responses carry
the live `review` summary and the SPA renders a tabbed MR page (Conversation /
Commits / File changes, `?tab=`). The Commits tab is served by
`MergeRequest.Commits`, a first-parent `CommitLogUntil` walk (stop = live merge
base, authors joined) exposed at `GET .../merge-requests/{id}/commits`. The
diff is side-by-side by default with a Unified/Split toggle and floating inline
thread cards
(`web/src/components/repo/DiffView.tsx` + `ThreadCard.tsx`, reused by the
commit page).

Merge is review-gated: `MergeRequest.WithReview` attaches the review usecase
(the nil seam disables the gate, like file locks/hooks), and `check()` fills
`Mergeability.BlockedBy` — live `changes_requested` always blocks, and a target
branch's `required_approvals` (new `branches.required_approvals` column, set
through `SetBranchProtection` / `PUT .../branches/{name}/protection` with an
optional `required_approvals` that is kept when absent) must be met by live,
non-stale approvals. `Merge` refuses blocked requests with a 409. The same
review seam writes the lifecycle timeline events (`opened` inside the Create
transaction; `merged` with the landed commit id+hash, `closed`, `reopened`
log-only after the change landed), so the SPA's timeline renders them. The MR
usecase's `AppendEvent` path requires the review repository to be
transaction-aware (`dbtx.New`), which is why
`NewMergeRequestReviewRepository` wraps its DB handle. gRPC carries the same
surface: `GetMergeRequest` (detail + mergeability + live review summary),
`ReopenMergeRequest`, `CheckMergeRequest`,
`ListMergeRequestCommits` (reuses `CommitLogEntry`),
`GetMergeRequestDiff` (new `DiffFileDetail`/`DiffHunkDetail`/`DiffLineDetail`
messages mirroring the REST diff model), `MergeabilityDetail.blocked_by`,
`MergeRequestDetail.review`/`MergeRequestDetail.draft` (populated by
`ListMergeRequests` via `AttachSummaries`), and
`Branch.required_approvals`/`Branch.dismiss_stale_approvals`.

Draft requests: `merge_requests.is_draft` blocks `Merge` through
`check()`'s `blocked_by: "draft"`; `SetDraft` (author or project admin, open
requests only) toggles it, marking ready emits the `ready_for_review` timeline
event plus the `mr.ready_for_review` webhook, and the list takes a `draft`
filter (the CLI's `--status draft` maps to open+draft). A WIP/Draft title
prefix (`wip:`, `draft:`, `[wip]`, ...) auto-drafts on create and on rename,
and removing the prefix from a title-drafted request marks it ready. The SPA
marks drafts in the list, the page header, the edit dialog and the merge box;
the CLI adds `mr create --draft [--reviewer <id>] [--assignee <id>]`, `mr update --draft=false` and
`mr ready <number>`.
`SetBranchProtection` carries `dismiss_stale_approvals` (default on; off
carries live decisions onto the new head through `CarryOverReviews` instead of
dismissing them), `require_status_checks` plus required check names, and named
`required_reviewers` whose live approval the review gate demands
(`blocked_by: "required_reviewers"`). `Branch.Delete` refuses protected
branches (409) until they are unprotected.

Status checks: `merge_request_checks` rows are keyed by (request, head commit,
name) and written by `MergeRequestCheck.Report` (`POST .../checks`, gRPC
`ReportMergeRequestCheck`, `nipa mr check [--commit <id>]`). A report must name
the source head it was started for (`head_commit_id`; the CLI defaults to the
commit the working copy is pinned to, never the server's current head); a moved head is a 409, and the request author cannot report
when the target requires status checks; a target branch with
`require_status_checks` blocks merging (`blocked_by: "status_checks"`) until
every required name succeeded for the current source head — a push starts a
clean slate. `List`/`nipa mr checks` return the head's checks; reporting emits
`mr.check_reported`.

Create takes `usecase.CreateMergeRequestInput`; optional `ReviewerIDs`/
`AssigneeIDs` (REST `reviewer_ids`/`assignee_ids`, gRPC fields 7/8, SPA/desktop
create forms, MCP `reviewers`/`assignees`) need project write, are deduped and
validated like `SetAssignees` (self review rejected, nil review seam = 400) and
are written in the create transaction after the `opened` event (review
requests + `review_requested` events, no separate webhook), so only
`mr.created` fires and they get the "opened" email.

Assignees: `merge_request_assignees` (base-set through
`SetAssignees` / `POST .../assignees` / gRPC `SetMergeRequestAssignees` /
`nipa mr assign`, project-write gated, ids validated) attach to list and get
payloads and drive the list's `assignee` filter; the SPA has a sidebar picker
and a list filter. The list also takes a free-text `search` (title/description
LIKE; CLI `--search`).

Merge strategies: `Merge` takes a `strategy` (`ff` default, `merge`, `squash`,
`rebase`) and `delete_source`. `Branch.MergeForMergeRequest`
(`internal/usecase/merge_strategies.go`) reuses the hoisted `internal/merge`
three-way engine (the former `internal/client/merge`): it loads the full
unfiltered trees (`mergeTree`), materializes taken files and diff3 text merges
(new content stored through the `Chunk` usecase via `WithChunkUploader`), and
writes the resulting tree and commit through `PushRepository.ApplyPushAll` —
one transaction for the whole rebase chain (`WithMergeCommitter`). Conflicts
return a 409 naming the paths and nothing lands; `behind_target` only blocks
`ff` and the review gate still applies to it (an objection or missing approval
blocks a diverged source too), while an ancestor source is `up_to_date`; rebase
walks the source first-parent history oldest-first, stopping at any commit the
base already contains, so a source that merged the target does not replay the
shared history (`rebaseCommitLimit`). Squash uses the request title and creator
as author;
rebase keeps each original commit's author and message. The target branch stays
protection-aware (admin required) and merged-source deletion is best-effort
(warn on failure). The SPA merge box picks the strategy plus a delete-source
checkbox; the CLI adds `mr merge --strategy <ff|merge|squash|rebase>
[--delete-source]`.

MR lists paginate by number: `GET .../merge-requests` takes
`status/author/source/target/draft/assignee/search/after/limit` and returns
`{merge_requests, next_cursor}` (the cursor is the last number of a full page;
gRPC uses `ListMergeRequestsRequest.after_number`/`ListMergeRequestsResponse.
next_cursor`). The handlers over-fetch one row, so `next_cursor` only appears
when a next page really exists; changing filters resets the cursor. Review
activity is webhook-visible through the same MR-shaped payload as the lifecycle
events: `mr.review_submitted`, `mr.review_dismissed`, `mr.review_requested`,
`mr.review_request_removed`, `mr.comment_created` (new comments only) and
`mr.check_reported`. The
CLI mirrors the surface under `nipa mr`: `create --draft`, `view`, `reopen`,
`ready`, `assign <n> <user-id>...`, `review --approve|--request-changes -m`,
`comments`, `comment [-m]
[--file --new-line/--old-line]`, `reply <number> <thread-id>`, `resolve
<number> <thread-id> [--unresolve]`, `timeline`, `requests`,
`request-review <number> <user-id>`, `unrequest-review`, `diff`, `checks <n>`,
`check <n> --name --state [--url]`,
`merge --strategy/--delete-source`, and `list
--author/--source/--target/--assignee/--search/--after/--status draft`.

Flow for `nipa revert <commit>` / `<from>..<to>`: resolve targets via gRPC
`GetCommit` / `WalkCommits` (range walks newest-first, exclusive stop; ranges are
capped at 16 targets and rejected before any three-way work) → for each
target three-way merge `(base=target tree, ours=current head, theirs=mainline
parent tree; root commit ⇒ empty parent; merge commits need --mainline 1|2)` via
`usecase/threeway.go` → stage and commit one revert per target through
`Push.pushStaged` (base = previous head/tree hash) → on conflict persist
localrepo `revert_state` (remaining targets, current tree hash + commit id,
conflicts) and return; `--continue` commits the resolved step and resumes,
`--skip` drops it, `--abort` restores the original tree/pin, `--no-commit`
applies to the working copy without committing (single commit, `-m` sets the
message). Reverts are forward-only; history is never rewritten.

Flow for `nipa restore <path>...` (git-restore equivalent, `internal/client/usecase/restore.go`):
`WorkingCopy.Restore` loads the local snapshot, expands file/directory targets
over tracked paths (unknown paths error `not tracked`), refuses any target with
staged changes ("push or reset them first" — staged content is not stored
separately), then re-materializes the rest from the local object store through
`materializeFile`, skipping files whose working hash already matches so mtimes
stay stable; refreshed fingerprints are batched into `stat_cache`.
`materializeFile` and conflict resolution go through `replaceFile`
(`materialize_other.go` / `materialize_windows.go`, which clears
`FILE_ATTRIBUTE_READONLY` for the rename): a read-only working file is
replaced and stays read-only, since engine plugins own that state. Offline
while the chunks are cached in `.nipa/objects`; snapshot chunks missing from
the cache go through `pristine.ensureSnapshotContent` (`usecase/pristine.go`):
each missing chunk is first recaptured by re-chunking the working file with
`chunker.Encode` + `storedEncoding` (skipped when a fresh `stat_cache` entry
already shows a different hash; chunks are content addressed, so only the
missing hashes the re-chunk reproduces are stored), and the rest is downloaded
with `downloadMissing` scoped to the pinned commit and those paths through the
optional `WorkingCopy.WithDownloader(auth, client)` seam (CLI `restore`, the
daemon's cached `WorkingCopy` via `RepoOps.Auth`/`Downloader` from
`serveRepoOps`, the MCP restore tool via `UseCases.Auth`/`Connector`); with no
downloader it is a user error `content not cached locally; connect to the
server`. The daemon exposes it as the unary `Restore` RPC returning the
refreshed status.

Flow for `nipa diff [<rev1> [<rev2>]] [--] [<path>...]`: `cli/diff.go` splits
revisions from paths (paths only after `--`; `<a>..<b>`/`<a>...<b>` expand to two
revisions and `...` sets merge-base mode) → `usecase.Diff.Run`. With no revisions
it is offline while the old side is cached (otherwise the old side of
deletions and modifications, binaries only with `--ext-diff`, goes through
`ensureSnapshotContent` like restore, using the `Diff`'s own auth/client as the
downloader; `ReadBlob` with `BlobSnapshot` does the same): `Snapshot()` (paths, hashes, modes and per-file chunk
hashes) + `ListStaged()` → `walkWorkingFiles` (shared with `WorkingCopy.Status`).
Tracked files whose `stat_cache` fingerprint is fresh and whose file hash equals
the old side are reused without reading; everything else (staged additions,
stale fingerprints, real changes) is hashed with `chunkFile` → `diff.Compare`
over path-keyed `diff.Entry` maps; untracked-unstaged files are excluded, missing
tracked files are deletions, staged new files are additions. `--no-cache` reads
every working file. With revisions, each token
resolves to a commit ID first (branch name via gRPC `GetBranchByName`, then tag
name via `GetTagByName`; `HEAD`/`@`
via localrepo `LoadCommit`, falling back to the configured branch; otherwise
`snow.ParseBase36` → gRPC `GetCommit`), then `GetCommit(id).Tree` is flattened
with `diff.FromTree`; missing chunks are downloaded with `downloadMissing`
through the commit-scoped `ChunkScope` (binary content only with
`--ext-diff`). `<a>...<b>` calls `GetMergeBase` with
the two commit IDs and uses `MergeBaseTree` as the old side. Renames always run
through `diff.DetectRenames` (exact hash, line similarity for text, chunk
overlap otherwise); `-w`/`-b` live in `diff.Options` and `diff.FilterIgnored`
drops files whose differences are fully ignored. `--ext-diff` runs
`internal/client/difftool` with git's 7-argument contract (command from
`NIPA_EXTERNAL_DIFF`/`diffExternal`) and only when requested. `--staged`/`Paths`
narrow the result; rendering (patch, stat, name-only, name-status), colors and
the pager live in `cli/diff.go` + `internal/diff`.

Flow for `nipa serve` (client daemon, design in `docs/DAEMON.md`):
`cmd/nipa/serve.go` builds `daemon.NewServer` (proto
`internal/client/grpc/proto/daemon.proto`, generated
`internal/client/grpc/daemonpb`) and publishes `~/.config/nipa/daemon.json`
(`{pid, port, token, version}`, 0600, atomic write); every RPC must carry the
`x-nipa-daemon-token` metadata (constant-time compare), and `Shutdown`/SIGTERM
drain in-flight operations before removing the file. `WatchRepo(root)` resolves
any path inside a clone to its root (up to 32 parents), validates
`.nipa/config` and pins a per-root entry: cached `localrepo` handle +
`usecase.WorkingCopy`, watcher/reconciler, coordinator and a `RepoOps` graph
(`serveRepoOps` builds one usecase set and one gRPC client per root, since the
client transport binds one host at a time). `Status`/`Stage` are read-through
`WorkingCopy` calls under the shared coordinator slot (`Status` reports the
configured branch and the detached `head` marker); `Update`/`Switch`/
`Push`/`Merge`/`Revert` take the exclusive slot (FIFO, queued exclusive blocks
later shared waits) and stream `OpEvent`s (queued/started/progress/result/
failure; cancelling the stream always terminates it with a failure event).
`Switch` detaches at a tag when its request carries `tag`
(`UpdateRunner.SwitchTag`), otherwise switches branch.
`Diff` streams rendered patch/stat/name output (64 KiB chunks, file-by-file for
patches) under the shared slot (`show` + one revision diffs a commit against
its first parent, `DiffOptions.Show`); `DiffFiles` runs the same request but streams
one structured `DiffFile` per path (status, `internal/preview` viewer kind,
side metadata, counts, `greet.DiffHunkDetail` hunks capped at 1 MiB per file)
for the desktop GUI. `ReadBlob` streams one file version (working copy,
offline `snapshot`, or a `revision` whose chunks are downloaded first) through
`usecase.Diff.ReadBlob`, and `Preview` decodes old/new images with
`internal/preview` into downscaled PNG chunks plus pixel-diff stats (415 for
non-image or unsupported formats). `WatchChanges` streams coalesced change
notices per root (watcher batch paths after the stat refresh; empty = refresh
all after ops and stage/restore) for the GUI, and `Status` reports `added`
(staged paths the snapshot does not track). An fsnotify watcher debounces events into
dirty batches and a background reconciler refreshes `stat_cache` through
`WorkingCopy.RefreshStatEntries`; watcher failure or event loss is harmless
(read-through status still stat-detects). `Proxy*` RPCs forward `greet.*`
messages verbatim over `Client.ServiceClient()`, filling the clone's
org/project context from `.nipa/config`; server-scoped `AccountStatus`/`Login`/`LoginSSH`/`ListProjects`/
`RemoteBranches`/`RemoteTree` run on a fresh connection per call
(`Options.Accounts`, wired in `cmd/nipa/serve.go`; the daemon never prompts),
`ProxyTagList`/`ProxyTagCreate`/`ProxyTagDelete` forward the tag RPCs,
`ProxyMergeRequestUpdate`/`Commits`/`Checks`/`Timeline`/`AddComment`/`Reply`/
`Resolve` complete the merge-request surface, `WatchEvents` polls the server per root (branch log vs the pinned commit,
lock list, open merge requests' review requests) and streams `RepoActivity`
with teammate `pushed`/`lock_released`/`review_requested` events,
`Conflicts`/`ResolveConflict`
(`usecase.Conflicts` over the merge/revert state's `conflict_kinds`,
`conflict_binary` and `resolved` fields, filled by `applyThreeWay`) list and
settle conflicted files for the GUI, `Status`
reports a waiting merge/revert as `pending` (+ `pending_remaining`),
`Clone` streams `OpEvent`s outside any repo slot and `SetSparse` stores the
prefixes then updates under the exclusive slot; `LockStatus` joins the lock list,
the branch scope (default branch = mainline locks), admin rights and the
caller id (`Client.UserID`, the access token's JWT `sub`) with the status into
per-file `mine`/`theirs`/`needed`/`free` states for changed binaries; `UnwatchRepo` waits for in-flight refs
before closing handles.

JSON bridge (design in `docs/DAEMON.md`): `Server` also binds a second loopback
listener (`--json-port` / `NIPA_DAEMON_JSON_PORT`, published as `json_port` in
`daemon.json`) served by `jsonbridge.go`, a reflection-driven HTTP/1.1 adapter
for engine plugins that cannot speak gRPC (Unity first, plan in
`docs/plans/UNITY_PLUGIN.md`): `POST /v1/<Method>` protojson-decodes the body
and invokes the daemon's own gRPC listener with the forwarded
`x-nipa-daemon-token` (unary = JSON, server streams = NDJSON, errors mapped to
HTTP statuses, non-loopback `Host`/`Origin` refused). `MetaWarnings`
(`daemon/metawarnings.go` over `internal/unitymeta` / `internal/godotmeta`,
`engine` = `unity|godot`, empty detects `project.godot` then
`ProjectSettings/ProjectVersion.txt`) is the sidecar lint RPC the desktop
service and engine plugins call. `LoginSSH` takes an optional `key_path` +
`passphrase` (exactly that key, no fallback, never logged).

Flow for storage quota (design in `docs/CLOUD_STORAGE_QUOTA.md`):
`internal/usecase.StorageLedger` is attached with `Chunk.WithStorageLedger`;
nil (the free default) skips all accounting. `PresignUploadURLs` calls
`checkHeadroom` before signing (advisory), `ConfirmUploads` attributes the
present chunks in one transaction, and `MergeForMergeRequest` attributes
server-generated diff3 chunks through `Chunk.AttributeProjectChunks` (the
`chunkUploader` seam) before the commit lands. Quota failures are 402
(`domain.NewErrorQuotaExceeded`), crossing gRPC as `ResourceExhausted`. The
enterprise implementation lives in `ee/storageusage` (postgres,
`STORAGE_LEDGER=cloud`; `SAAS_MODE=1` auto-enables it so the seeded free-tier
quota is enforced; the free `cmd/nipad` ignores the setting and keeps
running with no ledger, no quota and the routes 404): it bills
every org to one owner user (lazily seeded from
`organizations.created_by_user_id`), tracks per-project chunk links with
cached project/org counters, and enforces `owner_quota` (NULL/0 =
unlimited; missing row = `STORAGE_LEDGER_DEFAULT_QUOTA_BYTES`, default
15 GiB, or `SAAS_FREE_QUOTA_BYTES` in SaaS mode via `ledgerDefaultQuotaBytes`). `RecountOrg`
rebuilds links and counters from the stored history. REST routes
(`/orgs/{org}/storage`, `/billing/me`, admin quota/transfer/recount) are
registered on the shared `/api/v1` WebService via `serverapp.WithRoutes` only
when the ledger is enabled, so they 404 otherwise; the SPA hides the org
storage card on 404, while the `/settings/billing` page is always linked from
the header and the profile settings nav (it shows an empty state when the
ledger is off).
`owner_quota` has two columns: `quota_bytes` is the computed (billing)
quota written by `Ledger.SetComputedQuota`, `override_bytes` the super-admin
override (`PUT /admin/billing/owners/{user}/quota` → `SetQuotaOverride`, a
null body overrides to unlimited = 0; `DELETE` → `ClearQuotaOverride`). The
effective quota is `COALESCE(override_bytes, quota_bytes)`, with the same
NULL/0/missing-row semantics; `Ledger.BillingOwner` is the exported org →
owner resolution.

Flow for billing (design in `docs/BILLING.md`, plan in
`docs/plans/BILLING.md`): the only free-side change is the nil-able `usecase.SeatGate` seam
(`Admit(ctx, orgID, userID, insert)`), attached through
`UsecaseDeps.SeatGate` to `Org.AddMember` (not role updates or org
creation), `Group.AddMember` and user-subject `Permission.CreateRule` (group
rules add no seat); a nil gate runs `insert` directly. The postgres Org,
Group and PBAC repositories resolve the transaction from the context
(`dbtx.New`) so the insert joins the gate's transaction. `ee/billing.Service`
implements it over small interfaces (repository, ledger, transactor; like
`ee/registration`, unit-tested with stubs), while the SQL lives in
`postgres.BillingRepository` (`ee/repository/postgres/billing_repository.go`).
`Admit` runs one transaction: resolve the org's billing owner, take the
per-owner `pg_advisory_xact_lock` (`LockOwner`, own key namespace), admit
users that already hold a seat (`IsSeat`), otherwise count seats
(`CountSeats`: distinct non-deleted users reached through `org_members`,
`group_members` of non-deleted groups and user `pbac_rules` of the owner's
non-deleted orgs, owner included) against the seat limit (live
subscription's paid seats + `BILLING_FREE_SEATS`: the first free seats come
with every plan; `BILLING_FREE_SEATS` alone without one), and refuse with a
402 `seat limit reached`.
`Service.Apply` takes a provider-neutral `domain.BillingSubscription`,
upserts it by `provider_subscription_id` only when `provider_updated_at` is
newer, and recomputes `quota_bytes` from the live subscription
(`active|trialing|past_due`, newest update) with
`entitlement.go` (`free + seats*seat_bytes + packs*pack_bytes`, 0 free =
unlimited, saturating); it never touches `override_bytes`.
`Service.ReconcileQuotas` (`reconcile.go`) runs at SaaS startup as a
background `serverapp.Dispatcher` (`quotaReconcileJob`, provider-less service
when billing is off, failures only logged, cancelled on shutdown): it keyset-pages `owner_quota` (`QuotaOwners`, 500), recomputes
each owner under `LockOwner`, rewrites only differing `quota_bytes`, skips
deleted users and audits `billing.quotas.reconciled` when anything changed
(also after a partial run, with `complete: false`). `internal/config` carries
`BILLING_PROVIDER` (`off` default), `PADDLE_*` and `BILLING_*` keys with
`ValidateBilling` (every key, `SAAS_MODE=1`, the ledger, free seats ≥ 1,
bytes > 0); `ee/cmd/nipad/billing.go` (`buildBilling`) wires nothing with
`off`, otherwise builds the Paddle client and the service, passes it as
`UsecaseDeps.SeatGate`, registers the routes, the webhook root and the
`/billing/me` hook. The provider seam is `billing.Provider`
(`ee/billing/provider.go`: `EnsureCustomer`, `CreateCheckout`,
`UpdateSubscription`, `Get/ListSubscriptions`, `PortalURL`, `ParseWebhook`,
`DecodeEvent`, `EventSubscription`; `Items{seats, storage_packs, interval
month|year}`, seats = paid seats, `Validate` needs seats + packs ≥ 1, price
ids always come from config). `ee/billing/paddle`
implements it over the Paddle Billing REST API (`sandbox-api.paddle.com` /
`api.paddle.com` by `PADDLE_ENVIRONMENT`, bearer key, 15 s timeout):
`POST /customers` (a `customer_already_exists` 409 adopts the `ctm_` id from
the error detail, else `GET /customers?email=`), `POST /transactions` with
`custom_data.user_id` (base36; seat/storage items only for a non-zero
quantity), `PATCH /subscriptions/{id}`
(`prorated_immediately`, `on_payment_failure: prevent_change`, full item list),
`GET /subscriptions?customer_id=` (cursor `after`), `POST
/customers/{id}/portal-sessions` (`urls.general.overview`). Mapping: seats and
packs are the quantities of the configured seat/storage prices, the interval
is the matched price's, an unknown price, mixed intervals or no items is an
error (packs-only maps to 0 seats),
`cancel_at_period_end` = `scheduled_change.action == "cancel"`. Paddle 409s and
`subscription_locked*`/`subscription_update_when*` codes become 409, the rest
502 (detail internal, never the key). Webhooks verify `Paddle-Signature`
(`ts=…;h1=…`, HMAC-SHA256 over `ts:rawBody`, any `h1`, 5 min tolerance;
`paddle.Sign` builds one for tests). `Service`: `Checkout` (409 with a live
subscription or when paid seats < seats used − free seats; customer stored in
`billing_accounts` once), `ChangeSubscription` (404 without a live
subscription, 409 for the same seat rule or below used bytes, applies the PATCH answer immediately), `Portal`,
`Summary`, `Sync`, `HandleEvent` (stores `billing_events` first, a
processed/failed duplicate is a no-op, an unfinished one is retried; user from
`billing_accounts.provider_customer_id` (`custom_data` must match it, unknown customers are rejected, upserts never change `user_id`); 4xx outcomes such
as unknown/deleted users or unmappable data are marked failed and answered
200, everything else returns the error), `Replay`; only
`subscription.created|updated|trialing|activated|past_due|paused|resumed|canceled`
apply, other types are stored as processed. HTTP: `POST /api/webhooks/paddle`
is its own WebService (`billing.WebhookService` through
`serverapp.WithWebService` / `api.WithWebService`, no bearer or origin filter,
1 MiB raw body cap, 401 bad signature, 500 only for transient errors);
`/api/v1` gains `POST /billing/checkout` → `{transaction_id}`, `PATCH
/billing/subscription`, `POST /billing/portal` → `{url}`, super-admin `POST
/admin/billing/owners/{user}/sync` and `POST
/admin/billing/events/{id}/replay`; `GET /billing/me` stays in
`ee/storageusage` and merges `storageusage.BillingSummary` (plan, status,
interval, seats (paid quantity), seats_used, seat_limit (paid + free), storage_packs,
current_period_end, cancel_at_period_end) through `RoutesConfig.Billing` only when set;
`registration.WithBilling` adds `billing: {provider, environment,
client_token, prices}` to `GET /api/v1/auth/capabilities`. The `ee/e2e`
smoke (`TestEnterpriseBillingPaddleSmoke`) drives signed fake events against a
fake Paddle. The SPA side is the `/settings/billing` plan card and editor (see
Web UI below).

Flow for rate limiting (design in `docs/RATE_LIMIT.md`): `internal/ratelimit`
holds the `Store` seam (atomic token-bucket `Take`), the `Limiter` (named scope
rules, fail-open on store errors, nil limiter allows everything), the `Memory`
store (free `nipad`, also selectable on `nipad-ee`) and `ClientIP`
(`X-Forwarded-For` honoured only from `RATE_LIMIT_TRUSTED_PROXIES`, right-most
untrusted hop). `ee/ratelimit.Redis` is the shared store (one Lua script);
`RATE_LIMIT_STORE=memory|redis` is resolved by `serverapp.BuildRateLimiter`
(`config.RateLimitStoreName`: the free main passes no redis factory, so `redis`
is a startup error there; on `nipad-ee` an unset store picks redis when
`RATE_LIMIT_REDIS_ADDR` is set). One limiter feeds `UsecaseDeps.RateLimiter`
(`usecase.Auth.WithLimiter`, the per-email `auth_id` scope for password and SSH
logins, 429 via `domain.NewErrorTooManyRequests`) and `serverapp.WithRateLimit`
(`api.RateLimit`: global/chunk middleware inside the access log, `auth_ip`
filter on the `/api/v1/auth` WebService, `api_user` filter after the bearer
filter; `grpcserver.RateLimiter` chained after JWT: public login RPCs per IP,
the rest per user). REST answers 429 with `Retry-After`; gRPC answers
`ResourceExhausted` with the `rate limit exceeded` prefix, which
`client/grpc.toDomainError` maps back to 429 (other `ResourceExhausted` stays
402). Metric: `nipa_ratelimit_total{scope,result}`.

Flow for the audit log (design in `docs/AUDIT_LOG.md`, both editions): an
append-only `audit_log` table (no foreign keys, metadata JSON TEXT on sqlite /
JSONB on postgres) written by `usecase.Audit` (`AuditRecorder`), wired in
`serverapp.BuildUsecases` and attached to the org, group, permission, branch
protection, file lock (force release only), user, project and webhook
usecases through `WithAudit` (nil = off). Each audited write and its record run
in one `WithinTx` via `auditedWrite`, so a failed record rolls the change back;
repositories touched by audited writes must be `dbtx`-aware (no private
`BeginTx`). Action names live in `internal/domain/audit_actions.go`; reads are
never audited and metadata never carries secrets. The REST auth filter and the
gRPC interceptors attach `domain.RequestMeta` (client IP through
`ratelimit.ClientIP` + `RATE_LIMIT_TRUSTED_PROXIES`, user agent); entries
without a claim are `system` (SaaS signup/OAuth). Super admins read it at
`GET /api/v1/admin/audit` (actor/org/project/action prefix/target/since/until,
keyset `after`, `next_cursor`). On `nipad-ee`, `ee/billing.Service` and
`ee/storageusage.Ledger` take the same recorder (`WithAudit`, wired after
`BuildUsecases` in `ee/cmd/nipad/main.go`) for the `billing.*` actions:
quota override set/cleared and owner transfers (through the exported
`usecase.AuditedWrite`), checkout, plan change, sync, replay, and
`billing.subscription.applied` inside `Apply`'s transaction only when the
upsert wrote (claim actor when present, otherwise `provider`/`paddle` with the
event id; old values read from the live subscription and
`Ledger.ComputedQuota`). `billing.seat.denied` is the exception: written after
`Admit` rolls back on a `dbtx.Detach`ed context, failures only logged.

Flow for tracing (design in `docs/TRACING.md`): `internal/obs/tracing.go`
builds an OTLP gRPC tracer provider (`SetupTracing`), installed globally with
the W3C propagator only when enabled — otherwise every path stays on the OTel
no-op provider (no exporter, no spans, no headers). Server: config
`TRACING_ENABLED` / `TRACING_OTLP_ENDPOINT` / `TRACING_STDOUT` /
`TRACING_SAMPLE_RATIO` / `TRACING_SERVICE_NAME`, wired in both mains and
`serverapp.WithTracing` (gRPC via the in-module
`google.golang.org/grpc/stats/opentelemetry` server option, HTTP via
`otelhttp` around the non-gRPC mux, flush before `os.Exit` in the signal
handler). Client: env-only `NIPA_TRACING` (`1` or `stdout`),
`NIPA_TRACING_OTLP_ENDPOINT`, `NIPA_TRACING_SAMPLE_RATIO`;
`obs.ActiveTracing().GRPCDialOption()` is appended in
`Transport.Connect`, chunk PUT/POST/GET requests inject `traceparent`
manually (`injectTraceContext`) so a single trace spans CLI → gRPC →
presigned HTTP → S3/local store. Manual spans go through
`obs.StartSpan`/`obs.FinishSpan` with the shared `nipa.*` attribute
constants: client `nipa.push`/`push.upload`/`push.commit`/`nipa.clone`/
`nipa.update`/`nipa.switch`/`nipa.sync`/`chunk.upload.*`/`chunk.download.get`,
server `push.apply`, `chunk.presign_*`/`chunk.confirm`/`chunk.store`/
`chunk.verify`/`chunk.download` and the per-object `storage.local.*` /
`storage.s3.*` store spans.

Flow for observability to OpenObserve (SaaS; design in `docs/OBSERVABILITY.md`):
`internal/obs/otlp.go` holds the shared OTLP endpoint resolution
(`otlpEndpointTarget.traceOptions`/`logOptions`, plus the
`traceHTTPOptions`/`logHTTPOptions` variants), transport selection
(`TRACING_OTLP_PROTOCOL` = `grpc` default / `http`; the OTLP/HTTP exporters get
the `/v1/traces` or `/v1/logs` signal path appended to a configured URL prefix
by `httpSignalURL` before `WithEndpointURL` uses it as-is), `ParseHeaders`
(comma-separated `KEY=VALUE`, first `=` only so base64 padding survives) and the
shared resource; `TRACING_OTLP_HEADERS` feeds the trace exporters.
`obs.SetupLogs` (`internal/obs/logs.go`, OTLP + otelslog bridge)
ships slog records over OTLP when `LOGS_OTLP_ENABLED` (endpoint and protocol
fall back to the `TRACING_*` values, same headers), fanned out next to the
console/JSON output via `fanoutHandler` in `obs.Setup` with a
`levelFilterHandler` so every sink obeys `LOG_LEVEL`; both exporters flush on
shutdown via `serverapp.WithLogs`. Metrics are pull-based (`/metrics`, `nipa_*`
series) or pushed directly with `METRICS_OTLP_ENABLED`: `obs.SetupMetricsPush`
(`internal/obs/metrics_push.go`) bridges the Prometheus registry through
`contrib/bridges/prometheus` into a periodic OTLP reader (endpoint/protocol
fall back to `TRACING_*`, headers shared, `METRICS_OTLP_INTERVAL_SECONDS`
cadence), flushed via `serverapp.WithMetricsPush` — no collector required
(scraping and remote-write agents remain supported in docs/OBSERVABILITY.md).

Server merge-request diffs reuse the same engine: `usecase.MergeRequest.Diff`
resolves the source and target heads, `GetMergeBase`, and calls
`Branch.TreeDiffBetween` (`internal/usecase/browser.go`), which walks the two
commit trees in lockstep (`Branch.changedTreeEntries`, `tree_walk.go`): a pair
of directories with the same stored hash is skipped without a query, the others
are loaded with each side pruned by the caller's read filter, and the flattened
entries go to `diff.TreeDiffEntries(baseEntries, headEntries, chunkStore.Get)`
— a three-dot diff like GitHub/GitLab, exposed at
`GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/diff`. The web
UI commit pages diff a commit against its first parent through the same
`CommitDiff` walk (`?path=` filters to a file/dir). A zero hash counts as
"unknown" and is never skipped. `commitChanges` (merge/fast-forward write
checks, binary lock planning) and `VisibleChunks` (which also skips directories
already visited for an earlier commit, keyed by path and hash) share the idea.

## Commands

From `Makefile`:

- `make godot-test` — CMake + doctest for the Godot plugin `core/` with the gcovr
  80% line gate; `make godot-build` builds the GDExtension (fetches godot-cpp 4.4).

- `make sqlc` — regenerate all sqlc packages: sqlite +
  `internal/repository/sqlc/localrepo` for the free client/server, and postgres
  in `ee/repository/postgres/sqlc` (inputs: `ee/db/migrations/postgres`,
  `ee/repository/postgres/queries`). Uses
  `go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest`.
- `make mock` — `go generate ./...` (regenerates gomock mocks from `//go:generate`).
- `make migrate-up` / `make migrate-down` — sqlite only, `go run ./cmd/migrate`
  (default `DSN=nipa.db`). `nipad` also applies pending sqlite migrations at
  startup. Postgres equivalents: `make migrate-up-postgres DSN=postgres://...`
  / `make migrate-down-postgres` via `go run ./ee/cmd/migrate`; `nipad-ee`
  applies pending postgres migrations at startup.
- `make migrate-create name=<name>` — new up/down migration pair in both
  dialect dirs: `db/migrations/sqlite` and `ee/db/migrations/postgres` (both
  embedded via `go:embed` and read by `sqlc.yaml`).
- `make build` — web + `bin/nipad` + `bin/nipa`; `make build-server-ee` —
  `bin/nipad-ee`; `make build-all` — web + both servers + client. Builds inject
  `internal/version` via `-ldflags` from
  `VERSION ?= git describe --tags --always --dirty` / `REVISION`; plain
  `go build` falls back to `debug.ReadBuildInfo` (`dev-<sha>` or the VCS
  pseudo-version). `make release version=vX.Y.Z` tags a clean HEAD (annotated;
  push manually). `nipa version [--server] [url]` reports the client build and
  probes the server through the public `GetServerVersion` RPC; both servers
  also answer unauthenticated `GET /version` (JSON) and `-version`, and log the
  version at startup. `daemon.Version` defaults to the build version (Ping /
  `daemon.json` / `nipa mcp`).
- `make test` — `go test ./...` (requires Docker for the testcontainers-backed
  postgres/minio suites); `make test-ee` — `go test ./ee/...`.
- `make test-client` / `test-client-ee` / `test-client-both` — the Ginkgo
  black-box client e2e suite (`tests/run.sh [free|ee|both]`): builds `bin/nipa`,
  starts a real `nipad`/`nipad-ee` (ee starts postgres + minio from
  `tests/docker-compose.ee.yml`) and drives the compiled CLI against it, so both
  editions are verified to behave the same client-side. Covers every command and
  flag (clone/push/update, branch, switch, merge, revert, log, diff,
  sparse-checkout, tag, lock, mr, acl, group) plus `nipa serve` (daemon gRPC),
  `nipa mcp` (stdio JSON-RPC) and webhook deliveries (events, filters,
  signatures, ping/redelivery), with multi-user identities for permission
  (incl. org owners' implicit project access vs. plain members' deny-by-default)
  and lock scenarios. `NIPA_TOKEN_FILE` makes `securestorage` file-backed so the
  CLI runs without an OS keyring/TTY; the suite skips unless `NIPA_TEST_HOST` is
  set. Details in `tests/README.md`.
- `make web` / `web-dev` / `web-install` — build the SPA into
  `web/server/dist` / run the Vite dev server (proxies `/api`, `/docs` to
  `NIPA_SERVER_URL`) / `npm install`. `make marketing-dist
  [MARKETING_DIR=../nipa-web]` builds the Astro marketing site with
  `MARKETING_BASE=/web/` and syncs it into `ee/web/marketing/dist` for the
  embedded fallback; production marketing is Netlify (base `/`) behind the
  Caddy proxy and needs no rebuild.
- `make proto` — regenerates pb from `internal/grpc/proto/server.proto` into
  `internal/grpc/pb` and from `internal/client/grpc/proto/daemon.proto` into
  `internal/client/grpc/daemonpb` (the daemon proto imports the server one;
  the target `M`-maps that import onto `internal/grpc/pb`).
- `make lint` — golangci-lint via Docker image (`.golangci.yml` enables
  errcheck/govet/ineffassign/staticcheck/unused/misspell/unconvert; formatters
  gofmt/goimports).

Direct: `go build ./...`, `go vet ./...`, `go test ./...`.

- `make bench-upload mb=512` (or `NIPA_BENCH_MB=512 go test ./internal/e2e
  -run TestEndToEnd_UploadThroughput -v`) measures upload throughput against an
  in-process server: it pushes a repetitive text file, a random binary file and
  a packed-asset `.png`, logs MiB/s, encoding, chunk and HTTP PUT counts, then
  re-clones and verifies the bytes. Skipped unless `NIPA_BENCH_MB` is set;
  `NIPA_BENCH_WORKERS` pins transfer concurrency (otherwise the adaptive tuner
  is exercised).
- Performance tooling (see `docs/PERFORMANCE.md`): `make bench-download mb=512`
  times a fresh clone plus a no-op update (`TestEndToEnd_CloneThroughput`),
  `make bench-scale gb=2` pushes/clones one N-GiB file and asserts peak heap
  growth stays below the file size (`NIPA_BENCH_GB`,
  `TestEndToEnd_GBScaleMemoryCeiling`), `make bench-unit` runs the
  chunker/diff/treehash micro-benchmarks, and `make load-test args='...'` runs
  the `tests/load` scenarios (push/clone/mixed, one branch and worktree per
  worker, client-side p50/p95/p99 plus a `/metrics` delta) against a running
  server. Requested metrics must be namespace-filtered (`nipa_*`), bench and
  load code stays env-gated so CI never runs it.
- The server opens SQLite through `db.OpenSQLite`, which appends
  `_pragma=busy_timeout(10000)`, `_pragma=journal_mode(WAL)` and
  `_txlock=immediate` to the DSN. Without WAL + a busy timeout, concurrent
  reads (every RPC resolves the project first) collide with confirm/Push
  writes and surface as `database is locked (5) (SQLITE_BUSY)`; without
  immediate transactions, a deferred transaction that reads before writing
  (e.g. `ApplyPushAll` resolving chunk rows) fails upgrading with
  `SQLITE_BUSY_SNAPSHOT` when another connection commits in between, and the
  busy handler is not invoked for that case.

## Deployment (zero-downtime SaaS)

`docs/DEPLOY.md` is the runbook for running the SaaS on a VPS without Docker:
two `nipad-ee` colors (`nipad-ee@blue`/`@green`) behind Caddy, which terminates
TLS for the single h2c port and proxies to the active color. Assets live in
`deploy/` (`nipad-ee@.service` template, `nipad-ee.env.sample`, `caddy/Caddyfile`,
`caddy/caddy.env.sample` + `caddy/caddy.service.d/nipa.conf` for `MARKETING_UPSTREAM`,
`ci-deploy.sh` driven by `.github/workflows/server-deploy.yml` for the dev (main) and
production (`v*` tag) GitHub environments,
`upgrade.sh`). The upgrade script installs the new binary into the idle color,
waits for `/readyz`, rewrites the Caddyfile upstream + `systemctl reload caddy`
(a reload never drops connections), waits for the old color to drain, then
SIGTERMs it. `internal/serverapp` drains gracefully: on SIGTERM `/readyz`
answers 503 (`readinessHandler` + drain flag), the gRPC health service reports
NOT_SERVING, and one `SHUTDOWN_TIMEOUT_SECONDS` deadline (config default 15,
unit's `TimeoutStopSec` must stay above it) bounds the whole shutdown:
`httpServer.Shutdown`, the dispatcher stop and the exporter flushes. `Run`
returns only after the drain finished (`serveUntilShutdown`): `Serve` returns
the moment the listener closes, so returning then would exit the process
mid-request. Both colors may run
concurrently during a flip:
snowflake node ids are leased from postgres (never pin `SNOWFLAKE_NODE_ID`),
email deliveries are claimed atomically, but webhooks can double-deliver in the
overlap window (consumers must be idempotent). Migrations must stay forward
compatible for one release.

## Conventions

- **Package layout**: consider packages under `internal/` as the border; use the
  existing import ordering (stdlib, third-party, then `github.com/nipalab/nipa/*`).
- **IDs**: server-side records use `github.com/nipalab/nipa/internal/snow`
  (`snow.ID`, `.Base36()` for wire, `.Int64()`/`snow.ParseBase36` for DB/proto).
  The 8-bit node id comes from `SNOWFLAKE_NODE_ID`; when unset `nipad-ee` leases
  a unique one per instance from `snowflake_node_leases` (`ee/nodelease`,
  renewed until shutdown, exit on lost lease) so multi-pod never collides,
  while the free `nipad` stays single-instance with the static default 0.
  Client localrepo tables do NOT use snow IDs.
- **Errors**: `internal/domain.Error` with HTTP-style `Code` + `Message` +
  `InternalMessage` + `Cause`. Constructions: `NewErrorNotFound(msg)`,
  `NewErrorNoPermission()`, `NewErrorUser(msg)`, `NewErrorDatabase(internalMsg)`,
  `NewErrorInternalServer(...)`. Predicates: `domain.IsErrorNotFound(err)`,
  `domain.IsErrorNoPermission(err)`. Client mirrors this with
  `internal/client/domain.Error` (`NewUserError` 400, `NewTokenError` 401).
- **SQL**: schema + queries are sqlc inputs (`db/migrations/sqlite` +
  `db/queries/sqlite` for the free server, `ee/db/migrations/postgres` +
  `ee/repository/postgres/queries` for enterprise, and
  `internal/client/localrepo/schema|queries` for the client). After changing a
  `.sql` file, run `make sqlc` and check the generated Go compiles. Named params
  (`:name`) are converted to `?N` in `database/sql`.
- **sqlc gotchas**: a multi-statement `:exec` query has its SQL truncated by sqlc
  to one statement — use separate single-statement queries instead. Full-table
  `DELETE` without `WHERE` trips SonarQube (S1035-ish); keep them out. On
  sqlite, mixing unnamed `?` placeholders with `sqlc.narg(...)` makes sqlc emit
  a broken `?N` mixture that misbinds args at runtime — name every parameter
  with `sqlc.arg(...)` once a query uses `narg`. On
  postgres, quote keyword arg names (`sqlc.arg('limit')`), cast nullable filters
  and `LIMIT`/`OFFSET` params (`sqlc.narg('x')::boolean`,
  `sqlc.arg('limit')::bigint`), and keep integer columns `BIGINT` so generated
  types stay `int64` like sqlite.
- **Testing**: testify (`require`/`assert`) + gomock (`go.uber.org/mock`,
  `github.com/golang/mock` legacy mocks exist in `*_mock_test.go`). Server usecase
  tests use `NewMock<h1>...` gomock controllers; client usecases use hand-written
  stubs. SQLite-backed tests use `t.TempDir()` for real DBs. Enterprise postgres
  tests are testify suites over testcontainers (postgres + minio, one schema per
  test); override with `NIPA_TEST_POSTGRES_DSN`/`NIPA_TEST_S3_ENDPOINT`/
  `NIPA_TEST_S3_IMAGE` when using service containers.
- **Mocks**: live next to the code with the name suffix `_mock_test.go`; regenerate
  with `make mock`. In `internal/grpc/server`, `*_deps_mock_test.go` /
  `*_usecase_mock_test.go` are the mocks.
- **gofmt/golangci**: new/edited files must be gofmt-clean. One legacy file was
  left unformatted on purpose — do not reformat it unless the whole file is
  already clean: `internal/client/domain/url_test.go`.

## Client localrepo design (`internal/client/localrepo`)

- `.nipa/` inside the clone target: `config` (json `{url, branch, sparse?,
  head?}`; `head` is the detached marker `{"kind":"tag","name":"v1.0.0"}` and
  is absent while on a branch) written
  atomically via `atomicWrite`, `nipa.db` (SQLite, `modernc.org/sqlite`,
  `_pragma=foreign_keys(ON)` in the DSN — cascades silently don't fire otherwise),
  and `objects/` with Git-style loose chunk objects.
- Schema (in `schema/schema.sql`, embedded via `//go:embed`, also the sqlc source):
  - `tree_nodes(path TEXT PRIMARY KEY, parent_path, hash BLOB, mode)`
  - `files(path TEXT PRIMARY KEY, tree_path FK->tree_nodes ON DELETE CASCADE, hash,
    size_bytes, mode, is_binary, encoding, chunks BLOB)` — `chunks` is
    the concatenated 32-byte hashes of the file's chunks (metadata only, no
    content); `encoding` is `raw` or `zstd` and refers to the stored chunk bytes.
  - `meta(key PK, value)` — stores `tree_hash`, the pinned `commit_id` /
    `commit_hash`, and the pending `merge_state` / `revert_state` JSON.
  - `staged_files(path PK, staged_at)`.
  - `pinned_paths(path PK, pinned_at)` — lock pins (`PinPaths`/`UnpinPaths`/
    `ListPins`), stored normalized like snapshot paths
    (`NormalizePathPrefix`: slash separated, no leading slash, root not
    pinnable); a pin covers its exact path or a directory prefix
    (`domain.PrefixCovers`, so `a/b` never covers `a/bc`).
  - `stat_cache(path PK, size_bytes, mtime_ns, mode, hash, cached_at)` — working
    file fingerprints whose `hash` was validated when the content was last hashed;
    `cached_at` (unix nano) must be newer than `mtime_ns` for the row to be trusted.
- Content lives in `.nipa/objects/<hash[:2]>/<hash[2:]>` (content-addressed loose
  objects, written temp+rename (`.tmp-*` in the object's directory) so `nipa
  update` can skip unchanged content). `StoreChunks`/`StoreChunk` write objects; `OpenChunk`/`LoadChunk` read
  them; `MissingChunks` stats them. Update/switch download only the chunks of
  files they rewrite and store only those of cached files (zstd text, pinned
  raw content, raw files without known chunk sizes): raw unpinned chunks go
  straight into the working file and never touch `.nipa/objects`, so there is
  no temporary 2× disk peak. Merge/revert cache materialized content, and `Push`
  caches the chunks it uploads before sending them except raw chunks of
  unpinned paths (the upload windows carry the bytes in memory). The DB holds metadata only; file→chunk hashes are stored as
  the `files.chunks` blob (no chunk catalog table), which is what lets `nipa diff`
  rebuild the old side offline.
- Object GC: `PruneObjects(keep)` deletes every object not in `keep` plus
  `.tmp-*` files older than an hour. `usecase.pruneCache` (`gc.go`) keeps the
  chunks of the `Snapshot()` files `cachedInPristine` accepts (zstd text, or
  raw content covered by a pin), so binaries are not kept twice, and it is a
  no-op while `merge_state` or
  `revert_state` is pending. It runs after a successful clone, update, switch,
  tag switch and `Push.Run` (deferred `pruneAfterSuccess`: skipped for repos
  without `PruneObjects`, failures only `slog.Warn`, never fail the operation),
  and on demand through `nipa gc` (`usecase.GC`), which prints the objects and
  bytes freed. Processes coordinate through `.nipa/lock` (`localrepo/lock.go`,
  `flock` on Unix, `LockFileEx` on Windows, a fresh handle per acquisition so
  holders in one process conflict like separate processes): clone, update,
  switch, tag switch, push (and `Plan`), merge, revert, restore, diff and
  `ReadBlob` hold it shared for the whole call (`lockShared` right after
  `Init`; the daemon gets it per operation through the same usecases, never
  per watched root), and prune takes it exclusive without waiting, returning
  `Skipped` with `repository busy` (`nipa gc` prints `skipped: repository
  busy`). The automatic prune runs after the operation released its shared
  lock (defer order), so it never upgrades in place.
- `SaveTree` semantics (see `store.go`): one transaction. The wanted tree is
  flattened (nodes parent-first) and diffed in memory against the stored
  `tree_nodes`/`files` rows (`syncRows`): parent, hash, mode, size, binary flag,
  encoding and the chunk blob are all compared, never the tree hash alone,
  because sparse, filtered and locally built trees do not guarantee that equal
  hashes mean equal rows. Only new or differing rows are upserted and rows that
  left the tree are deleted one by one, so saving a 10k-file clone with 5
  changes reads the stored rows and writes ~5 files plus their ancestor nodes
  (~20 ms), not the whole table. `Snapshot()` returns every `files` row. The same transaction drops `stat_cache` rows whose path left `files`.
  `meta_tree_hash` is re-set each save. `SaveTree(nil)` deletes every row.
  Cached objects are never touched by `SaveTree`; only the GC above removes them.
- `stat_cache` is what keeps `nipa status`/`nipa diff` cheap on big binaries: a
  tracked file whose `(size, mtime)` match a fresh fingerprint (`mtime < cached_at`)
  is trusted without reading, while misses are rehashed (bounded worker pool in
  `Status`) and written back in one `SaveStatEntries` transaction. Fingerprints are
  recorded only when content is known — files materialized by `syncWorkingCopy` /
  `applyThreeWay` and files scanned by `Push`; update's no-op skip path is
  deliberately left alone because it never verified the file, so a dirty file is
  hash-detected once by `status`. Paths are working-copy relative (no leading
  slash) and the sweep strips `files.path`'s leading slash. `status --no-cache` /
  `diff --no-cache` force a full rehash (and refresh the cache). Accepted blind
  spot, same as SVN/Git: an edit that preserves size and mtime is invisible until
  the mtime changes.
- There is no schema migration: the local DB is recreated from scratch (an old
  clone just needs to be deleted and re-cloned).
- The `localRepo` interface (used by `usecase.Repo.Clone`): `Init(target)` /
  `SaveConfig(domain.Config)` / `LoadConfig` / `SaveTree(*serverDomain.TreeNode)`,
  plus the content cache methods `MissingChunks` / `StoreChunks` / `OpenChunk` /
  `LoadChunk`, the working-copy fingerprints `SaveStatEntries` (`LoadStatCache` on the
  status/diff interfaces), staging (`StageAdd` / `StageRemove` / `ListStaged` /
  `ClearStaged`), the head pin (`SaveCommit` / `LoadCommit`) and the
  pending-operation state (`Save/Load/ClearMergeState`,
  `Save/Load/ClearRevertState`). `Push.Run` reads both pending states: merge state
  wins for the base tree/second parent (plus `TargetCommitID`), otherwise
  `RevertState.CurrentTreeHash`/`CurrentCommitID` is the base, and when neither is
  pending the pinned head commit (`LoadCommit`) is sent as `base_commit_id`; both
  states are cleared only after a successful push, and a multi-target revert
  sequence blocks `Push.Run` until `--continue`/`--abort`.
- `clientDomain.Snapshot` files carry the file-level hash (`files.hash`, derived
  from the server manifest by `client/grpc.toServerFile` as `chunker.FileHash` of the
  chunk hashes) plus the `Chunks` hash list decoded from `files.chunks`; content
  itself stays in the loose object store.

## Proto / gRPC

- Source is `internal/grpc/proto/server.proto`; service `NipaService` with
  `LoginWithUsernamePassword`, `LoginWithRefreshToken`; branch RPCs
  `GetListBranch`, `GetBranch`, `GetBranchByName`, `GetDefaultBranch`,
  `CreateBranch`, `RenameBranch`, `DeleteBranch`, `SetDefaultBranch`,
  `SetBranchProtection`; tag RPCs `ListTags`, `GetTagByName`, `CreateTag`,
  `DeleteTag`; history/tree RPCs `GetTreeManifest`, `GetCommitLog`,
  `GetCommit`, `WalkCommits`, `GetMergeBase`, `MergeFastForward`; merge-request
  RPCs `CreateMergeRequest`, `UpdateMergeRequest`, `ListMergeRequests`,
  `GetMergeRequest`, `MergeMergeRequest`, `CloseMergeRequest`,
  `ReopenMergeRequest`, `CheckMergeRequest`, `ListMergeRequestCommits`,
  `GetMergeRequestDiff`; review RPCs
  `SubmitMergeRequestReview`, `ListMergeRequestReviews`,
  `GetMergeRequestReviewState`, `WithdrawMergeRequestReview`,
  `DismissMergeRequestReview`, `ListMergeRequestThreads`,
  `AddMergeRequestComment`, `ReplyMergeRequestThread`,
  `UpdateMergeRequestComment`, `DeleteMergeRequestComment`,
  `ResolveMergeRequestThread`, `DeleteMergeRequestThread`,
  `ListMergeRequestReviewRequests`, `RequestMergeRequestReview`,
  `RemoveMergeRequestReviewRequest`, `ListMergeRequestTimeline`; transfer RPCs `Push`,
  `GetChunkUploadUrls`, `GetChunkDownloadUrls`, `ConfirmChunkUploads`; file-lock
  RPCs `LockFile`, `UnlockFile`, `ListFileLocks`; browsing RPCs
  `ListMyProjects` (readable projects of the caller's orgs) and
  `ListDirectory` (one directory of a branch or commit, via `Branch.TreeAt`);
  PBAC
  RPCs `GetMyPermissions`, `CreatePBACRule`, `ListPBACRules`, `DeletePBACRule`,
  `ListProjectPathPermissions`, `SetProjectPathPermission`,
  `DeleteProjectPathPermission`; group RPCs `CreateGroup`, `ListGroups`,
  `AddGroupMember`, `RemoveGroupMember`.
  `rpc` `GetTreeManifest` takes
  `{context{org,project}, branch, path, tree_hash?, recursive, paths,
  commit_id?}` (`paths` = directory prefixes to include, empty = whole tree;
  when `commit_id` is set the manifest is built from that commit's tree — the
  commit must belong to the project, and `tree_hash` is then ignored) and
  returns
  `{branch, root_tree}`. `TreeManifest` carries `tree_hash`, `path`, `sub_trees`,
  `files` (`path, mode, size_bytes, is_binary, chunk_hashes`). `GetBranch` takes
  `branch_id` (base36) and `GetBranchByName` takes `name`; both return the branch
  with its head `commit_id`. `CreateBranch` takes `from_branch` and/or
  `from_commit_id`/`from_commit_hash` (exact fork point, base36/hex).
  `GetCommitLog` takes `{context, branch, start_commit_id?, limit, path?}`
  (`path` switches to `Branch.PathCommitLog`: only commits that changed that
  file or folder, scan capped at 500 commits); `ListTags` defaults a missing
  limit to 100.
  `GetCommit` takes
  `{context, commit_id}` (base36; **ID only**, no hash alias) and returns the
  commit detail plus its recursive tree manifest; `WalkCommits` takes
  `{context, start_commit_id, stop_commit_id?, limit}` and returns
  `CommitWalkEntry`s newest-first, both-parents, stop-exclusive. `GetMergeBase`
  takes branch names and/or optional `target_commit_id`/`source_commit_id`
  (base36, take precedence) so a merge base can be resolved for commit pairs.
  All are project-scoped in the usecase because `CommitGet`/`CommitGetByHash`
  have no project filter, and commit IDs are server-local (snow) identifiers.
- **Chunk transfer is presigned HTTP, not gRPC streaming.** `GetChunkUploadUrls`
  (project write; refs carry `hash` + exact `size_bytes`) and
  `GetChunkDownloadUrls` (`commit_ids` + optional `paths` prefixes + `hashes`;
  the visible set is the union of the referenced commit trees narrowed by the
  prefixes — `Branch.VisibleChunks(projectID, commitIDs, paths)` — and requested
  hashes are filtered against it first) return one
  page of signed paths built by `internal/chunkurl` (`/api/chunks/{org}/{project}/{hash}
  ?op=put|get&size=&exp=&sig=`, HMAC-SHA256 with `CHUNK_URL_SIGNING_KEY`,
  constant-time verify, TTL `CHUNK_PRESIGN_TTL_SECONDS` default 3600s) — or
  absolute backend presigned URLs when the store implements
  `storage.DirectTransferStore` (S3). The
  client then PUTs/GETs bytes directly over HTTP through an adaptive transfer
  limiter (`internal/client/grpc/transfer.go`): concurrency starts at 16, is
  auto-tuned within [4, 64] from observed throughput, and can be pinned with
  `NIPA_UPLOAD_WORKERS` or `uploadWorkers` in `~/.config/nipa/config.json`
  (pinned disables tuning). `ConfirmChunkUploads` measures the stored content
  size server-side (never a client-supplied value) and writes the chunk
  metadata rows, so `Push` can rely on them; linked chunks without a proof take
  their size from the row in one batched `LinkedProjectChunkSizes` lookup
  (presign checks every ref with 16 concurrent `Exists` calls, so a lost
  object is uploaded again); confirmations are
  serialized client-side under a mutex so concurrent upload windows never stack
  SQLite metadata writes. Chunks the project is already linked to come back with `already_stored` so
  clients skip the transfer. A project may reference a chunk only after it proved it
  holds the bytes (`internal/chunkproof`): `GetChunkUploadUrls` hands every
  chunk the project is not linked to an HMAC-signed, project- and hash-bound
  `proof_challenge`, the client answers with `BLAKE3(challenge || bytes)` in
  `ConfirmChunkUploads.proofs`, and the server reads the stored object back,
  checks it against its hash and the proof, and links the project in one batch
  (`project_chunk_uploads`). The client claims with proofs first, so content the
  server already holds for another project is linked without being sent again;
  only chunks reported missing are uploaded and then confirmed with their proofs.
  Unproven hashes are reported missing, `ApplyPush` joins through the link table,
  and server-generated merge content is linked via `AttributeProjectChunks`.
  `Push` sends `base_commit_id` (base36; preferred over
  `base_tree_hash`, so sparse/permission-filtered clones push deltas against the
  full server tree) and `parent_2_commit_hash` for merge commits.
  `Push.pushStaged` scans into 16MB windows handed to
  two uploader goroutines (queue depth 2), so hashing overlaps with the network;
  window chunks are cached locally before upload (text and pinned raw files
  only) and the scanner only blocks
  when the bounded queue is full. Chunk sizes are content-aware
  (`internal/chunker/profile.go`): text and in-place-edited formats (`.blend`,
  `.psd`, `.mb`, `.sqlite`, ...) keep the 64KB default, packed assets (textures,
  audio, video, models, archives) use 4MB average chunks, unknown binaries 1MB;
  the server accepts refs up to `chunker.MaxChunkSize()` (16MB). Text is
  zstd-compressed per file (`chunker.Encode`): files up to 8MB raw become one
  compressed chunk (≤4MB), larger text falls back to CDC chunks of the plaintext
  (256KB avg) compressed individually. Every file carries an `encoding` column
  (`internal/chunker.EncodingRaw`/`EncodingZstd`, proto `FileNode.encoding`,
  `files.encoding` on both server and localrepo); hashes always refer to the
  stored bytes, and every client hashing path (push, add/status, diff,
  merge/revert materialization, guarded remove) must go through
  `chunker.Encode`/`chunker.ConfigForFile`, passing the tracked encoding for
  stored files (`storedEncoding`), so file hashes stay consistent; decode at
  assembly points with `chunker.Decode` (client materialization,
  `diff.LoadContent`, merge's `loadFileContent`). The signed routes live in a `/api/chunks` webService (go-restful
  `os.Exit`s on duplicate root paths so it cannot share `/api/v1`) and are NOT
  behind the Bearer filter — the signature is the capability. HTTP PUT pins the
  exact size (Content-Length check + `MaxBytesReader` + BLAKE3 verify) and only
  writes content (`Chunk.StoreUploaded`, no DB writes, avoiding SQLite lock
  contention from concurrent PUTs); DB metadata and project links are written at
  confirm time. `ApplyPush` (both repositories) resolves every chunk a push
  references in one project-scoped lookup before it writes anything (a chunk the
  project is not linked to fails with `missing chunk data for <hash>`), then
  inserts the files and their chunk links in bulk through
  `internal/repository/pushbatch`: one JSON document per batch of 5000 files or
  20000 links, expanded by `json_each` on sqlite and `jsonb_array_elements` on
  postgres, with ids matched back by `(tree_id, name)`. A 10k-file push is a
  handful of statements instead of ~30k round trips.
  `internal/client/grpc/chunks.go`'s `UploadChunks`/`DownloadChunks` take the
  `ChunkScope{Org, Project, CommitIDs, Paths}` that scopes download visibility;
  download callbacks are
  serialized under a mutex because transfers run concurrently (the client
  usecase's `downloadChunks` hands each delivered chunk to a worker pool that
  verifies its hash and passes it to a sink: `StoreChunks`, a direct
  `WriteAt` into working-file temp files, or both). Downloads request
  signed URLs in windows of 1000 hashes (the server's default page size) and
  prefetch the next window while the current one transfers, so URLs never wait
  behind a long clone past their TTL and the worker pool is fed continuously
  (`runChunks`). A transient per-chunk failure (network error, truncated body,
  5xx, 408, 429) is retried up to 3 attempts with a 250 ms doubling backoff plus
  jitter (uploads too: PUTs are content-addressed and idempotent); a 429 has its
  own budget (10 retries honouring `Retry-After`, capped at 30 s) and halves the
  transfer concurrency, and unary RPCs rejected by the server rate limiter are
  repeated the same way (`internal/client/grpc/retry.go`); 4xx answers (expired or unknown URL) fail immediately, and a chunk
  reaches the callback only after a complete read, so a retry never delivers it
  twice. The server only
  returns relative paths: the client connects with the address from
  `domain.NipaUrl.Origin` (`scheme://host[:port]`; a bare host keeps cleartext
  h2c, `https://` dials TLS with system roots) and `Client.httpURL` prefixes
  `http://` when the target has no scheme. Tokens are still keyed by the bare
  host (`domain.HostForAddress`); `Auth`/`Session` normalize any address they
  receive so the transport may carry a scheme while `securestorage` never does.
- **Chunk content backends**: `internal/storage.ChunkStore` (Put/Get/Size/
  Exists/Close, content-addressed by BLAKE3, idempotent Put) is the single seam.
  `storage.NewLocalStore` (`CHUNK_STORAGE=local`, `CHUNK_STORAGE_DIR`) is the
  free `cmd/nipad`'s only backend — `CHUNK_STORAGE=s3` there fails with a hint
  to use `nipad-ee`. The enterprise `ee/cmd/nipad` is S3-only: empty or `s3`
  selects `ee/storage/s3` (`CHUNK_S3_*`), anything else is a startup error. The
  S3 store keys objects `<prefix>/<hash[:2]>/<hash[2:]>` and probes the bucket
  at startup. `internal/` must never import `ee/` — only `ee/cmd/*` does (the
  free `cmd/nipad` links no ee package). A store implementing
  `storage.DirectTransferStore` (the S3 store does) hands clients absolute
  presigned backend URLs: downloads via `PresignedGetObject`, uploads via a
  POST policy whose `content-length-range` pins the exact declared chunk size
  (S3 rejects oversized bodies with EntityTooLarge); `usecase.Chunk`
  type-switches on the capability, `PresignedChunkUrl` carries
  `method`/`form_data`, and the client POSTs multipart with the file part last
  (`Client.httpURL` passes absolute URLs through). Direct uploads are
  presigned PUTs (not POST policies, which Cloudflare R2 does not implement) to
  the shared key, signed with `If-None-Match: *` and the exact `Content-Length`:
  the backend refuses (412) a write when the object exists, even when a target is
  replayed, so a stored chunk can never be overwritten and one project cannot
  corrupt content another relies on. The client sends the target's `headers`
  and treats a 412 as "stored meanwhile". A chunk that already exists gets no
  upload target at all (`url` empty, only a `proof_challenge`): the project
  claims it with the possession proof, which costs no write. Because direct
  writes bypass the server, `ConfirmChunkUploads` hashes whatever it reads from
  the shared key (for chunks the project is not linked to yet and for linked
  ones the client supplies a proof for): an object that does not match its hash is deleted and
  reported missing, and the client presigns and uploads that chunk again (up to
  three attempts). The side effect is that a project writer who knows a hash
  learns that its content exists (no target is issued). Backends must support
  conditional create on presigned PUT (AWS S3, MinIO, R2);
  `TestIntegration_S3CreateOnlyUploads` verifies it against a real backend when
  the `NIPA_S3_TEST_*` variables are set (R2: endpoint
  `https://<account>.r2.cloudflarestorage.com`, region `auto`).
- **Storage accounting seam (free vs cloud).** `usecase.Chunk.WithStorageLedger`
  accepts an optional `StorageLedger` (`internal/usecase/storage_ledger.go`:
  `AttributedSizes`/`Headroom`/`Attribute`). The free version wires none (nil):
  presign/confirm skip all accounting and no quota applies. Cloud builds inject
  an `ee/` implementation at `ee/cmd/nipad` (same import pattern as `ee/storage/s3`);
  it links chunks to projects, keeps per-project/org/owner counters and enforces
  the owner's quota at both `GetChunkUploadUrls` (projected new bytes) and
  `ConfirmChunkUploads` (authoritative). Quota failures use
  `domain.NewErrorQuotaExceeded` (HTTP 402) and cross gRPC as
  `ResourceExhausted`, which the client maps back to a 402 domain error so
  `nipa push` reports it. The cloud implementation (ee tables, per-user quota,
  purchase, recount) is planned in `docs/CLOUD_STORAGE_QUOTA.md`.
- `Parsec`/`ParseNipaUrl` (`internal/client/domain/url.go`): `/org/project[/path]`.
  `path` is threaded through to the manifest request so a missing subpath returns a
  404, and cloning a repo with an empty (no-commit) branch returns an empty tree,
  not an error (branch root only).

## SonarQube / CI

- CI runs `go build ./...`, `go vet ./...`, `go test -race -coverprofile=...
  -coverpkg=./...` (cross-package coverage, so the REST/e2e integration tests
  count towards the handler/usecase packages they exercise).
- `sonar-project.properties` excludes generated code
  (`internal/repository/sqlc/**`, `ee/repository/postgres/sqlc/**`), swagger,
  and cmd mains from analysis; coverage expects `coverage.out`.
- Avoid introducing SonarQube issues: no DELETE without WHERE, keep coverage in new
  packages high (localrepo aims ~90%+).

## Web UI

- Routes/API map lives in `docs/WEB_UI.md`. SPA uses `react-router-dom` v6,
  memory-only access token + `HttpOnly` refresh cookie, and a capability mask
  from `/permissions/me` to hide admin controls (server remains authoritative).
  The SPA root `/` is an entry redirect (`anonymous → /login`, signed in →
  `/dashboard`, the app home; `APP_HOME` in `web/src/routes.ts`). The SaaS
  marketing site is a separate Astro deployment (the `nipa-web` repo on
  Netlify) served at `https://nipalab.com`; `deploy/caddy/Caddyfile`
  mirrors its pages and assets on the app domain with the `@marketing` path
  matcher (`/`, `/pricing`, `/download`, `/support`, `/contact`, `/privacy`,
  `/terms`, `/guides*`, `/_astro/*`, `/screenshots/*`, root assets;
  `MARKETING_UPSTREAM` required, the Netlify origin, sent its own Host).
  `/docs` stays the REST API docs. Marketing links are root-relative
  (`siteUrl`/`appUrl` in nipa-web `src/consts.ts`), so
  marketing changes ship without rebuilding `nipad-ee`; `upgrade.sh` only
  rewrites the `reverse_proxy 127.0.0.1` line. The optional embedded fallback
  (`ee/web/marketing`, base `/web`, placeholder committed,
  `serverapp.WithMarketingUI` only when `SAAS_MODE=1`) serves the same paths
  when the Caddy proxy is not used; `make marketing-dist
  [MARKETING_DIR=../nipa-web]` builds with `MARKETING_BASE=/web/` and syncs the
  result into it. Organization/project slugs reject the paths owned by the
  router, marketing and fixed endpoints (`internal/usecase/reserved.go`,
  mirrored in `web/src/slug.ts`; a test checks every `@marketing` path is
  reserved, and `npm run routes` in nipa-web checks the Caddyfile covers the
  build).
  Pages cover repository/org lists, tree browser, blob viewer, commit history
  + diffs, branch management, merge requests (list/create; detail tabs for
  overview, commits and file changes; merge/reviews with decisions, inline
  floating threads, reviewer requests, activity timeline),
  file locks (list/lock/unlock), project/org settings (protection + ACL,
  members/groups), user administration
  and profile; the browse endpoints are served by `internal/usecase/browser.go`
  (+ `browser_history.go` for per-file/per-dir last-commit info) under
  `internal/http/api/`. Release tags have REST routes (`/tags`,
  `/tags/{name}`) and `endpoints.ts` helpers; the SPA has a Tags page (list, create,
  delete, ref-picker tab).
- Billing (`docs/BILLING.md`): `BillingPage` + `components/BillingPlan.tsx`
  show the plan card and editor only when `GET /auth/capabilities` carries
  `billing`; `web/src/billing/paddle.ts` injects Paddle.js v2 from
  `cdn.paddle.com` on demand (once per page, `Environment.set("sandbox")`
  before `Initialize`, events fanned out through `subscribePaddleEvents`,
  `previewPrices` over `PricePreview`) and is mocked in tests. Checkout opens
  `Paddle.Checkout.open({ transactionId })` from `createCheckout`, never with
  client-side price ids. `FormDialog` renders a seat-limit 402 as
  `SeatLimitBanner` (link to `/settings/billing`). The app has no CSP; adding
  one must allow Paddle.js and its checkout frames.
- Keep `web/src/api/endpoints.ts` in sync with the REST handlers under
  `internal/http/`. `npm run lint` (tsc), `npm test` (vitest) and `make web`
  must stay green; the built SPA is embedded via `web/server/webui.go`.

## Things to remember

- Big (10k+ files) manifests are expected on day one — never plan an O(entire repo)
  DB write for an update path; default to diffing against the stored rows and
  writing only the difference.
- Keep the free/enterprise boundary: `internal/` never imports `ee/`, the free
  `cmd/nipad` links no ee package (sqlite + local chunks only), and postgres/S3
  are wired exclusively from `ee/cmd/*`; see `docs/POSTGRES.md`.
- Revert is forward-only: one commit per target, history is never rewritten, and
  pending `revert_state` is cleared only on success/abort. It reuses existing
  queries and added no SQL, sqlc, or schema changes.
- Do not commit secrets, config.yaml (gitignored, sample is `config.yaml.sample`).
- Added/edited tests should run under `go test ./...` with `go vet ./...` clean.
- If you change `opencode.json`, agents, or skills, tell the user to restart
  opencode after saving.
- Don't put too much comment on the code. Avoid to add comments as much as possible