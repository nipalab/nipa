# Nipa Desktop

Design for the cross-platform desktop client (macOS, Windows, Linux) aimed at
game teams: artists, designers, audio, programmers and build engineers working
on binary-heavy projects.

## Decisions

- **Framework: Wails v3 (Go) + React/TypeScript.** Visual asset diffs are the core
  feature, and the web platform already provides what they need: WebGL2 for
  image compare modes and float textures, three.js for glTF/OBJ/FBX/USDZ,
  WebAudio for waveforms and playback. The Go side talks to `nipa serve`
  (`docs/DAEMON.md`) with the generated `daemonpb` client and reuses Go
  decoders. GPUI (Rust) was rejected: every viewer would be written from
  scratch on a pre-1.0 API in a second language. Tauri was rejected because it
  adds Rust while the reusable code is Go.
- **Decode in Go, render in the webview.** Formats the webview cannot open
  (TGA, DDS, KTX2, EXR, PSD, OGG/FLAC on WebKit) are decoded by
  `internal/preview` into normalized data (RGBA, float16, audio peaks), so the
  UI never depends on the webview's codec set.
- **Layout: `desktop/` in this repo as a nested Go module**
  (`github.com/nipalab/nipa/desktop`, `replace` to `../`). It can import
  `internal/...`, proto + daemon + UI changes land together, and Wails' cgo /
  WebKitGTK dependencies stay out of `go test ./...` and the server builds.
- **UI stack:** React 18, Vite, Tailwind + shadcn/ui, lucide icons, TanStack
  Query/Virtual, CodeMirror 6, three.js.

## The app (`desktop/`)

Wails v3.1.0 project, nested Go module `github.com/nipalab/nipa/desktop`
(`replace github.com/nipalab/nipa => ../`), so root `go test ./...`, vet and
Sonar never compile it.

- `main.go` wires the bound service, the Wails notifications service, the
  window, the tray and the single-instance lock (binary `nipa-desktop`; it
  must not be called `nipa`, the app looks for the CLI next to itself).
  Closing the window hides it while "Keep in tray" is on (the default) and
  quits otherwise; `--background` (passed by the login item) starts hidden.
  A second launch focuses the first one and hands it its `nipa://` argument.
- `internal/tray`: pure tooltip and menu model from every clone's activity
  ("game · 3 new commits · 1 review", Open Nipa, Settings…, Quit Nipa); the
  tray icon is `build/trayicon.png`.
- `internal/autostart`: the login item (XDG `autostart/nipa-desktop.desktop`,
  macOS LaunchAgent `com.nipalab.nipa`, Windows `HKCU\…\Run`), started with
  `--background`.
- `internal/connector`: reads `daemon.json` (`daemon.ReadEndpoint`), checks the
  pid, dials loopback gRPC with the token as per-RPC credentials and `Ping`s.
  When no live daemon answers it starts `nipa serve` detached (own session /
  process group, output in the user cache dir `nipa/daemon.log`) and polls
  until it answers (10 s). The CLI is resolved from `NIPA_BIN`, next to the
  app binary, then `PATH`. The daemon outlives the app on purpose: Explorer
  and engine plugins share it.
- `internal/workspace`: the opened clones and the app settings in
  `<config dir>/nipa/desktop.json` (atomic write, 0600). The daemon only keeps watched roots in memory, so the
  app re-watches them on start; a clone that fails to watch stays listed with
  its error.
- `internal/service`: the Wails-bound API (`Connect`, `Repos`, `PickRepo`,
  `AddRepo`, `RemoveRepo`; Files: `Files`, `FileInfo`, `FileView`,
  `RestoreRevision`, `RevealFile`, `FileLink`, `AssetRefs`, `Provenance`,
  `SetProvenance`; Changes screen: `Changes`, `Stage`, `Unstage`,
  `Discard`, `FileDiff`, `ImageDiff`, `ModelDiff`, `AudioDiff`, `VideoDiff`,
  `OpenMedia`, `HasMedia`, `ReleaseMedia`, `FontDiff`, `TableDiff`, `SceneDiff`, `MetaWarnings`, `PushChecks`, `GenerateCommitMessage`, `Push`, `Update`,
  `Shelves`, `Shelve`, `ApplyShelf`, `AbortShelfApply`, `DropShelf`, `ShelfFiles`, `SyncPlan`; Details: `DiskUsage`, `CleanUp`; Locks: `LockStatus`, `Lock`, `Unlock`,
  `PickFile`, `OpenFile`; People: `Members`, `AssignMergeRequest`, `RequestReview`, `ProjectActivity`; Clone: `AccountStatus`, `SignIn`, `SignInSSH`,
  `ListProjects`, `RemoteBranches`, `RemoteTree`, `ChooseFolder`, `Clone`,
  `SetSparse`; History: `History`, `CommitFiles`, `Branches`, `CreateBranch`,
  `DeleteBranch`, `SwitchBranch`, `Tags`, `CreateTag`, `DeleteTag`,
  `CheckoutTag`, `Revert`; Merge requests: `MergeRequests`, `MergeRequest`,
  `MergeRequestActivity`, `MergeRequestCommits`, `RevisionFiles`,
  `CreateMergeRequest`, `GenerateMergeRequest`, `EditMergeRequest`, `SetDraft`, `MergeMergeRequest`,
  `CloseMergeRequest`, `ReopenMergeRequest`, `SubmitReview`, `Comment`,
  `Reply`, `ResolveThread`; Conflicts: `Merge`, `AbortMerge`, `Conflicts`,
  `ConflictTexts`, `ConflictDiff`, `ResolveConflict`; Activity and links:
  `Activity`, `OpenLink`, `Navigate`, `TakeNavigation`; Settings:
  `Settings`, `SaveSettings`, `UploadWorkers`, `SetUploadWorkers`,
  `RestartDaemon`, `ChooseProgram`, `IgnoreRules`, `SaveIgnoreRules`,
  `LLMSettings`, `SetLLM`, `SetLLMKey`, `ClearLLMKey`, `TestLLM`; Updates:
  `UpdateStatus`, `CheckForUpdate`, `InstallUpdate`, `RestartToUpdate`,
  `OpenUpdatePage`). Plain Go behind `Dialer`/
  `RepoStore`/`FolderPicker` seams, so it is unit-tested without a webview; an
  `Unavailable` error drops the session so the next call reconnects (and
  restarts the daemon) and re-watches each clone on first use. Every watched
  clone gets a follower on the daemon's `WatchChanges` stream, relayed as the
  `repo:changed` event; `Push`/`Update` relay `OpEvent`s as `op:progress` and
  stop the daemon operation when the frontend cancels the call. Events are
  registered in `main.go`, so the bindings carry their types.
- Changes screen (`frontend/src/components/changes`): staged and unstaged
  groups in a virtualized list (checkbox = stage, discard with confirm,
  arrow-key navigation), push message + Push with progress/cancel, Update in
  the header. `FileDiff` turns one `DiffFiles` entry into the viewer model
  (line kinds `added`/`removed`/`context`); an untracked text file is read
  with `ReadBlob` and shown as a full addition. `DiffViewer.viewerFor` is the
  viewer registry: text diffs (unified/split, virtualized, per-line
  highlight.js), notices (too large, mode-only, empty), the image viewer and
  a metadata card for other binaries. The list refreshes on `repo:changed`,
  window focus and a 30 s poll.
