# Nipa

A centralized version control system built for binary-heavy projects (game
development, 3D assets, rich media) and large team monorepos.

Nipa combines the strengths of Git (lightweight branches, release tags, merge
requests, client-side three-way merges), Perforce (exclusive file locking,
high-throughput binary streaming) and SVN (path-based access control, one
central source of truth), so teams can track large assets at scale without
giving up developer autonomy.

- **Tech stack:** Go, gRPC/Protobuf, SQLite (`modernc.org/sqlite`), FastCDC
  content-defined chunking, BLAKE3, zstd
- **Model:** client-server monorepo: the `nipa` CLI, a local client daemon
  (`nipa serve`) for GUI and engine integrations, and the `nipad` server
  (SQLite + local chunk storage), which serves gRPC, the REST API and the
  embedded web UI on a single port (default `6745`).

## Quick start

```sh
make web-install                 # first time only (npm dependencies)
make build                       # web UI + bin/nipad + bin/nipa
cp config.yaml.sample config.yaml
./bin/nipad                      # applies SQLite migrations at startup

# clone a repository (tries your SSH key, then prompts for a password)
nipa clone http://localhost:6745/org/project ./work
cd work

# edit files, then stage and commit them on the server
nipa add path/to/file
nipa status
nipa push -m "my change message"
```

The working copy tracks the branch chosen at clone time. Files are split into
content-addressed chunks and rehydrated from a local chunk cache, so updates
and branch switches only transfer what actually changed.

- [`docs/SERVER_GUIDE.md`](docs/SERVER_GUIDE.md): building, configuring and
  running the server, including first-user and project bootstrapping.
- [`docs/CLIENT_GUIDE.md`](docs/CLIENT_GUIDE.md): a task-oriented walkthrough
  of every client command.

## Features

- **Binary streaming.** Content-aware chunk sizes (64 KB for text and
  edit-in-place formats, 4 MB for packed textures, audio, video and models),
  per-file zstd compression for text, presigned direct HTTP transfers with
  adaptive concurrency, retries and cross-project deduplication.
- **Exclusive file locks.** Modifying or removing a tracked binary requires a
  lock, either per file or per directory. Locks on the default branch apply to
  the whole project, and merge requests hold locks for the binaries they change.
- **Branches, tags and merges.** Lightweight branches, immutable release tags
  with detached checkouts, fast-forward and three-way merges with diff3 text
  merging, and forward-only reverts.
- **Merge requests.** Drafts, reviews with inline threads, required approvals
  and reviewers, status checks, assignees, and `ff`/`merge`/`squash`/`rebase`
  strategies.
- **Path-based access control.** PBAC rules per user or group on directory
  prefixes (`read`, `write`, `lock`, `admin`). Clones, manifests and diffs only
  show what the caller can read.
- **Sparse checkouts.** Materialize only selected directories; pushes still
  apply as deltas against the full server tree.
- **Large repositories.** Stat-cached `status`/`diff`, incremental local tree
  saves and batched server writes keep 10k+ file repositories fast.
- **Operations.** Webhooks, email notifications, an audit log, rate limiting,
  Prometheus metrics and OpenTelemetry tracing/logs.
- **SSH key login.** A challenge-response login over the same gRPC transport;
  the private key never leaves the client ([`docs/SSH.md`](docs/SSH.md)).

## Commands

Run `nipa <command> --help` for full details, or see
[`docs/CLIENT_GUIDE.md`](docs/CLIENT_GUIDE.md) for examples of every command
and flag.

