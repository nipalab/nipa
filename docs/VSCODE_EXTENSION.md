# VS Code extension

`plugins/vscode` (Apache 2.0) puts Nipa into VS Code's Source Control view. It
is for programmers: the people who edit text in VS Code, for example in
monorepo teams, Godot and Unity projects, and Unreal projects (which have no
engine plugin). Artists keep using the desktop app and the engine plugins.

## Shape

The extension is a thin TypeScript client of the daemon's JSON bridge
(`POST /v1/<Method>`, NDJSON streams; see `docs/DAEMON.md`). It has no gRPC
code and holds no server credentials: the daemon owns the session.

| Module | Role |
|---|---|
| `src/daemon/endpoint.ts` | `daemon.json` path (`os.UserConfigDir` rules) and parsing |
| `src/daemon/connector.ts` | attach (pid alive + `Ping`) or start `nipa serve` detached; CLI from `nipa.path` → `NIPA_BIN` → `PATH` |
| `src/daemon/client.ts` | unary JSON and NDJSON stream calls with the `x-nipa-daemon-token` header; `DaemonError` carries status, gRPC code and maintenance info |
| `src/daemon/session.ts` | one shared connection, `reconnect(stale)` after the daemon went away |
| `src/daemon/ops.ts` | `OpEvent` streams to a result or a failure, `ReadBlob` assembly |
| `src/scm/repository.ts` | one `SourceControl` per clone: groups, push/update, quick diff, lock states, `WatchChanges` loop |
| `src/scm/model.ts` | clone discovery and the `FileDecorationProvider` |
| `src/scm/snapshot.ts` | `nipa-blob:` documents (`ReadBlob` source `snapshot`) |
| `src/scm/commands.ts` | command handlers, the branch/tag picker, resolving the clone and files a command is for |
| `src/scm/blame.ts` | blame annotations as editor decorations |
| `src/scm/helpers.ts` | pure helpers: `nipa://` links, lock coverage, conflict markers, blame labels, maintenance note |

Only stable VS Code APIs are used:

- `scm.createSourceControl` and resource groups, `inputBox`,
  `quickDiffProvider` and `statusBarCommands`
- `registerFileDecorationProvider` and `registerTextDocumentContentProvider`
- `scm/*` menus

## Behaviour

- **Discovery:** each workspace folder is searched upward through 32 parents
  for `.nipa/config`, and `**/.nipa/config` finds nested clones. A file
  belongs to the deepest clone that contains it.
- **Watching:** `WatchRepo` is sent on open and again after a reconnect or an
  `is not watched` answer. The extension never calls `UnwatchRepo`: unwatching
  is not reference-counted, so it would also stop the desktop app's watch.
- **Status:** status refreshes on `WatchChanges` notices (debounced 300 ms).
  An empty notice means an operation ran, which also re-reads open snapshot
  documents.
- **Groups:** staged paths are `added` or `modified`, depending on `added`;
  `deleted` paths are staged deletions. Unstaged changes are `modified`,
  `missing` (shown as deleted) and `untracked`. Conflicts are marked resolved
  from `resolved`.
- **Quick diff:** every tracked file under the clone gets a snapshot URI;
  untracked and newly added files get none. When `ReadBlob` fails, the
  snapshot document falls back to the working file, so the gutter shows no
  change rather than a whole-file addition. Binaries read as empty.
- **Push:**
  - It refuses while detached at a tag, and requires a message.
  - With nothing staged, it offers to stage the tracked changes.
  - It gathers `PushPlan` warnings (asset violations, broken references,
    plan warnings) and `LockStatus` `theirs`/`needed` states of staged files.
    Any warning asks "Push Anyway".
  - Operations are never retried after a connection error.
- **Locks:** lock states are fetched every `nipa.lockRefreshSeconds`, and
  after operations, only while there are changes. Failures (offline,
  signed out) clear them and are only logged.
- **Maintenance:** a maintenance refusal shows the server message plus the
  estimated end.

## Tests

- `npm test` (vitest, 80% gate) runs against a fake `vscode` module
  (`test/fakes/vscode.ts`) and a fake JSON bridge over real HTTP
  (`test/fakes/daemon.ts`).
- `test/integration.test.ts` starts a real `nipa serve` when
  `NIPA_VSCODE_DAEMON_BIN` is set, as CI does.
- `make vscode-test` / `make vscode-pack` and
  `.github/workflows/vscode-extension.yml` run them.

## Phase 2

- **Blame:** `BlameController` asks `Blame` (working file, so unpushed lines
  are marked) for each visible, saved file in a clone while `nipa.blame` is
  on.
  - It draws a fixed-width `before` decoration with the author and age once
    per run of lines, "Not pushed" for local lines and "Older" past the
    history read.
  - The hover card shows the commit and links to
    `nipa://.../commits/<id>`.
  - Results are cached per file and dropped on save and on every status
    change of the clone. Edits clear the annotations until the next save.
- **Locks:** Lock sends `ProxyLockFile` with the clone's branch; the server
  maps the default branch to a mainline lock. Unlock and Ask to Release read
  `LockStatus.locks` and pick the most specific covering lock (exact path or
  folder prefix, the same rule as `domain.PrefixCovers`).
  - Unlock takes your own lock first. Someone else's lock is only released
    for project admins (`can_release_any`) after a confirmation.
  - Each changed binary in the `needed` or `theirs` state gets one warning
    (with Lock or Ask to Release) until its state changes. Warnings are
    skipped during the push check and when `nipa.lockWarnings` is off.
- **Conflicts:** `Conflicts` is loaded while status lists conflicts.
  - Rows are `conflict-text`, `conflict-other` or `resolved`.
  - The sides follow `usecase.ConflictSides`: revisions for a merge or
    revert; snapshot and `shelf` for a shelf apply. They open as
    `nipa-blob:` documents.
  - Mark as Resolved (`ResolveConflict` `working`) refuses a dirty document
    and a file that still has `<<<<<<<`/`>>>>>>>` lines. Take Mine and Take
    Theirs map to `ours` and `theirs`.
- **Switch and update:**
  - The branch item in the status bar opens a picker of branches and tags
    (`ProxyBranchList`/`ProxyTagList`, keyset pages of 100, at most 10
    pages each).
  - Before `Switch` or `Update`, `SyncPlan` names the local changes the
    operation would replace. "Shelve and Continue" runs `ShelveCreate` with
    exactly those paths. A failed check never blocks.
- **Activity:** `WatchEvents` adds incoming commits to the Update button and
  shows the strictest active maintenance window in the status bar. A string
  `error` field on a streamed message is data, not a stream failure; only an
  `error` object is.
- **Links:** "Open in Nipa Desktop" and "Copy nipa:// Link" build
  `nipa://<host>/<org>/<project>/file/<path>` like the desktop `FileLink`
  (`?server=` only when the scheme differs from the loopback=http,
  else https default).

Not covered yet: continuing or aborting a pending merge or revert from VS
Code (the CLI and the desktop app do it), applying or listing shelves, and
team notifications.