- Image viewer (`ImageDiff.tsx`, `src/lib/imageCompare.ts`): `ImageDiff`
  calls the daemon's `Preview` (snapshot before, working file after; previews
  capped at 2048 px) and returns the PNGs as data URLs plus the pixel-diff
  stats; a 415 from the daemon (format without a decoder yet, too large)
  becomes `unsupported` and the metadata card. The frontend decodes the PNGs
  through a canvas and renders side by side, swipe, onion skin, a difference
  heatmap (client-side with a tolerance slider, changed region from the
  daemon's stats) and blink, with R/G/B/A channel isolation, synced zoom/pan
  (wheel, drag, 1:1, fit; fit enlarges small sprites up to 800% with
  pixelated rendering) and a before/after metadata table. Canvas 2D is enough
  at the preview size, so there is no WebGL path; the mode is remembered in
  `nipa.imageMode`.
- Texture and layer controls: when either side has mips, faces or float
  data a second toolbar offers a mip picker (sizes from the level-0
  dimensions), a face picker (`+X…−Z`, `Layer n` for arrays) and, for HDR
  sides, exposure (±8 EV) and a tone curve (clamp, Reinhard, ACES) applied
  client-side by `src/lib/hdr.ts` after unpacking the RGBE preview. Picking a
  surface refetches `ImageDiff` with `ImageOptions{Level, Face}` while the
  previous preview stays on screen; the choice resets per file. PSD/PSB
  sides list their layers (`src/lib/layers.ts` pairs them by kind, name and
  duplicate index) with added/removed/changed status and what changed
  (pixels, bounds, visibility, opacity, blend mode, nesting).
- Model viewer (`ModelDiff.tsx`, lazily loaded with three.js 0.186):
  `ModelDiff` in the service reads each side through `ReadBlob` (snapshot
  before, working file after, or a revision) plus the files it references —
  glTF buffers and images, OBJ `mtllib` libraries and their `map_*` textures —
  resolved next to the model and refused when they leave the repository;
  absent files are listed as `missing`. Stats come from
  `preview.DecodeModel`; the model is read up to 256 MiB and returned
  base64 only while it and its files fit 64 MiB (`tooLarge` otherwise, stats
  still shown). `src/lib/modelLoad.ts` parses glTF/GLB, OBJ+MTL, FBX and
  USD/USDA/USDC/USDZ from memory (a `LoadingManager` URL modifier serves the
  referenced files as data URLs) and counts the scene itself for formats
  without Go stats. `src/lib/modelStage.ts` drives one WebGL renderer with a
  shared camera and `OrbitControls`: side by side (two scissored viewports)
  or overlay (the old model as an orange wireframe on top of the new one),
  shaded/wireframe/both, synced animation playback by clip name, camera fit
  to the union of both bounds. The panel diffs vertices, triangles, meshes,
  materials, textures, bones, animations, morph targets, bounds and the
  exporter, and lists added/removed material and animation names.
- Audio viewer (`AudioDiff.tsx`): `AudioDiff` in the service reads each side
  (up to 256 MiB) and runs `preview.DecodeAudio` in the app process, so the
  webview never needs OGG/FLAC/MP3 codecs: per-channel min/max waveform
  peaks, a log-frequency spectrogram, stats and a 16-bit WAV copy for
  playback (at most 48 MiB: stereo, then mono, then cut short). Both sides
  stack on one time axis (shorter files draw shorter) with a shared playhead;
  click to seek. `src/lib/audio.ts` plays both versions through WebAudio in
  lockstep and only changes which one is audible (A/B), so switching never
  loses the position. The panel diffs duration, rate, channels, encoding,
  sample peak, RMS and integrated loudness (LUFS) and flags clipping,
  truncated analysis and shortened playback.
- Video viewer (`VideoDiff.tsx`): `VideoDiff` in the service streams each
  side (up to 4 GiB) into a private temp folder (`media.go`, removed on
  shutdown) and probes the container in Go with
  `preview.ProbeVideo`. The webview plays it from
  `/nipa-media/<token>/<name>`, served by `service.MediaMiddleware` on the
  Wails asset server with `http.ServeContent` (range requests, never a data
  URL). Both versions sit side by side with one Play/Pause, a shared seek
  bar, previous/next frame (from the frame rate, 30 fps otherwise) and A/B
  audio (the other side is muted); the follower is re-synced when it drifts.
  A file is held from the moment `VideoDiff` returns it until the viewer
  closes and calls `ReleaseMedia(tokens)` (streaming a released file holds
  it again); only released files are evicted, least recently used first,
  beyond 4, and 32 unreleased files bound requests that outlived their view.
  The video answer is not cached past the viewer's lifetime. As a backstop,
  a playback error asks `HasMedia(token)` and reloads the comparison once
  when the file is gone instead of blaming the format.
  A video the webview cannot decode (codec, AVI, too large) keeps its stats
  and offers "Open externally" (`OpenMedia(token)`, the default player on
  the temp copy). The panel diffs container, duration, resolution, codecs,
  frame rate, audio and bitrate.
- Font viewer (`FontDiff.tsx`): `FontDiff` decodes each side (32 MiB) with
  `preview.DecodeFont` and renders `preview.RenderFontSheet` (sample line +
  the first 256 covered glyphs, black on transparent, inverted in dark
  mode) as a PNG data URL. With both sides it lists added and removed code
  point ranges (`preview.CompareFonts`, sample characters per range, 400
  ranges at most) and the names/metrics that changed; the details table
  shows family, style, version, glyphs, characters, units per em, ascent,
  descent, line gap, cap height and x-height, plus coverage per script.
- Embedded thumbnails: `.blend`, `.uasset` and `.umap` go through the image
  viewer (`viewerFor` with `lib/files.ts` `hasEmbeddedThumbnail`, also used
  by the Files grid). The daemon `Preview` extracts the thumbnail
  (`preview.DecodeThumbnail`, first 64 MiB of the file) and marks the side
  `thumbnail`, which the viewer states with a "Thumbnail only" note; files
  without one fall back to the metadata view with the reason.
- Table viewer (`TableDiff.tsx`): `TableDiff` in the service parses both
  sides with `preview.ParseTable` (16 MiB each) and returns
  `preview.DiffTables`: columns matched by header name, rows matched by the
  first shared column when its values are unique (an id or localization
  key), otherwise by content. Changed cells show old and new value, added
  and removed rows/columns are tinted, moved rows marked; long tables start
  on "Only changed rows". A filter bar narrows rows by text (new and old
  values, all columns or one picked column), hides columns without changes
  ("Changed columns only"), and steps through changes (previous/next with
  "n of m changes", wrapping). The row gutter, the header and the key column
  (moved first) stay put while scrolling; each header shows its changed-cell
  count (`lib/table.ts`). `.xlsx` workbooks go through `preview.ParseSheets`:
  the service returns the selected sheet (`TableDiff`'s `sheet` argument,
  default and fallback the first changed sheet) plus `sheets[]` with a status
  and counts per sheet, rendered as sheet tabs with a changed marker. A
  switch shows the plain text diff instead.
- Scene viewer (`SceneDiff.tsx`): `SceneDiff` in the service parses both
  sides of a Unity Force Text scene, prefab or asset (`.unity .prefab .asset
  .mat .anim .controller .overrideController .physicMaterial`, 16 MiB each)
  with `preview.ParseUnityYAML` and returns `preview.DiffScenes`: objects
  matched by fileID, a collapsible GameObject/component tree with
  added/removed/changed/moved badges and `old → new` property rows, plus the
  dependency paths that appeared or disappeared. GUIDs resolve to asset paths
  through an index of the working copy's `Assets/` and `Packages/` `.meta`
  files (per clone and Unity project folder, dropped when a `repo:changed`
  notice touches a `.meta`); revision sides use the same index and an
  unknown GUID shows as `guid:<guid>`. Binary-serialized assets are refused
  with a "set Asset Serialization to Force Text" hint. Godot text
  resources (`.tscn .tres`, plus `project.godot` and `.import` as plain
  sections) go through `preview.ParseGodotResource` instead and need no
  index: nodes are matched by their path below the root (resource ids
  renumber on save), `ExtResource`/`SubResource` values show the resource
  path or `sub:<type>#<id>`, and connections appear as
  `signal from → to.method` entries. Binary `.scn`/`.res` stay opaque. A
  Structure/Text switch shows the line diff (with comments) instead.
- Unity `.meta` checks: `MetaWarnings` runs `unitymeta.Lint` over the
  status of every Unity project in the clone (an `Assets/` folder next to
  `ProjectSettings/`) and the Changes screen lists the warnings above the
  file list, marks affected rows and jumps to the file on click: assets and
  new folders without a `.meta`, `.meta` files left without their asset or
  deleted while the asset stays, a `.meta` not staged with its staged asset,
  and a changed GUID (old content read from the snapshot, 256 checks max).
- Pre-push checks: `PushChecks` calls the daemon's `PushPlan` (the
  `nipa push --dry-run` report) and the Changes screen shows a collapsible
  "Checks" block above the Push button (`usePushChecks`: query keyed on the
  staged set, debounced 400 ms, refreshed on `repo:changed`). "Over budget"
  rows name the file, the `.nipaassets.yml` limit and rule with the message;
  "Broken references / No longer used" rows name the referring file and the
  reason/target, or the asset nothing uses any more; a truncated reference
  scan and skipped checks are noted. Clicking a row selects that file (or the
  broken target) in the changes list. Warn only: the button stays enabled and
  reads "Push anyway" while anything is reported. The block is hidden with
  nothing staged or nothing to report (no budget file and no engine asset
  change gives an empty report).