| Command                     | Description                                                                 |
| --------------------------- | --------------------------------------------------------------------------- |
| `nipa login`                | Sign in to a server. Tries an SSH key first (`--ssh-key`, `NIPA_SSH_KEY`, config `sshKeyPath`, ssh-agent, then `~/.ssh` keys), then falls back to a password (`--username`). Every command logs in implicitly when needed. |
| `nipa clone <url> <target>` | Clone a repository. `-b <name>` selects the starting branch (default: the project's default branch), `--sparse a,b` limits the checkout to directory prefixes. |
| `nipa branch`               | Show the current branch. `-a` lists all server branches, `-c <name>` creates a branch and switches to it, `-d <name>` deletes one. |
| `nipa switch <branch>`      | Fetch the branch, materialize its tree and repoint the working copy at it. `--tag <name>` checks out a release tag instead, detaching HEAD. Not allowed while changes are staged. |
| `nipa tag`                  | List release tags. `-c <name> [-m <message>]` creates an immutable tag at the checked-out commit (or `--branch <b>` / `--commit <id\|hash>`), `-d <name>` deletes one. |
| `nipa add <path> [...]`     | Stage files (or everything inside directories) for the next push. Ignored files are skipped; `-f` stages them anyway. |
| `nipa remove <path> [...]`  | Unstage files. `-a` unstages everything.                                    |
| `nipa restore <path> [...]` | Discard unpushed changes, restoring files from the last synced tree (offline). Refused for staged paths. |
| `nipa status`               | Show the working copy: `A` staged, `M` modified, `?` untracked, `!` missing, `C` conflict. |
| `nipa push -m "<message>"`  | Upload staged changes and commit them on the configured branch. `--dry-run` reports the changes and upload estimate without contacting the server. |
| `nipa update`               | Fetch and apply the latest changes of the configured branch (or re-sync the checked-out tag while detached). |
| `nipa sparse-checkout`      | `list`, `set`, `add`, `remove` or `disable` the sparse directory prefixes, then update the working copy. |
| `nipa merge <branch>`       | Merge another branch into the current one. Fast-forwards when possible; `--no-ff`, `--ff-only`, `--abort`, `-m` and `--dry-run` are supported. |
| `nipa revert <commit>`      | Create new commits that undo a commit or a range (`<from>..<to>`, up to 16 commits) without rewriting history. `--mainline 1\|2`, `--no-commit`, `--continue`/`--skip`/`--abort` and `--dry-run` are supported. |
| `nipa log`                  | Show the commit history, scrollable in a terminal. `-n` limits, `--oneline` prints one line per commit. |
| `nipa diff [<rev1> [<rev2>]]` | Show changes as a unified patch: working tree vs the last synced snapshot (offline), a revision vs the working tree, or two revisions. Revisions are branch names, tag names, base36 commit IDs or `HEAD`/`@`; `<a>..<b>` and `<a>...<b>` (merge base) are supported. Renames are detected; `--staged`, `--stat`, `--name-only`, `--name-status`, `-w`/`-b`, `--ext-diff` and `-- <path>` are supported. |
| `nipa lock <path> [--branch]` | Lock a binary file or a directory prefix. `nipa lock list` shows active locks. |
| `nipa unlock <path> [--branch]` | Release a lock you hold.                                                  |
| `nipa mr ...`               | Merge requests: `create [--draft]`, `list`, `view`, `update`, `ready`, `assign`, `review --approve\|--request-changes`, `comment`, `reply`, `resolve`, `request-review`, `timeline`, `diff`, `check`/`checks`, `merge --strategy <ff\|merge\|squash\|rebase> [--delete-source]`, `close`, `reopen`. |
| `nipa acl ...`              | Manage path-based permission rules (`grant`, `revoke`, `list`, `my`, and `permission list\|set\|remove` for project path permissions). |
| `nipa group ...`            | Manage groups and their members (`create`, `list`, `add-member`, `remove-member`). |
| `nipa serve`                | Run the local daemon that GUI clients and engine plugins connect to. See [`docs/DAEMON.md`](docs/DAEMON.md). |
| `nipa mcp [--repo <dir>] [--allow <caps>] [--allow-write] [--no-agent-trailer]` | Serve the working copy as a Model Context Protocol server over stdio so AI agents can inspect and change it directly. Read-only tools by default; mutating tools are enabled per capability (`stage`, `push`, `sync`, `branch`, `lock`, `mr`, `merge`) with `--allow`, or all with `--allow-write`. See [AI agents and automation](#ai-agents-and-automation). |
| `nipa version [--server] [url]` | Print the client version and, with `--server`, the server's version.     |

### Branches and tags

Branch creation (`nipa branch -c <name>`) forks from the exact commit the
working copy is pinned to (clone, update, switch and push record it locally),
so it works offline and never picks up commits you have not seen.

Release tags are immutable named pointers to one commit, with an optional
annotation message (`nipa tag -c v1.0.0 -m "first release"`). `nipa switch
--tag v1.0.0` checks the tagged tree out in a detached state: `nipa update`
re-syncs the tag, `status`/`log`/`diff` follow the pinned commit, and
push/merge/revert refuse until `nipa switch <branch>` returns to a branch.

### Ignore rules

Files matching the root `.nipaignore` (versioned, shared with the team) or the
clone-local `.nipa/ignore` (never tracked) are hidden from `nipa status` and
skipped by `nipa add`; `nipa add -f` stages an ignored file anyway, after which
it stays tracked. The syntax is a gitignore subset: `#` comments, `!` negation,
`*`/`?`, `**`, a leading or embedded `/` anchoring to the root and a trailing
`/` matching directories only; the last matching rule wins. Sparse checkouts
always include the root `.nipaignore`.

### Reverts

A revert moves history forward: each reverted commit produces a new commit
applying its inverse on top of the branch head. A conflicted revert stops with
`C` files; resolve them and run `nipa revert --continue` (for a single pending
commit a plain `nipa push` also finishes it), skip the commit with `--skip`,
or discard the whole operation with `--abort`.

### Diffs

Without revisions, `nipa diff` is fully offline: the snapshot records each
file's chunk hashes, so the old side is rebuilt from the local object cache,
and unchanged files are skipped through the stat cache. With revisions, missing
chunks are downloaded first. Renames (at least 50% similarity) render git's
`rename from`/`rename to` headers. `--ext-diff` runs an external tool per file
with git's seven-argument contract, configured through `NIPA_EXTERNAL_DIFF` or
`diffExternal` in `~/.config/nipa/config.json`.

### File locks

`nipa push` refuses to land a modified or removed tracked binary unless you
hold a lock covering it. Brand-new files only conflict with someone else's
covering lock. Locks on the default branch are project-wide, while locks on any
other branch are scoped to that branch. Your exact-path locks are released once
the push they guarded lands; directory locks stay until you `nipa unlock` them.
See [`docs/LOCKING.md`](docs/LOCKING.md).

## Clients and integrations

GUI surfaces contain no VCS logic. They connect to the local `nipa serve`
daemon (loopback gRPC with a capability token, plus a JSON/NDJSON HTTP bridge
for engines that cannot speak gRPC) and use the same operations as the CLI.

```sh
nipa serve   # publishes its endpoint and token in ~/.config/nipa/daemon.json
```

- **Desktop app** (`desktop/`, Wails v3 + React): changes, history, branches
  and tags, merge requests with inline reviews, conflict resolution, file
  locks, team activity notifications and in-app updates. Visual diffs cover
  images (including TGA, DDS, KTX2, EXR, HDR and PSD), 3D models (glTF/GLB/OBJ),
  audio waveforms and loudness, CSV/TSV cells, and Unity/Godot scene structure.
  Build with `make desktop` (needs the `wails3` CLI); design in
  [`docs/DESKTOP.md`](docs/DESKTOP.md).
- **Unity plugin** (`plugins/unity/com.nipalab.nipa`): a version control
  provider with overlays, locks, history, conflicts and a project setup
  checker. Install from a git URL or tarball (`make unity-pack`); see
  [`docs/UNITY_PLUGIN.md`](docs/UNITY_PLUGIN.md).
- **Godot plugin** (`plugins/godot`, Godot 4.4+): a native
  `EditorVCSInterface`, the Nipa dock, auto-locking, conflicts with structural
  `.tscn`/`.tres` merges, history, image diffs and a setup checker. Ships as
  `nipa-godot-vX.Y.Z.zip` on releases tagged `godot-v*`; see
  [`docs/GODOT_PLUGIN.md`](docs/GODOT_PLUGIN.md#install).
- **Web UI** (`web/`, Vite + React + Primer): repository browsing, commit
  history and diffs, branches, tags, merge requests, locks, settings and
  administration. It is embedded in the server binary; see
  [`docs/WEB_UI.md`](docs/WEB_UI.md).

## AI agents and automation

Nipa can be driven by scripts and AI agents as well as by people:

- **MCP server** — `nipa mcp [--repo <dir>] [--allow <caps>]` serves the
  clone over stdio (Model Context Protocol) so agents such as Claude Code,
  Cursor or Copilot can work with a working copy directly. Read-only tools
  (status, diff, log, branches, tags, locks and lock queries, conflicts, merge
  requests with their diff, threads, commits and timeline) are always
  registered; write tools are grouped by capability (`stage`, `push`, `sync`,
  `branch`, `lock`, `mr`, `merge`) and enabled with `--allow` (or all with
  `--allow-write`). Diffs are capped by `max_bytes`, and text written through
  MCP carries a `Nipa-Agent: <client>/<version>` trailer (`--no-agent-trailer`
  disables it). Authentication never prompts (a prompt would corrupt the
  JSON-RPC stream), so log in with a normal CLI command in the clone first.
- **Machine-readable output** — read commands accept `--json` (`nipa status`,
  `nipa diff`, `nipa log`, `nipa branch`, `nipa tag`, `nipa lock list`,
  `nipa mr list`);
  `NIPA_OUTPUT=json` enables it globally. Payloads go to stdout; failures are a
  single `{"error":{"code","message","hint","action"}}` envelope on stderr, and
  exit codes are stable: `0` success, `1` generic failure, `2`
  locked/precondition, `127` not found.
- **Dry runs** — `nipa push|merge|revert --dry-run [--json]` report the
  file-level changes, conflicts and (for push) the upload estimate without
  staging, committing, pushing or rewriting the working copy.
- **REST API** — the server publishes its OpenAPI document at
  `/docs/api.json` (Swagger UI at `/docs/`), covering browsing, merge
  requests, reviews, file locks, permissions and groups.

The full agent-facing reference lives in [`docs/AI.md`](docs/AI.md).

## Architecture

- **Server** (`internal/`): `domain` (entities and errors), `usecase` (business
  logic over repository interfaces), `repository/sqlite` (sqlc), `grpc`
  (protobuf service and handlers), `http` (REST API) and `serverapp` (shared
  wiring and the single-port HTTP/gRPC/SPA lifecycle).
- **Client** (`internal/client/`): `cli` (cobra commands), `usecase`
  (clone, push, update, merge, revert, diff and more), `grpc` (transport and
  chunk transfers), `localrepo` (`.nipa/` metadata in SQLite plus a loose
  object cache), `merge` (three-way tree merge and diff3), `daemon`
  (`nipa serve`) and `mcp` (`nipa mcp`).
- **Shared pure packages**: `internal/diff` (Myers diff, tree comparison,
  rename detection, rendering; also used for server-side merge-request diffs),
  `internal/preview` (asset decoders and visual diffs) and `internal/godotres`
  (Godot resource parser and three-way merge).
- **Storage model**: content is stored as chunks addressed by BLAKE3 hash; a
  tree is a recursive manifest of directories, files and their chunk lists. A
  clone keeps a JSON `config`, a SQLite database (tree snapshot, staged files,
  stat cache, pending merge/revert state) and a content cache in `.nipa/`.

[`AGENTS.md`](AGENTS.md) has a detailed walkthrough of every flow.

## Development

```sh
make build            # web UI + bin/nipad + bin/nipa
make test             # go test ./...
make test-client      # black-box CLI suite against a real nipad
make lint             # golangci-lint
make web-dev          # Vite dev server proxying the API
make sqlc proto mock  # regenerate sqlc, protobuf and gomock code
make desktop-test unity-test godot-test   # client integrations
```

Benchmarks and load tests (`make bench-upload`, `bench-download`,
`bench-scale`, `load-test`) are described in
[`docs/PERFORMANCE.md`](docs/PERFORMANCE.md).

## License

This project is dual-licensed:

- Community open source license: Apache License 2.0. The main project code is covered by the current [LICENSE](https://github.com/nipalab/nipa/blob/main/LICENSE).
- Commercial enterprise license: all files and folders under the `ee` directory are licensed separately under the enterprise license in [ee/LICENSE](https://github.com/nipalab/nipa/blob/main/ee/LICENSE).

The Apache 2.0 license applies to the community OSS edition. The commercial enterprise license applies only to code under the `ee` directory.
