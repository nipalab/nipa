# Nipa

A next-generation centralized Version Control System engineered for binary-heavy projects (e.g., game development, 3D assets, rich media) and large team monorepos.

`nipa` combines the strengths of Git (lightweight branching, release tags, Merge Requests, client 3-way text merges), Perforce (exclusive file locking, high-throughput binary streaming), and SVN (fine-grained path-based access control, centralized single source of truth) to provide scalable asset tracking with developer autonomy.

- **Language/Tech Stack:** Go (Golang), gRPC, Protobuf, SQLite (`modernc.org/sqlite`), FastCDC, BLAKE3
- **Architecture Model:** Client-Server Monorepo (`nipa` / `nipad`) with a local client daemon (`nipa serve`) for GUI integrations

## Quick Start

```sh
make build-server build-client   # bin/nipad + bin/nipa
# run the server (see config.yaml.sample for settings)
./bin/nipad

# clone a repository (prompts for credentials on first use)
nipa clone http://localhost:6745/org/project ./work
cd work

# edit files, then stage and commit them on the server
nipa add path/to/file
nipa status
nipa push -m "my change message"
```

The working copy tracks the branch configured at clone time. Files are split
into content-addressed chunks (FastCDC) and rehydrated from a local chunk cache,
so updates and branch switches only transfer what actually changed.

GUI clients (the desktop client, a Windows Explorer extension, game-engine
plugins) connect to a local daemon instead of spawning the CLI per event:

```sh
nipa serve   # foreground; publishes its loopback endpoint in ~/.config/nipa/daemon.json
```

## Commands

Run `nipa <command> --help` for full details.