- Files tab (first tab; Changes stays the one selected on open): a project
  browser over the daemon's `ListFiles`. Left, a lazy folder tree (a
  folder's children are listed the first time it is expanded); middle, the
  folder as a sortable list (name, kind, size, status, lock; folders first)
  or a thumbnail grid (the choice is kept in `localStorage`
  `nipa.files.view`), both virtualized with TanStack Virtual so a 10k-file
  folder renders a screenful of rows; right, the selected file. Entries carry
  the working-copy status (modified, added, deleted, missing, untracked,
  conflict) and folders a dot when something inside changed; untracked files
  and new folders that only exist on disk are listed too. Lock state is
  joined in the frontend from the `LockStatus` list the app already polls
  (`lockFor`: the most specific in-scope lock covering the path), so listing
  a folder never calls the server. Thumbnails are 128 px `ImageDiff` previews
  of the working file (new side only), requested only for rendered grid
  cells and cached by the snapshot hash (or the working file's stamp for
  changed files). The detail pane reuses the change viewers with only the new
  side (`FileView` reads the working file like an untracked change; text
  shows as plain lines), then size, kind, status, lock owner, the last change
  (`FileInfo`: snapshot metadata plus the last commit through
  `ProxyCommitLog` with `path` and limit 1; offline it only says "Not
  available offline") and the file's versions (20 newest, image thumbnails of
  each revision). Toolbar and right-click menu: Open, Reveal in file manager
  (`RevealFile`: `open -R` on macOS, `explorer /select,` on Windows, the
  FileManager1 D-Bus `ShowItems` call on Linux with `xdg-open` of the folder
  as the fallback), Lock / Unlock, History (the History tab filtered by the
  path), Copy path and Copy `nipa://` link (`FileLink`). "Get this version"
  on a version runs the daemon's `RestoreRevision` through the operation
  progress like the other operations: the revision is downloaded
  commit-scoped and written over the working file (a read-only file stays
  read-only), nothing is staged and the file then shows as modified; when the
  file already has local changes a confirmation comes first. In a sparse
  clone, folders outside the checkout are listed greyed out from
  `RemoteTree` (only for folders the sparse set does not cover, so a full
  checkout makes no remote call) with "Download this folder", which adds the
  prefix through `SetSparse`. `repo:changed` refreshes only the listings of
  the folders above the changed paths (everything without paths).
