# Desktop app internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

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
  WebAudio), CSV/TSV and `.xlsx` cell diffs via `TableDiff`
  (`preview.ParseSheets` + `DiffSheets`/`DiffTables`, `sheet` selector and
  `sheets[]` for workbook tabs; `TableDiff.tsx` filters rows by text/column,
  "Changed columns only", previous/next change, sticky key column,
  per-column changed counts through `lib/table.ts`),
  video compare via `VideoDiff` (each side streamed into a private temp
  folder, `service/media.go`, served to the webview at
  `/nipa-media/<token>/<name>` by `service.MediaMiddleware` on the Wails
  asset server with range requests; `preview.ProbeVideo` container stats;
  `VideoDiff.tsx` synced side-by-side playback, frame step, A/B audio,
  "Open externally" through `OpenMedia`; files are held until the viewer
  calls `ReleaseMedia` on close and only released ones are evicted, a
  playback error checks `HasMedia` and reloads once), font compare via `FontDiff`
  (`preview.DecodeFont`/`RenderFontSheet`/`CompareFonts`; `FontDiff.tsx`
  glyph sheets, metrics, added/removed code points), embedded `.blend`/
  `.uasset`/`.umap` thumbnails in `ImageDiff` and the Files grid
  (`lib/files.ts` `hasEmbeddedThumbnail`, daemon `Preview` sets
  `ImagePreview.thumbnail`, "Thumbnail only" note),
  Unity YAML scene/prefab/asset structural diffs via `SceneDiff`
  (`preview.ParseUnityYAML` + `DiffScenes`, GUIDs resolved from a cached
  per-clone `.meta` index dropped on `.meta` change notices; Godot
  `.tscn`/`.tres`/`project.godot`/`.import` through
  `preview.ParseGodotResource`),
  Unity `.meta` warnings via `MetaWarnings` (`internal/unitymeta`),
  warn-only pre-push checks via `PushChecks` (daemon `PushPlan`; the
  collapsible "Checks" block above Push in `CommitBox`, budget violations
  and broken/unused references, "Push anyway"; `usePushChecks` keyed on the
  staged set, debounced, refreshed on `repo:changed`; above
  `LARGE_PUSH_FILES` (10,000) staged files `CommitBox` adds a note with the
  count pointing at generated folders and `.nipaignore`),
  generated push messages and MR descriptions via `GenerateCommitMessage`/
  `GenerateMergeRequest` (daemon `Generate`; Generate buttons in `CommitBox`
  and the New merge request dialog, shown when `LLMSettings.configured`;
  the draft only fills the fields, `useDraft` appends the `Nipa-Generated`
  trailer to kept/edited drafts and forgets it once the field is cleared),
  a Details-tab "Storage" card via `DiskUsage`/`CleanUp` (daemon
  `DiskUsage`/`GC`; `details/StorageCard.tsx`: project files, local cache,
  cached objects, lock pins, "Clean up" with the freed amount as a toast or
  "Repository is busy, try again", and a sparse-editor hint for full
  checkouts over `SPARSE_HINT_BYTES` = 20 GiB),
  a Files tab first in the tab list (`components/files`: lazy `FolderTree`,
  virtualized sortable `FileList`/thumbnail `FileGrid` (view kept in
  `localStorage` `nipa.files.view`), `FileDetail` reusing the change viewers
  on the new side only via `FileView`, last change via `FileInfo`, versions
  with "Get this version" through `RestoreRevision` (confirmed when the file
  has local changes); `Files` over the daemon `ListFiles`, lock state joined
  in the frontend from `LockStatus` (`lib/files.ts` `lockFor`), 128 px
  working-file thumbnails cached by hash, sparse clones list folders outside
  the checkout greyed from `RemoteTree` with "Download this folder" via
  `SetSparse`, `RevealFile`/`FileLink`, deep link `nipa://.../file/<path>`
  (`Navigation.path`, `Intent.path`), History jumps filtered by path),
  where-used and origin panels via `AssetRefs`/`Provenance`/`SetProvenance`
  (daemon RPCs of the same names; `files/AssetInsight.tsx` in the Files
  detail pane and as the collapsible "References & origin" footer
  `InsightPanel` under the Changes diff viewer: Used by / Uses / Unresolved
  rows that open the file in the Files tab (`RepoView` `showFile` → the
  `open` intent), truncated/sparse notes, origin fields and status, an "Add
  record…" form for a missing record (prompt hashed in the service, never
  sent); `hooks/useAssetInsight.ts` keys `["refs"|"provenance", root, path]`,
  refreshed on `repo:changed`; delete safety in
  `changes/DeleteSafety.tsx`: staging a missing asset other files use asks
  first (`useStageGuard`, lookup raced against 400 ms before the dialog
  opens and fills in), the Discard dialog of a modified asset names its
  referrers)
  a command palette (`components/palette/CommandPalette.tsx`, Ctrl/Cmd+K:
  actions, files of every clone via `SearchFiles` (daemon `SearchFiles`
  over `localrepo.SearchPaths`, a `LIKE` subsequence prefilter ranked in
  Go; `lib/fuzzy.ts` ranks the same way client-side), branches/tags, MRs by
  server search or number, commits by message/id/hash plus exact id
  lookup; "Switch branch…"/"Lock file…"/"Branch from issue…" sub-modes (the
  issue mode searches through `SearchIssues` when the tracker can search and
  always offers a typed key, `lib/palette.ts` `issueKey`; choosing one sends
  the `issue-branch` intent, which `RepoView` runs as `IssueBranch` (daemon
  `ProxyResolveIssues`, naming like `nipa branch --issue`; NotFound/
  FailedPrecondition/Unimplemented skip the check) + `CreateBranch` from the
  current branch + the guarded switch); results become `Intent`s
  with `action`/`target` (`components/tabs.ts`) that `RepoView` runs once
  per `seq`), keyboard shortcuts declared once in `lib/shortcuts.ts` and
  bound through `hooks/useShortcuts.ts` (ignored in text fields, IME
  composition and under modals; Space/arrows yield to focused controls),
  the `?` "Keyboard shortcuts" sheet, window zoom (`lib/zoom.ts`:
  Ctrl/Cmd `+`/`=`/`-`/`0`, also under modals via `inModals`, steps 50–200%,
  `nipa.zoom` in the `ui` map, Settings > Appearance select; `SetZoom` →
  `Service.ZoomFunc` → Wails `window.SetZoom`, `service.StoredZoom` sets the
  initial window option and `applyStoredZoom` re-applies it at boot since
  macOS ignores that option),
  a blame gutter on text viewers via `Blame` (daemon `Blame`; the
  "Blame" toggle in `DiffViewer` and the Files detail viewer, kept in
  `localStorage` `nipa.blame`, `hooks/useTextTools.ts` +
  `lib/blame.ts`: author and age once per run of lines, "Not pushed" /
  "Older", click → the commit in History through `RepoNavContext`, which
  `RepoView` provides), "Open in editor at line N" on working-copy text
  diff rows via `OpenFileAt` (tool commands may be templates with
  `{file}`/`{line}`, split on spaces outside quotes by `toolArgs`),
  "Merge scene" for conflicted Godot `.tscn`/`.tres` in the text resolver
  via `MergeScene` (daemon `MergeGodotResource`; a clean merge resolves
  with the content, conflicts are listed above the line editor),
  shelves via `Shelves`/`Shelve`/`ApplyShelf`/`AbortShelfApply`/`DropShelf`/
  `ShelfFiles` (daemon `ShelveCreate`/`ShelveApply`/`ShelveList`/
  `ShelveDrop`/`ShelveFiles`; "Shelve…" and the row icon open
  `changes/ShelveDialog.tsx`, a checklist of every change; the collapsible
  `changes/ShelvesPanel.tsx` under the file list applies, keeps or deletes
  and previews shelved files in the diff pane with `FileDiff.oldSource`/
  `newSource` `shelf_base`/`shelf`; a conflicted apply waits as
  `pending: "shelf"` with an Abort banner and the Conflicts tab), and
  "Shelve and continue" before Update, branch switch and tag checkout
  (`changes/SyncGuard.tsx` `useSyncGuard` over `SyncPlan`, daemon
  `SyncPlan`: only the local changes the operation would replace are
  shelved, then the operation runs; a failed check fails closed: the same
  dialog shows the error with only Cancel and Continue anyway. `SetSparse`
  is not previewed: `SyncPlan` cannot express a new sparse set),
  lock watching (`WatchLock`/`UnwatchLock`/`LockWatches`: paths kept per
  clone in `desktop.json` `lockWatches`; a `lock_released` event that covers
  one raises a "free" notification instead of the generic one and clicking
  it locks the file through `Navigation.Action`),
  issue chips (`components/issues/IssueChips.tsx`: `Commit.issues`,
  `MergeRequest.issues` and `Branch.issues` from the `issue_keys` fields,
  omitted when empty; plain key chips in list rows; the Branches view passes
  `link` so its key chips open the issue URL (click does not bubble to the
  row, no `ResolveIssues`); the commit and merge request headers pass
  `root`, link each chip to the tracker and call `ResolveIssues` (daemon
  `ProxyResolveIssues`, empty without a tracker) once per open view to show
  title and status, quietly key-only when the tracker is down or fails),
  member pickers and the project activity feed (`Members`/`AssignMergeRequest`/
  `RequestReview`/`ProjectActivity`; daemon `ProxyProjectMembers` over the
  server `ListProjectMembers` RPC and `GET .../projects/{p}/members`, backed by
  `Permission.ListProjectMembers`: name-matched org owners and subjects of
  rules that grant read in one SQL query (every org member and rule subject
  when a project default grants read), name and id only;
  `ProxyMergeRequestAssign`/`ProxyMergeRequestRequestReview`;
  `people/MemberPicker.tsx` replaces the raw user-id inputs in the New merge
  request dialog and `mr/PeopleBar.tsx` edits assignees and review requests in
  the request header; the Activity tab `activity/ActivityView.tsx` shows
  `service.ProjectActivity`, composed client-side from the 10 most recently
  updated branches × 30 commits, open and merged merge requests and the
  current locks, merged and ordered by `mergeFeed`, with range, person and
  kind filters and rows that open History, a merge request or Locks),
  localization (`frontend/src/i18n`: `i18next` flat catalogs `locales/{en,id,ja,zh-CN,ko}.json`,
  English is the source, `_one`/`_other` plurals, `nipa.language` in the `ui` map =
  `system` or a code, Settings > General changes it and reloads; components use
  `useTranslation`, everything else `translate`/`plural`/`locale()`; `main.tsx`
  loads `App` after the settings hydrate; `i18n/lint.test.ts` rejects hard-coded
  JSX text and unknown keys, `catalogs.test.ts` missing keys and changed
  placeholders; new UI text must land in every catalog; `internal/lang` does the
  same for the tray and notifications through `service.LanguageOf`),
  simple mode and the first-run tour (`lib/mode.ts`: `nipa.mode`/`nipa.tour`
  in the `ui` map; `tour/ModePrompt.tsx` asks once, `visibleTabs(conflicts,
  simple)` keeps Files/Changes/History/Locks, `labels()` renames Update/Push to
  Get latest/Submit, `tour/Tour.tsx` coach marks start after the first clone and
  replay from Settings > General),
  and the Settings "AI assistance" section (`LLMSettings`/`SetLLM` on the
  shared client config, write-only key via `SetLLMKey`/`ClearLLMKey` in the
  OS keyring through `Service.Keys`, `TestLLM` one-request check),
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
  shared client config, `RestartDaemon`; `IgnoreRules`/`SaveIgnoreRules`;
  the window size and maximised state (`workspace.Window`, saved on close,
  tray quit and `ShouldQuit`) and the interface state (`ui` map through
  `UIState`/`SaveUIState`: `lib/settings.ts` loads it before the first render
  and backs `readSetting`/`writeSetting`, since the webview's localStorage is
  not kept on every platform; per-clone tab and view selections through
  `usePersistedState` + `repoSetting`, dropped when the clone is removed)),
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