| Command                     | Description                                                                 |
| --------------------------- | --------------------------------------------------------------------------- |
| `nipa clone <url> <target>` | Clone a repository. `-b <name>` selects the starting branch (default `main`). |
| `nipa branch`               | Show the current branch. `-a` lists all server branches, `-c <name>` creates a new branch and switches to it. |
| `nipa switch <branch>`      | Fetch the given branch, materialize its tree in the working copy, and repoint the local repository at it. `--tag <name>` checks out a release tag instead, detaching HEAD at that tag. Not allowed while changes are staged. |
| `nipa tag`                  | List the project's release tags. `-c <name> [-m <message>]` creates an immutable tag pointing at the checked-out commit (or `--branch <b>` / `--commit <id\|hash>`), `-d <name>` deletes one. |
| `nipa add <path> [...]`     | Mark files (or everything inside directories) for the next push.            |
| `nipa remove <path> [...]`  | Unmark files for the next push. `-a` unmarks everything.                    |
| `nipa status`               | Show the working copy status: `A` staged, `M` modified, `?` untracked, `!` missing, `C` conflicts. |
| `nipa lock <path> [--branch]` | Lock a tracked binary file, or a directory prefix covering a whole editing pass, so only you can land it. Locks on the default branch are project-global; `--branch` scopes the lock elsewhere. |
| `nipa lock list`            | List active binary file locks: path, scope, holder, acquisition time, and the merge request when one holds it. |
| `nipa unlock <path> [--branch]` | Release a binary file lock you hold. |
| `nipa push -m "<message>"`  | Upload staged changes to the server and commit them on the configured branch. `--dry-run` reports the would-land changes and upload estimate without contacting the server. |
| `nipa update`               | Fetch and apply the latest changes of the configured branch; while detached at a tag, re-sync the checked-out tag instead. |
| `nipa merge <branch>`       | Merge another branch into the current one. Fast-forwards when possible; `--no-ff` forces a merge commit, `--ff-only` refuses, `--abort` cancels a conflicted merge, `-m` sets the message, `--dry-run` reports the outcome and conflicts without applying. |
| `nipa revert <commit>`      | Create new commits that undo the given commit or range (`<from>..<to>`, newest first, up to 16 commits) without rewriting history. `--mainline 1\|2` for merge commits, `--no-commit` stages without committing, `-m` sets the message (single commit only), `--continue` / `--abort` / `--skip` drive a conflicted revert, `--dry-run` reports the chained conflicts and changes without applying. |
| `nipa log`                  | Show the commit history of the current branch. Interactive and scrollable when stdout is a terminal; `-n` limits, `--oneline` prints one line per commit, `--no-pager` disables the pager. |
| `nipa diff [<rev1> [<rev2>]]` | Show changes as a unified patch. With no revisions: working tree vs the last synced snapshot (offline). One revision: that tree vs the working tree. Two revisions: tree vs tree. A revision is a branch name, a tag name, a base36 commit ID (as printed by `nipa log`) or `HEAD`/`@`; `<a>..<b>` compares the two endpoints and `<a>...<b>` compares their merge base against `<b>`. Renames are detected automatically. `--staged` limits to what the next push would upload, `-U` sets the context, `--stat`/`--name-only`/`--name-status` select other formats, `-w`/`-b` ignore whitespace, `-- <path>` limits paths, `--exit-code` sets the exit status, `--ext-diff` opens each changed file in the configured external tool (`NIPA_EXTERNAL_DIFF` or `diffExternal` in `~/.config/nipa/config.json`), and `--no-pager`/`--no-color` disable the pager/colors. |
| `nipa serve`                | Run the local daemon that GUI clients connect to: loopback gRPC with a capability token, a per-clone status cache fed by a file watcher, streaming progress for long operations and a pass-through proxy for the server APIs. Publishes its port and token in `~/.config/nipa/daemon.json` (0600) and stops on SIGINT/SIGTERM, draining in-flight operations. |
| `nipa mcp [--repo <dir>] [--allow-write]` | Serve the working copy as a Model Context Protocol server over stdio so AI agents can inspect and change it directly. Read-only tools by default; mutating tools require `--allow-write`. See [AI agents and automation](#ai-agents-and-automation). |

Branch creation (`nipa branch -c <name>`) forks from the exact commit the
working copy is pinned to (clone, update, switch and push record the branch head
commit id locally; push also records its hash), falling back to the current
branch head when there is nothing pinned yet.

Release tags are immutable named pointers to one commit, with an optional
annotation message (`nipa tag -c v1.0.0 -m "first release"`); the name is freed
by `nipa tag -d`. `nipa switch --tag v1.0.0` checks the tagged tree out in a
detached state: the configured branch stays, `nipa update` re-syncs the tag,
`nipa status`, `nipa log` and `nipa diff` follow the pinned commit, and
push/merge/revert refuse until `nipa switch <branch>` returns to a branch.

Commit references are the base36 commit IDs printed by `nipa log`. A revert
moves history forward: each reverted commit produces a new commit applying its
inverse, on top of the current branch head. A conflicted revert stops with
`C` files; resolve them and run `nipa revert --continue` (for a single pending
commit a plain `nipa push` also finishes it), skip it with `nipa revert --skip`,
or discard the whole operation with `nipa revert --abort`.

`nipa diff` compares the working tree against the last synced snapshot, fully
offline: unchanged files are skipped, tracked files removed from disk are
deletions, and files staged with `nipa add` that are not in the snapshot are
additions. Untracked files are only reported by `nipa status`. The snapshot
records each file's chunk hashes, so the old side of the comparison is
reassembled from the local object cache.

With revisions, each one resolves to a commit first: a branch name is looked up
on the server, then a tag name, `HEAD`/`@` uses the locally pinned commit (the
head recorded by clone/update/switch/push) and falls back to the configured
branch head. One
revision is compared against the working tree; two are compared as trees,
downloading any missing chunks into the local cache. `<a>...<b>` compares the
merge base of the two commits against `<b>`.

Renames are detected automatically (deleted/added pairs with at least 50%
similarity) and render git's `rename from`/`rename to` headers and `R<score>`
statuses. `-w`/`-b` ignore whitespace differences across every output format.
`--ext-diff` runs an external tool once per file with git's argument contract
(`<path> <old-file> <old-hash> <old-mode> <new-file> <new-hash> <new-mode>`),
configured through `NIPA_EXTERNAL_DIFF` or `diffExternal` in
`~/.config/nipa/config.json`; the tool only runs when `--ext-diff` is given.

Tracked binary files are mandatory-lock gated: `nipa push` refuses to land a
modified or removed binary unless you hold a lock covering it, while brand-new
files only conflict with someone else's covering lock (adding a fresh asset
needs no lock unless a directory or pre-emptive lock guards it). Locks on the
default branch are project-global; locks on any other branch are scoped to that
branch, and a merge request holds locks for its changed binaries until it is
merged or closed. `nipa lock` also accepts a directory prefix to cover a whole
editing pass — your exact-path lock is released once the push it guarded lands,
while directory locks stay until you `nipa unlock` them.