- Where used and origin (`components/files/AssetInsight.tsx`): the Files
  detail pane, and a collapsible "References & origin" footer under the
  Changes diff viewer (`InsightPanel`, closed by default, the choice kept in
  `localStorage` `nipa.insightPanel`, its header summarizing "Used by N ·
  Uses N · Origin: status"), show two sections for the selected file only.
  "References" (`AssetRefs`, the daemon's cached reference graph) lists "Used
  by" and "Uses", each row opening that file in the Files tab, "Not
  referenced anywhere" when nothing points at the asset, and "Unresolved"
  references (not found in the clone, or outside a sparse checkout), with a
  note when the scan was truncated or the clone is sparse. "Origin"
  (`Provenance`) appears when the file has a `.nipaprovenance.yml` record or
  a `require_provenance` rule covers it: tool, model, seed, source, recorded
  date, license and note, with the status in plain words (`modified` =
  changed since recorded and not marked as edited by a person, `edited` =
  edited by a person after generation). A required but missing record shows
  a warning and "Add record…", a form (tool required, whole-number seed,
  prompt, "edited by a person") saved through `SetProvenance`; the service
  hashes the prompt and only the hash reaches the daemon. When the clone has
  no `.nipaprovenance.yml` yet, the form says saving creates and stages it,
  which turns on origin tracking (like `nipa provenance set`). Both sections
  are hidden for files that are neither an engine asset nor covered by a
  record or rule. Queries are keyed `["refs"|"provenance", root, path]` and
  refetched 300 ms after a `repo:changed` notice.
- Command palette (`components/palette/CommandPalette.tsx`, Ctrl/Cmd+K or
  the header Search button): one input over Actions (Update, Push…, Switch
  branch…, New merge request, Lock file…, Refresh, each tab, Settings,
  Keyboard shortcuts), Files of every opened clone (daemon `SearchFiles`
  per clone, debounced, re-ranked together with `lib/fuzzy.ts` so the best
  file wins whichever clone it lives in), Branches and tags (switch or check
  out), Merge requests (the server `search` filter; `12`, `#12` or `!12`
  opens that number) and Commits (message words or id/hash prefix within the
  first loaded history page, plus an exact base36 id lookup). "Switch
  branch…" and "Lock file…" narrow the palette to branches or the current
  clone's files; Esc or Backspace on an empty query goes back. Results reach
  the repository view as an `Intent` (`components/tabs.ts`) carrying a tab
  and, for actions, `action`/`target`/`path`; `RepoView` runs each intent
  once (by `seq`), and choosing a clone in the sidebar drops the pending
  intent so coming back never repeats an action.
- Keyboard shortcuts: every binding is declared once in `lib/shortcuts.ts`
  (palette Mod+K, sheet `?`, sidebar Mod+B, settings Mod+,, tabs Mod+1–9,
  Update Mod+Shift+U, refresh Mod+R/F5, push message Mod+Shift+M, push
  Mod+Enter in the message, stage/unstage Space, next/previous file J/K or
  arrows) and handled through `hooks/useShortcuts.ts`. Plain keys never fire
  in text fields, Space and arrows never steal from a focused control,
  nothing fires during IME composition or while a modal is open; a test
  fails when two shortcuts share a chord. `?` opens the "Keyboard shortcuts"
  sheet built from the same map.
- Blame: a "Blame" toggle on text viewers (the Changes and History diffs,
  merge request files, the Files detail viewer; remembered in
  `localStorage` `nipa.blame`) adds a gutter to the new side naming the
  author and age of the commit that last changed each line, once per run
  of lines from the same commit, with the commit title and date as a
  tooltip. Clicking it opens that commit in History. The working file
  (`newRevision` empty) is blamed against the pinned commit, so lines that
  are not pushed yet read "Not pushed"; a commit diff blames its
  `newRevision`. `Blame` reads the daemon `Blame` stream (`useTextTools`,
  query `["blame", root, path, revision, ...]`, refreshed with the other
  repository queries after an operation); lines older than the history the
  daemon could read show "Older". Text files only, up to 2 MiB.
- Shelves (local only, `.nipa/shelves/<id>/`): "Shelve…" above the changed
  files and the per-row shelve icon open `ShelveDialog`, a checklist of every
  change (untracked files included, all checked or only the row's file) with
  an optional name; the checked files are stored and go back to the synced
  version (`Shelve`, daemon `ShelveCreate`). The collapsible "Shelves" section
  under the file list (`ShelvesPanel`, open state in `localStorage`
  `nipa.shelves.open`) lists shelves newest first with Apply, Apply and keep
  and Delete; clicking a shelved file previews it in the diff pane through the
  usual viewers (`ShelfFiles`: `FileDiff.oldSource`/`newSource` are
  `shelf_base`/`shelf` with the shelf id as revision, `imageRefs` passes them
  on). An apply that conflicts waits like a merge: the banner reads "Applying
  shelf …" with Resolve conflicts and Abort (`AbortShelfApply`), the
  Conflicts tab compares the synced version (ours) with the shelf (theirs),
  resolving does not stage, and the last resolution finishes the apply. Files
  with local changes are refused. Before Update, a branch switch or a tag
  checkout with local changes, `useSyncGuard` asks the daemon `SyncPlan`
  which of them the operation would replace; when there are some the
  "Your changes would be replaced" dialog offers Shelve and continue (shelves
  exactly those files as "Before updating"/"Before switching to …", then
  runs the operation), Continue anyway and Cancel. A failed check never
  blocks.
- Open at line: external tools in Settings may be command templates with
  `{file}` and `{line}` (`code --goto {file}:{line}`; words are split on
  spaces outside quotes, so quote program paths with spaces; a command
  without `{file}` is still a program started with the file). Rows of a
  working-copy text diff have "Open in editor at line N" on hover, using
  the row's new-side line (`OpenFileAt`; `{line}` is 1 for plain opens).
  Without a tool for the extension the system default opens the file
  without the line.
- Delete safety: staging a missing file (a deletion) first looks up its
  referrers; when another file uses it, a "Stage deletion?" confirmation
  names how many and the first three. The lookup is raced against 400 ms:
  a cached graph answers well within it, so an unused asset stages without a
  dialog, while a slower lookup (the first scan of a large project) opens
  the dialog at once and fills the note in when it arrives. A failed lookup
  stages without asking. The existing Discard confirmation of a modified
  asset names its referrers too.
- Generated text: with an endpoint and a model set (Settings › AI
  assistance, or `NIPA_LLM_ENDPOINT`/`NIPA_LLM_MODEL`), the push box and the
  New merge request dialog show a Generate button (`useLLMSettings`,
  `useDraft`). `GenerateCommitMessage` / `GenerateMergeRequest` call the
  daemon's `Generate` and the draft fills the push message, or the MR title
  and description, for editing; nothing is pushed or opened by itself. A busy
  state and the daemon's error are shown under the button. The
  `Nipa-Generated: <model>` trailer is appended at push/open time while the
  field holds generated text (kept or edited) and forgotten once the field is
  cleared, like `nipa push --generate` (message) and `nipa mr create
  --generate` (description, never the title). Settings › AI assistance edits
  the endpoint and model through `LLMSettings`/`SetLLM` (the shared
  `~/.config/nipa/config.json` `llm` block, `apiKeyEnv` kept), the key
  through `SetLLMKey`/`ClearLLMKey` (OS keyring entry `llm-api-key`; the
  field is write-only and only shows "Saved") and checks them with `TestLLM`
  (one small request from the app process, typed key first, then
  `NIPA_LLM_API_KEY`/`apiKeyEnv`, then the saved key; errors redacted). The
  daemon resolves the settings on every call, so no restart is needed.
- Storage: the Details tab's "Storage" card (`StorageCard`) shows what the
  clone occupies, from `DiskUsage` over the daemon's `DiskUsage`: project
  files (the snapshot's tracked sizes and file count, never a walk of the
  working tree), the local cache (`.nipa/objects` bytes) and the cached
  object count, plus the lock pins kept in the cache. "Clean up" calls
  `CleanUp` (daemon `GC`, the `nipa gc` prune) and reports the freed amount
  as a toast, "Repository is busy, try again" when another process holds
  `.nipa/lock`, or that a merge or revert is in progress; the numbers are
  refetched afterwards and after every operation. The button is disabled
  while an operation runs on the clone. A full checkout larger than
  `SPARSE_HINT_BYTES` (20 GiB) shows a one-line hint that opens the sparse
  folder editor.
- Clone and sign in: "Clone a repository…" (welcome screen and the sidebar)
  opens a dialog: server address (remembered) → `AccountStatus` tells
  whether the daemon holds a session for it; without one the dialog tries
  `SignInSSH` (registered key from ssh-agent or `~/.ssh`) first and offers
  email + password (`SignIn`) → `ListProjects` (readable projects of every
  organization the account belongs to, filterable) → branch
  (`RemoteBranches`, default preselected), folders (Everything, or a lazy
  folder tree from `RemoteTree` where ticking a folder takes its whole
  subtree) and location (`ChooseFolder` + folder name) → `Clone` streams
  download progress as `op:progress` for the target folder (cancellable),
  then opens the clone like `AddRepo`. The Details tab's "Change folders…"
  reuses the folder tree for an existing clone and applies it with
  `SetSparse` (sparse prefixes + update, progress in the header).
- History: the History tab pages the branch log (`History` over
  `ProxyCommitLog`, 50 per page, "Load older commits") with a lane marker
  per commit (merge commits marked), tag badges and a client-side
  author/message filter. "History of a file or folder" sends the path to the
  server (`GetCommitLog.path`, the web UI's per-path history), and for an
  image path a thumbnail strip shows each version (`ImageDiff` of that
  revision alone, 128 px). Selecting a commit lists what it changed against
  its first parent (`CommitFiles` = daemon `DiffFiles` with `show`; a root
  commit compares with an empty tree) and opens the same text, image, model,
  audio and table viewers as the Changes screen: each file carries
  `oldRevision`/`newRevision`, so the viewers read both sides from the server
  (`BlobRef` revision) and cache them under a separate `commit` query key.
  Parent links jump to commits outside the loaded page. Actions: Branch…
  (create at this commit), Tag… (name + message) and Revert… (confirmation,
  then the daemon `Revert` stream; merge commits stay CLI-only because they
  need `--mainline`).
- Branches & tags tab: branches (default first, current/default/protected
  badges, Switch, Delete with confirmation; "New branch" starts from the
  clone's branch head) and tags (message, age, commit; Check out, Delete,
  "New tag" on the branch head). Check out is the CLI's `switch --tag`: the
  header shows the tag instead of the branch, Update is disabled and a
  banner offers "Back to <branch>". A revert stopped on conflicts (status
  `pending` = `revert`, with the conflicting files and the commits left)
  shows a banner with Resolve conflicts, Continue (enabled once every file
  is resolved), Skip and Abort. Each other branch offers "Merge into
  <branch>" (confirmation, then the daemon `Merge`); a merge without
  conflicts is pushed right away.
- Godot scenes: the daemon's `MergeGodotResource` (see `docs/DAEMON.md`)
  merges a conflicted `.tscn`/`.tres` section by section without markers.
  The Conflicts tab offers it as "Merge scene" above the text editor of a
  conflicted text `.tscn`/`.tres` (`MergeScene`, `lib/conflicts.ts`
  `mergesScene`): a clean merge resolves the file with the merged content
  in one click; otherwise the sections and keys it could not decide are
  listed and the line-by-line editor below stays the way to resolve it.
- Conflicts tab (shown while a merge, revert or shelf apply waits with conflicts, with
  the open count): the conflicted files with their kind (both changed, both
  added, deleted on one side) and resolved state; the merge banner reads
  "Merging <source> into <branch>" with Resolve conflicts, Finish merge (a
  push with `Merge branch '<source>' into '<branch>'`, enabled once all are
  resolved) and Abort. Text files open a three-way editor: each marker
  block shows mine and theirs side by side with Mine / Theirs / Both (either
  order), the common ancestor on demand, and an editable result that is
  saved with Mark resolved (refused while markers remain). Binary and
  deleted-on-one-side files offer Take mine / Take theirs (Keep the file /
  Delete it, Keep it deleted / Restore theirs) and "Use the file on disk",
  with ours and theirs compared in the asset viewers (`ConflictDiff`).
  Resolving writes the file, stages it and records it in the pending state
  (`resolved`), so the choice survives restarts.
- Team activity: every watched clone also follows the daemon's
  `WatchEvents` stream (poll interval from settings). The sidebar shows new
  commits (↓N), reviews waiting and an unreachable server per clone, the
  Update button counts the new commits, and the matching views refresh.
  Teammate pushes, released locks and review requests raise desktop
  notifications (each kind can be switched off); clicking one opens the
  History, Locks or Merge requests tab (`Navigation`, `app:navigate`).
- Deep links: `nipa://<server>/<org>/<project>[/merge-requests/<n>|
  /commits/<id>|/file/<path>|/files|/history|/branches|/locks|/changes|/settings]`
  (a `server` query parameter overrides the address; localhost defaults to
  http, other hosts to https). `/file/<path>` (segments URL-escaped) opens the
  Files tab at that file, or inside that folder; "Copy nipa:// link" produces
  it and adds `?server=` only when the scheme differs from that default. A
  link to an opened clone selects it and opens the tab, request, commit or
  file; a link to a project not cloned yet opens the clone
  dialog with the server and project filled in. The scheme is registered by
  the build assets (`build/config.yml` `protocols`, Info.plist, the Linux
  `.desktop` file, the NSIS installer).
- Settings (gear in the sidebar): theme, keep in tray, start at login,
  notifications per kind, server check interval (30 s to 1 h, the daemon
  clamps to at least 15 s), uploads at once (the shared `uploadWorkers` in
  `~/.config/nipa/config.json`, applied after "Restart" of the background
  service) and external tools per extension (`.blend` → Blender), which
  `OpenFile` uses instead of the system default (`open -a` for macOS app
  bundles). The Details tab edits the clone's `.nipaignore` (versioned) and
  `.nipa/ignore` (local).
- Merge requests tab: the list (Open/Drafts/Merged/Closed/All, server-side
  search, 50 per page) with status, branches, approvals and changes
  requested, and "New" (source = the clone's branch, target = the default
  branch, title, description, draft). A request opens in three tabs.
  Conversation: description, the timeline merged with review decisions and
  top-level threads in time order (review events are shown through their
  review, stale and dismissed ones dimmed), status checks with links, a
  review box (Comment, Request changes, Approve), a comment box and the
  merge box: mergeability and the blocker in words (draft, changes
  requested, approvals, required reviewers, checks), strategy (merge,
  squash, rebase, fast-forward; remembered), delete source, Mark ready/
  Mark as draft, Close with confirmation, Reopen for closed requests.
  Commits: the request's commits. File changes: `RevisionFiles` (daemon
  `DiffFiles` of the merge base against the source head, or the merge
  commit once merged) in the same viewers as History; text diffs show
  threads under their line (`TextDiff` `comments`: removed lines anchor by
  old number, others by new) and a hover button starts a new one, outdated
  threads collapse above the diff, binaries point to the conversation.
  Threads collapse once resolved and take replies and resolve/reopen.
- Locks: `LockStatus` asks the daemon's `LockStatus` RPC for every visible
  lock plus the state of each changed binary (a tracked file stored as
  binary, or a known asset extension) in the lock scope of the clone's
  branch (mainline locks on the default branch, that branch's locks
  otherwise): `mine`, `theirs` (someone else's file or folder lock),
  `needed` (a tracked binary the server will refuse to push without the
  caller's lock) or `free`. The file list marks each row (blue: yours, red:
  held by someone, amber: needs a lock, click to lock; hover to lock a free
  one) and the push box refuses to push while a staged file is held by
  someone else, or offers "Lock N files" when staged files only need locks.
  The Locks tab lists all locks (in scope first; mine/team filter and text
  search; branch, holder, age, merge request) with Release for your own locks
  and, for project admins, anyone's after a confirmation, plus "Lock & edit a
  file…": the native file dialog (`PickFile`, refuses paths outside the
  clone or in `.nipa/`) picks a file, `Lock` locks it and `OpenFile` opens it
  in its default application. Lock status refreshes on change events, after
  operations and every 30 s; when it cannot be read (offline) nothing is
  blocked and the server stays the gate.
- `frontend/`: React 18, Vite 8, Tailwind 4 (tokens in `src/index.css`,
  light/dark/system theme in `src/lib/theme.tsx`), TanStack Query, lucide.
  Generated bindings live in `frontend/bindings` and are committed (CI fails
  when they are stale). `src/api/desktop.ts` is the only module importing them,
  which keeps tests mockable.
- Linux uses GTK4/WebKitGTK 6.0; the Taskfile falls back to `-tags gtk3` when
  `pkg-config webkitgtk-6.0` is missing (`wails3 build EXTRA_TAGS=...`
  overrides).

Commands: `make desktop` (app + bundled `nipa` in `desktop/bin`),
`make desktop-dev` (hot reload, `NIPA_BIN` = `bin/nipa`), `make desktop-test`.
They need the `wails3` CLI (`go install
github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`, plus `-tags gtk3` on hosts
with WebKitGTK 4.1 only). CI: `.github/workflows/desktop.yml` (tests, then a
Linux/macOS/Windows build matrix with a bindings freshness check).

## Asset diff coverage

| Tier | Formats | Viewer |
|---|---|---|
| MVP | PNG JPEG GIF WebP BMP TIFF TGA | image: side-by-side, swipe, onion skin, pixel heatmap, blink, channels, synced zoom |
| MVP | DDS KTX2 EXR HDR PSD/PSB | image + mip/face picker, exposure, PSD layer list |
| MVP | text, code, JSON, shaders | text diff (`internal/diff` hunks) |
| v1 | Unity YAML (`.unity .prefab .asset .mat .anim .controller`) | structural scene diff: object tree, property changes, dependencies; text toggle |
| v1 | Godot text resources (`.tscn .tres`, `project.godot`, `.import`) | same structural scene diff: node tree, properties, connections, `ext_resource` dependencies |
| v1 | glTF/GLB OBJ FBX USDZ | 3D: synced orbit cameras, wireframe, stats diff |
| v1 | WAV OGG FLAC MP3 | waveform + spectrogram, synced A/B playback |
| v1 | CSV/TSV, `.xlsx` | cell-level table diff, filters, sheet tabs |
| v1 | Unity `.meta` | lint: missing/orphan sidecars, GUID change |
| v2 | MP4/MOV WebM/MKV (AVI stats-less) | synced side-by-side playback, frame step, container stats, "Open externally" |
| v2 | TTF OTF WOFF (not WOFF2/TTC) | glyph sheet, metrics, coverage, added/removed code points |
| v2 | `.blend`, `.uasset`/`.umap` | embedded thumbnail in the image viewer ("Thumbnail only") |
| always | anything else | size/hash stats + external diff tool |

## `internal/preview`

Pure package (no client imports), shared by the daemon and later the server.

- `Classify(path, isBinary) Kind` picks the viewer kind
  (`image/model/audio/video/font/table/scene/text/opaque`); a binary
  scene extension is `opaque`.
- `DecodeImage(name, data)` decodes PNG/JPEG/GIF/BMP/TIFF/WebP (stdlib +
  `golang.org/x/image`) and TGA (own decoder: true-color, gray, color-mapped,
  RLE, 15/16/24/32 bpp, any origin) into `*image.NRGBA`, with format, bit
  depth and alpha. `MaxPixels` (8192²) bounds every decode before allocation;
  float sources get half of it (`checkFloatSize`, 16 bytes per pixel).
- `DecodeImageAt(name, data, Select{Level, Face})` adds the game formats,
  chosen by extension, and picks one surface of a texture (out-of-range values
  clamp; `Image.Levels/Faces/Cube/Level/Face` describe the texture):
  - DDS: legacy FourCC/D3DFMT/bit-mask headers and DX10 (DXGI) headers,
    mipmaps, cube maps and arrays; BC1–BC7 incl. BC6H (UF16/SF16), 8-bit
    RGBA/BGRA/R/RG/A, R10G10B10A2, RGBA16, half/float formats, R11G11B10 and
    RGB9E5. Volume textures are refused.
  - KTX2: the same pixel formats by `vkFormat`, levels/faces/layers, Zstandard
    or zlib supercompression. Basis Universal (BasisLZ/UASTC) is refused.
  - EXR: single-part scanline and tiled (level 0) images, NONE/RLE/ZIPS/ZIP/
    PXR24, half/float/uint channels; RGBA of the unnamed layer, else the first
    named layer with colour, Y as gray, or the first channel as gray. PIZ,
    B44, DWA, multi-part and deep files are refused.
  - Radiance HDR: adaptive RLE, old runs and flat scanlines, `-Y`/`+Y`.
  - PSD/PSB: the merged image (raw or PackBits; 1/8/16/32-bit; RGB, gray,
    indexed, CMYK, bitmap, duotone, multichannel; merged transparency when
    the layer count is negative) plus `Layers` top-most first with group
    nesting, visibility, opacity, blend key, bounds and an FNV hash of each
    layer's channel data (also read from `Lr16`/`Lr32`).
  Block decoders are checked bit-for-bit against bcdec goldens
  (`testdata/bcn`, generators in `testdata/gen`). Refusals wrap
  `ErrUnsupported` with a reason ("EXR PIZ compression") that the daemon
  forwards in its 415.
- `DecodeModel(name, data)` reads `ModelStats` from glTF/GLB (JSON only:
  accessor counts, triangles by primitive mode, materials, textures, skins and
  unique joints, animations, morph targets, world-space bounds from the
  POSITION min/max boxes through the node transforms of the default scene,
  external buffer/image URIs) and OBJ (vertices, fan-triangulated faces,
  objects/groups, `usemtl` names, `mtllib` files, bounds);
  `MaterialLibraryDependencies` lists an `.mtl` file's textures. Other model
  formats return `ErrUnsupported`.
- `DecodeAudio(name, data, AudioOptions)` decodes WAV (PCM 8–32-bit, float,
  extensible), AIFF/AIFC (big/little-endian PCM, float), FLAC
  (`mewkiz/flac`), Ogg Vorbis (`jfreymuth/oggvorbis`) and MP3
  (`hajimehoshi/go-mp3`, mono detected from the frame header), all pure Go,
  and streams the samples once through the analysers: waveform buckets,
  sample peak, RMS, clipped samples (at the format's full scale), integrated
  loudness per ITU-R BS.1770 (K-weighting for any rate, 400 ms blocks with
  absolute and relative gates, 5.1 surround weights), a 2048-point Hann
  spectrogram on log-spaced rows and an optional playback WAV. When the
  header's length is missing or off by more than 1 % it decodes again with
  the real length. `MaxAudioFrames` (one hour at 48 kHz) cuts longer audio
  and sets `Truncated`; decoder panics become errors.
- `ParseTable(name, data)` reads CSV/TSV (delimiter sniffed among `,` `;`
  tab; UTF-8 with or without BOM, UTF-16 with BOM; lazy quotes; ragged rows;
  `MaxTableCells`), and `DiffTables(old, new)` aligns columns and rows with
  a longest-increasing-subsequence match on unique keys (content rows get an
  occurrence number), so it stays O(n log n) on large localization tables.
- `ParseWorkbook(data)` reads `.xlsx` with `archive/zip` + `encoding/xml`:
  sheets from `xl/workbook.xml` resolved through
  `xl/_rels/workbook.xml.rels` (chart sheets skipped, `sheetN.xml` fallback),
  shared strings (rich runs joined, phonetic runs dropped), inline strings,
  booleans as `TRUE`/`FALSE`, errors and numbers as their cached values; no
  formula evaluation or number formats. Row 1 is the header and record
  numbers equal Excel row numbers. Bounded by `MaxWorkbookSheets`,
  `MaxWorkbookXMLBytes` (decompressed total), `MaxWorkbookRows`/
  `MaxWorkbookColumns`, `MaxTableCells` across sheets and 10000 zip parts;
  bomb-tested and fuzzed. `ParseSheets` dispatches by extension (CSV/TSV =
  one unnamed sheet) and `DiffSheets` pairs sheets by name.
- `ParseUnityYAML(data, resolve)` splits a Unity text file on its
  `--- !u!<class> &<fileID>[ stripped]` headers, decodes each document with
  `go.yaml.in/yaml/v3` and returns a `Scene`: object paths from the
  GameObject/Transform hierarchy (`Root/Child > Component`, prefab
  instances by name with their children and stripped objects under them),
  MonoBehaviours named after their script, properties flattened to
  `key.path → value` (flow vectors kept whole, one-key lists such as
  material properties keyed by name, long values clipped with a hash, prefab
  overrides as `override <fileID>.<propertyPath>`) and references rendered
  as local object paths or `resolve(guid)` asset paths (`Refs` lists the
  dependencies). `MaxSceneObjects`/`MaxSceneProps` bound it.
  `DiffScenes(old, new)` matches objects by ID (document order is ignored),
  reports added/removed/changed/moved (parent changed) objects with their
  property changes, the changed objects' ancestors for the tree and the
  dependency changes.
- `ParseGodotResource(data)` reads Godot text resources through
  `internal/godotres` (lossless `Parse`/`Write`, ordered sections and raw
  property values, `Section.Identity()`): node IDs are paths below the root
  (`.` for the root, display paths start with its name), types come from
  `type` or the instanced scene, `ExtResource`/`SubResource` references are
  rendered as paths or `sub:<type>#<id>` (strings untouched),
  sub_resources, connections and `editable` entries are their own objects,
  and any other section (`project.godot`, `.import`) is one object per
  section. Binary resources (`RSRC`) are refused with `ErrUnsupported`.
- `ProbeVideo(name, r io.ReaderAt, size)` reads container headers only:
  ISO BMFF (MP4/MOV: `mvhd`/`tkhd`/`mdhd`/`hdlr`/`stsd`/`stts`, 64-bit
  boxes, `moov` anywhere) and Matroska/WebM (EBML `Info`, `Tracks`,
  unknown-size segments, stops at the first `Cluster`) into duration,
  resolution, codec names, frame rate, audio rate/channels, overall bitrate
  and track count. Box/element count and depth are capped; other containers
  are `ErrUnsupported`.
- `DecodeFont(name, data)` parses TTF/OTF through
  `golang.org/x/image/font/sfnt` (WOFF 1.0 is unwrapped first; WOFF2 and
  collections are `ErrUnsupported`, `MaxFontBytes` 32 MiB): names, glyph
  count, metrics in font units, mapped code points as ranges (controls
  skipped) and counts per Unicode script. `RenderFontSheet` draws it with
  `font/opentype`; `CompareFonts` subtracts the coverage both ways and
  lists changed names/metrics.
- `DecodeThumbnail(name, data)` extracts embedded previews: Blender `TEST`
  blocks (classic 32/64-bit headers in either byte order and the 5.0
  `BLENDER17-01` large headers; gzip and zstd files decompressed with a
  64 MiB scan cap; bottom-up rows flipped) and Unreal package thumbnails
  (best effort: a PNG, or a JPEG with negative height, preceded by its
  width/height/size triple that matches the image header). No thumbnail is
  `ErrNoThumbnail`; `HasEmbeddedThumbnail(path)` names the formats.
- Float sources keep linear samples in `Image.HDR` (`FloatImage`) while
  `Pixels` is their clamped sRGB rendering, used for `CompareImages`.
  `DownscaleFloat` box-filters them and `PackHDR` packs them into an opaque
  8-bit PNG twice as wide (RGBE mantissas left, exponent + alpha right) so a
  browser canvas does not premultiply them away.
- `Downscale(img, maxSide)` fits a preview box with bilinear scaling.
- `CompareImages(old, new, threshold)` returns changed pixel count/ratio, max
  channel delta, the changed bounding box and size/format/depth/alpha changes;
  fully transparent pixels compare equal.
- Decoders are fuzzed (`FuzzDecodeTGA`, `FuzzDecodeImage`, `FuzzDecodeDDS`,
  `FuzzDecodeKTX2`, `FuzzDecodeEXR`, `FuzzDecodeHDR`, `FuzzDecodePSD`,
  `FuzzDecodeModel`, `FuzzDecodeAudio`, `FuzzParseTable`, `FuzzProbeVideo`,
  `FuzzDecodeFont`, `FuzzDecodeThumbnail`); encoded audio fixtures come from
  `testdata/gen/audio.sh` (GStreamer), the video/thumbnail/WOFF fixtures in
  `testdata/media` from `go run ./internal/preview/testdata/gen/media`;
  crashers found by the fuzzer are kept as seeds in `testdata/fuzz`.

## Packaging and updates

- **Version.** Release builds take their version from the tag
  (`wails3 task release VERSION=v1.2.3`, `NIPA_RELEASE_VERSION`, or
  `git describe`) through `internal/version` (`VERSION_LDFLAGS` in
  `desktop/Taskfile.yml`), so the app and its bundled CLI report the same
  version; the Info.plist, NSIS and nfpm versions follow. Builds whose version
  is not a release (`dev-…`, dirty) or that carry no update key never update.
- **Bundled CLI.** The app starts the daemon from the `nipa` next to its
  binary, and an update must move both. macOS ships it inside the `.app`
  (`Contents/MacOS/nipa`, universal), so the bundle swap carries it. Windows
  and Linux production builds embed it (`internal/companion`, `companion`
  tag, built by `common:build:companion`) and write it next to the app at
  start when it differs (a running `nipa.exe` is moved to `nipa.exe.old`).
  The connector's `MinVersion` then retires a daemon older than the app
  (`Shutdown`, wait, start the new CLI); newer or dev daemons are kept.
- **Artifacts** (`wails3 task release`, `make desktop-release`,
  `bin/release/`): macOS `nipa-desktop-macos-universal.zip` (update) and
  `.dmg`, signed and notarized when `MACOS_SIGN_IDENTITY` (+
  `MACOS_NOTARY_KEY`/`_KEY_ID`/`_ISSUER_ID`) is set; Windows
  `nipa-desktop-windows-amd64.zip` (update) and `-setup.exe` (per-user NSIS
  install in `%LOCALAPPDATA%\Programs\Nipa`, Authenticode-signed with
  `SIGN_CERTIFICATE`/`SIGN_THUMBPRINT`); Linux
  `nipa-desktop-linux-amd64.tar.gz` (update / portable) plus `.deb`/`.rpm`
  installing `nipa-desktop` and `nipa` into `/usr/local/bin`.
- **Toolchain.** CI installs `wails3` from `desktop/build/tools` (a tools
  module pinning it with a `tool` directive and `go.sum`):
  `go -C build/tools install tool`.
- **Feed.** `.github/workflows/desktop-release.yml` runs on `v*` tags: it
  builds the three OSes, signs `update.json` with
  `tools/updatemanifest` (`NIPA_UPDATE_SIGNING_KEY` secret; the public half
  is the `NIPA_UPDATE_PUBLIC_KEY` variable, created with `-genkey`) and
  attaches everything to the GitHub release (`-` tags are prereleases).
  The manifest is the Wails update manifest (schema 1): per-artifact
  SHA-256 digest plus a raw ed25519 signature of that digest, the release
  page in `metadata.page`. Apps read
  `releases/latest/download/update.json` (`update.FeedURL`, build-time
  `UPDATE_FEED`, runtime `NIPA_UPDATE_URL`), which never serves
  prereleases.
- **In the app.** `main.go` initializes the Wails updater (endpoint
  provider, pinned key, no updater window) behind the `service.Updater`
  seam. `WatchUpdates` checks a minute after start and every 6 h unless
  Settings → Updates turns it off (`manualUpdates`); when the install can
  replace itself (`update.Replaceable`: the `.app` or binary directory is
  writable) it downloads and verifies in the background and notifies
  "ready, restart"; otherwise (deb/rpm, machine-wide installs, a mounted
  AppImage or DMG) it notifies and offers the release page. The state
  (`UpdateStatus`, `update:state` event) drives the Settings section
  (Check now / Download / Restart to update, progress, notes) and a banner
  above the main view. `RestartToUpdate` hands over to the Wails helper,
  which swaps the target after the app exits and relaunches it.

## Demo mode and marketing screenshots

`NIPA_DEMO=1 npm run demo` (in `desktop/frontend`) serves the UI in a plain
browser: Vite aliases the generated `Service` binding and `@wailsio/runtime`
to `src/demo/` (`service.ts` is typed `typeof Service`, so API drift fails
`npm run lint`). Fixtures live in `src/demo/data.ts` (a game-studio repo with
image, 3D, audio, CSV and text changes, merge requests, locks and a conflicted
merge); `?scenario=conflicts` switches to the merge-in-progress state.
`make desktop-screenshots` runs `scripts/screenshots.ts` (`playwright-core`
driving the system Chromium, `CHROMIUM_BIN` overrides `/snap/bin/chromium`)
for every screen in `src/demo/screens.ts` at 1440x900 @2x. The make target
captures both themes, converts them to WebP (ImageMagick) and writes
`name.webp` (dark) and `name-light.webp` to `../nipa-web/public/screenshots`
(`SCREENSHOTS_OUT`); `npm run screenshots` alone writes PNGs. Options:
`--screen <name>`, `--theme dark|light`, `--out`, `--base`.

## Roadmap (one PR each)

1. `internal/preview`: classification, common images, TGA, compare. **(done)**
2. Daemon `DiffFiles`: structured diff stream (paths, status, kind, hunks). **(done)**
3. Daemon `ReadBlob` + `Preview` for images. **(done)**
4. `desktop/` Wails scaffold + daemon connector. **(done)**
5. Changes screen + text diff, daemon `WatchChanges`. **(done)**
6. Image compare viewer. **(done)**
7. DDS/KTX2/EXR/HDR/PSD decoders + viewer controls. **(done)**
8. 3D model viewer. **(done)**
9. Audio viewer, CSV table diff, Unity `.meta` lint. **(done)**
10. Locks (lock-aware status, Lock & edit, Locks page). **(done)**
11. Sign in, clone, sparse checkout. **(done)**
12. History, branches, tags, revert. **(done)**
13. Merge requests with asset viewers. **(done)**
14. Conflict resolution. **(done)**
15. Simple mode and first-run tour: `lib/mode.ts` keeps `nipa.mode` (`simple|full`) and `nipa.tour` in the `ui` settings map; the first-run `ModePrompt` asks once; simple mode shows Files, Changes, History and Locks (plus Conflicts when open), reads "Get latest"/"Submit", and hides branch, tag, revert and merge request entry points; Settings > Interface switches back and replays the four-step `Tour`, started after the first clone. **(done)**
15. Tray, notifications, deep links, settings. **(done)**
16. Packaging and auto-update. **(done)**
17. Pre-push checks (budgets, broken references) above the Push button. **(done)**
18. Generate the push message and MR description, AI settings with a keyring key. **(done)**
19. Storage card (disk usage, clean up, sparse hint) in the Details tab. **(done)**
20. Files tab: project browser with thumbnails, file versions and "Get this version" (daemon `ListFiles`, `RestoreRevision`). **(done)**
21. Where used and provenance: references and origin panels in the Files detail and the Changes screen, "Add record…", referrer warnings on deletion (daemon `AssetRefs`, `Provenance`, `SetProvenance`). **(done)**
22. Table filters (text, column, changed columns, jump to change, sticky key column, per-column counts) and `.xlsx` diff with sheet tabs. **(done)**
23. Search, command palette and keyboard shortcuts (daemon `SearchFiles`, `lib/shortcuts.ts`, "Keyboard shortcuts" sheet). **(done)**
24. Blame gutter (daemon `Blame`, `nipa blame`), open at line through `{file}`/`{line}` tool templates, "Merge scene" for Godot conflicts. **(done)**
25. Shelves (`nipa shelve`, daemon `ShelveCreate`/`ShelveApply`/`ShelveList`/`ShelveDrop`/`ShelveFiles`), the Shelves section and dialog in the Changes screen, shelf conflicts in the Conflicts tab, "Shelve and continue" before update/switch/tag checkout (daemon `SyncPlan`). **(done)**
26. Lock watch (part A of the lock-watch phase, client only): the bell on a teammate's lock in the Locks tab stores the path per clone in `desktop.json` (`lockWatches`); when a `lock_released` event covers it, a "<path> is free" notification replaces the generic one, drops the watch and, on click, locks the file (`Navigation.Action` = `lock`). Release requests (part B) are still to do.
27. Release requests (part B of the lock-watch phase): "Ask to release" with an optional message on teammates' locks (`RequestRelease`, daemon `ProxyLockRequestRelease`), a request badge on the holder's own lock rows next to "Release", a "Someone asks me to release a lock" notification kind (`notify.lockRequests`, on by default, daemon `lock_requested` event). Server: `file_lock_requests`, `FileLock.RequestRelease`, REST `POST .../locks/{id}/requests`, gRPC `RequestFileLockRelease`, the `lock.release_requested` webhook and holder email; web Locks page and `nipa lock request <path> [-m]`. **(done)**
28. Video, font and embedded-thumbnail viewers (`VideoDiff` over the `/nipa-media/` asset route, `FontDiff`, daemon `Preview` thumbnails for `.blend`/`.uasset`). **(done)**
29. Member pickers and the project activity feed: the new merge request dialog and the merge request header pick reviewers and assignees through `components/people/MemberPicker.tsx` (name search over `Members`, daemon `ProxyProjectMembers` → server `ListProjectMembers`: org members and rule subjects who can read the project, name and id only; chips, keyboard list, initials avatars) instead of raw user ids, and `PeopleBar` assigns (`AssignMergeRequest`, daemon `ProxyMergeRequestAssign`) and asks for reviews (`RequestReview`, daemon `ProxyMergeRequestRequestReview`) on open requests. The Activity tab (`ProjectActivity` composed client-side from the 10 most recently updated branches × 30 commits, open and merged merge requests and current locks; `components/activity/ActivityView.tsx`) groups a day-by-day timeline with range, person and kind filters, and its rows open History, a merge request or the Locks tab. **(done)**