## AI agents and automation

Nipa is designed to be driven by scripts and AI agents as well as humans:

- **MCP server** — `nipa mcp [--repo <dir>] [--allow-write]` serves the clone
  over stdio (Model Context Protocol) so agents such as Claude Code, Cursor or
  Copilot can work with a working copy directly. Read-only tools
  (`nipa_status`, `nipa_diff`, `nipa_log`, `nipa_branch_list`, `nipa_mr_list`,
  `nipa_lock_list`) are always registered; `nipa_add`, `nipa_push`,
  `nipa_branch_create`, `nipa_lock`/`nipa_unlock` and `nipa_mr_create` require
  `--allow-write`. Authentication never prompts (a prompt would corrupt the
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

- **Server** — `internal/`: `domain` (entities/errors), `usecase` (business
  logic), `repository` (SQLite/Postgres over sqlc), `grpc` (protobuf service +
  handlers), `http` (REST API).
- **Diff engine** — `internal/diff/` is a pure package (Myers line diff, tree
  comparison, rename detection, unified/stat/name rendering) shared by the
  client and used by the server for merge-request diffs: `TreeDiff` compares two
  commit trees (merge base vs source head, GitHub-style) and `AttachContents`
  loads file contents from the chunk store.
- **Client** — `internal/client/`: `cli` (cobra commands), `usecase`
  (clone/branch/push/update/merge/revert/diff orchestration), `grpc` (transport),
  `localrepo` (`.nipa/` local metadata + SQLite), `merge` (three-way tree merge
  + diff3), `securestorage` (keyring-backed token store), `output` (JSON DTOs
  shared by `--json` and the MCP tools), and `mcp` (`nipa mcp`, the Model
  Context Protocol server).
- **Client daemon** — `internal/client/daemon/` (`nipa serve`): a loopback gRPC
  service (proto `internal/client/grpc/proto/daemon.proto`) with token
  discovery, a per-clone operation coordinator (status/stage/diff share the
  repo, mutations are exclusive), server-streamed progress for
  update/switch/push/merge/revert, a streaming `diff`, an fsnotify watcher
  feeding a background stat-cache reconciler, and a proxy for the server APIs
  (branches, trees/commits, merge requests, file locks).
- **Client integrations** — GUI surfaces (the P4V-style desktop client, Windows
  Explorer integration, Unity/Unreal/Godot plugins) contain no VCS logic: they
  connect to the local `nipa serve` daemon and consume the same API the CLI
  does. Design notes live in
  [`docs/INTEGRATIONS.md`](docs/INTEGRATIONS.md) and
  [`docs/DAEMON.md`](docs/DAEMON.md).
- Local state lives in `.nipa/` inside the clone target: a JSON `config` with
  the repository URL, current branch, sparse prefixes and (while detached at a
  tag) the head marker, and a SQLite database tracking the tree
  snapshot (including each file's chunk hashes) and the staged file list, plus a
  content chunk cache (never erased, so updates can skip unchanged content and
  diffs can rebuild the old side).
- Content is stored server-side as FastCDC chunks addressed by BLAKE3 hash;
  the tree is a recursive manifest of directories, files and their chunk lists.
- **Web UI** (work in progress) — a Vite/React single-page app in `web/`
  using Primer React (GitHub's design system, `@primer/react`), built into
  `web/server/dist` and embedded into the `nipad` binary
  (`go:embed`), served on the same port as the REST/gRPC APIs. Run
  `make web && make build` to embed a fresh UI, or `make web-dev` for the
  Vite dev server (proxies `/auth`, `/docs`, `/api` to `localhost:6745`).


## License

This project is dual-licensed:

- Community open source license: Apache License 2.0. The main project code is covered by the current [LICENSE](https://github.com/nipalab/nipa/blob/main/LICENSE).
- Commercial enterprise license: all files and folders under the `ee` directory are licensed separately under the enterprise license in [ee/LICENSE](https://github.com/nipalab/nipa/blob/main/ee/LICENSE).

The Apache 2.0 license applies to the community OSS edition. The commercial enterprise license applies only to code under the `ee` directory.