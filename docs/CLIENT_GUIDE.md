# Using the Nipa client

This guide covers the `nipa` command-line client: logging in, the daily
working-copy loop, and every command with its important flags. It assumes a
server is already running and a project exists; see
[SERVER_GUIDE.md](SERVER_GUIDE.md) for the server side.

## Build and install

```sh
make build-client     # bin/nipa
# or: make build       # web UI + server + client
```

Put `bin/nipa` somewhere on your `PATH`. Check the client and the server it
talks to with:

```sh
nipa version
nipa version --server http://localhost:6745
```

## Connect and log in

Every command that talks to the server logs in implicitly when it has no stored
session. You can also log in explicitly:

```sh
nipa login --host http://localhost:6745
```

`nipa login` prompts for the email and password. Flags and alternatives:

| Flag / source | Meaning |
| --- | --- |
| `--host` | Server URL or host. Defaults to the host of the enclosing working copy. |
| `--username` | Email for password login. When omitted, it is prompted; SSH keys identify their own account. |
| `--ssh-key` | Private key path. Without it, the client tries `NIPA_SSH_KEY`, the `sshKeyPath` user config, ssh-agent, then `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`. |

SSH-key login is attempted first when a usable key is found and falls back to
the password prompt. A key passed explicitly (`--ssh-key` or `NIPA_SSH_KEY`)
that fails is a hard error rather than a silent fallback. See
[SSH.md](SSH.md) for registering keys on the server.

Sessions are stored in the OS keyring, keyed by server host. In headless
environments set `NIPA_TOKEN_FILE=/path/to/tokens.json` before any command to
use a file-backed store instead (the same file is read and written by every
`nipa` invocation).

## Clone and the daily loop

```sh
nipa clone http://localhost:6745/acme/rocket ./rocket
cd rocket

# edit files
nipa status
nipa add src/main.go assets/level.blend
nipa status
nipa push -m "Add the first level"
nipa update
```

`nipa clone <url> <target>`:

- URL format: `[scheme://]host[:port]/<org>/<project>[/<path>]`.
- `-b, --branch <name>` selects the starting branch (default `main`).
- `--sparse <prefix,...>` checks out only the given directory prefixes
  (repeatable, comma separated). A URL path (`.../project/assets`) also acts as
  a sparse prefix.
- The target must be empty or not exist yet.

`nipa add` marks files, and directories recursively, for the next push. Files
that are unchanged are skipped cheaply. Paths are **relative to the repository
root**, even when you run the command from a subdirectory. Missing tracked
paths are staged as deletions.

`nipa push` uploads the staged content, creates a commit on the configured
branch, and records the new head locally. `-m` is required. `--dry-run`
reports the file changes and upload estimate without contacting the server.

`nipa update` fetches and applies the current branch (or, while detached at a
tag, re-syncs that tag), materializing only changed files.

## Working-copy commands

### `nipa status`

Shows the working copy state, using these markers:

| Marker | Meaning |
| --- | --- |
| `A` | Staged for the next push |
| `D` | Staged deletion (missing on disk) |
| `M` | Modified but not staged |
| `?` | Untracked |
| `!` | Tracked but missing on disk |
| `C` | Conflict from a merge or revert |

Flags: `--no-cache` rehashes every tracked file instead of trusting the stat
cache, `--json` prints the machine-readable form. The header shows the current
branch, or `HEAD detached at tag "..."` while a release tag is checked out.

### `nipa add <path> [...]`

Stages new and modified paths, and stages missing tracked paths as deletions.
Directories expand recursively. Files matched by the ignore rules are skipped;
`-f, --force` stages them anyway (they then stay tracked).

### `nipa remove <path> [...]`

Unstages paths. `-a, --all` unstages everything.

### `nipa restore <path> [...]`

Discards local changes to the given files/directories by restoring them from the
last synced tree; deleted tracked files come back. It refuses paths that are
staged ("push or reset them first"). Restore is offline and does not touch the
server.

## Branches, switching and tags

### `nipa branch`

| Command | Effect |
| --- | --- |
| `nipa branch` | Print the current branch. |
| `nipa branch -a` | List all server branches; the current one is marked `*`. |
| `nipa branch -c <name>` | Create a branch from the pinned commit and switch to it. |
| `nipa branch -d <name>` | Delete a branch (protected branches are refused). |

`--json` is available for the current branch and the list form.

### `nipa switch <branch>` / `nipa switch --tag <name>`

Materializes another branch (or release tag) in the working copy and updates the
local configuration. Switching is refused while changes are staged. Switching to
a branch clears any detached-tag marker.

`nipa switch --tag v1.0.0` checks out the tagged commit in a **detached** state:
the configured branch stays, `nipa update` re-syncs the tag, `nipa log`,
`nipa diff` and `nipa status` follow the pinned commit, but `push`, `merge` and
`revert` refuse until you switch back to a branch.

### `nipa tag`

| Command | Effect |
| --- | --- |
| `nipa tag` | List tags (name, commit, creation time, message). |
| `nipa tag -c <name> [-m <message>]` | Create an immutable tag at the checked-out commit. |
| `nipa tag -c <name> --branch <b>` | Tag the head of a branch instead. |
| `nipa tag -c <name> --commit <id\|hash>` | Tag a specific commit (base36 id or hex hash). |
| `nipa tag -d <name>` | Delete a tag, freeing the name. |

Tag names are project-scoped and unique; `--json` is available when listing.

## Merge and revert

### `nipa merge <branch>`

Merges another branch into the current one with a three-way tree merge. It
fast-forwards when the current branch has not moved; otherwise it creates a
merge commit (text conflicts use diff3 markers).

| Flag | Meaning |
| --- | --- |
| `--no-ff` | Always create a merge commit. |
| `--ff-only` | Refuse instead of creating a merge commit. |
| `--abort` | Cancel a merge that stopped on conflicts and restore the working copy. |
| `-m, --message` | Merge commit message. |
| `--dry-run` | Report the outcome and conflicts without applying anything. |

On conflicts the files are left with markers and `nipa status` shows `C <path>`.
Resolve them, then `nipa push` finishes the merge. `nipa merge --abort` discards
the pending merge.

### `nipa revert <commit>`

Creates new commits that undo the given commit or range without rewriting
history.

```sh
nipa revert 1k2j3h4           # one commit (base36 id from nipa log)
nipa revert 1k2j3h4..1k2j9z0  # commits reachable from the right side, excluding the left; newest first, max 16
nipa revert <merge-commit> --mainline 1
```

| Flag | Meaning |
| --- | --- |
| `--mainline 1\|2` | Required when reverting a merge commit. |
| `--no-commit` | Apply the inverse of a single commit and stage it without committing. |
| `-m, --message` | Message for the revert commit (single-commit revert only). |
| `--continue` | Resolve conflicts and continue after a stopped step. |
| `--skip` | Drop the conflicting commit and continue. |
| `--abort` | Restore the pre-revert tree and pin. |
| `--dry-run` | Report conflicts and changes without applying. |

A conflicted single-commit revert can also be finished with a plain
`nipa push`; multi-target sequences must use `nipa revert --continue` or
`--abort`.

## History and inspection

### `nipa log`

Shows the current branch history (interactive and scrollable on a terminal).
`-n, --limit <n>` limits the count, `-o, --oneline` prints one line per commit,
`--no-pager` prints plainly, `--json` emits structured data. While detached at a
tag, the log walks from the tag's commit.

### `nipa diff [<rev1> [<rev2>]] [--] [<path>...]`

| Invocation | Comparison |
| --- | --- |
| `nipa diff` | Working tree vs the last synced snapshot, fully offline. |
| `nipa diff <rev>` | That revision's tree vs the working tree. |
| `nipa diff <rev1> <rev2>` | Tree vs tree. |
| `nipa diff <a>..<b>` | The two endpoints. |
| `nipa diff <a>...<b>` | Merge base of `a` and `b` vs `b` (three-dot). |

A revision is a branch name, a tag name, `HEAD`/`@` (the locally pinned
commit), or a base36 commit id. Paths must come after `--`.

| Flag | Meaning |
| --- | --- |
| `-U, --unified <n>` | Context lines (default `3`). |
| `--stat` | Per-file summary. |
| `--name-only` | Changed paths only. |
| `--name-status` | Paths with `A`/`M`/`D`/`R` status. |
| `--staged` | Only what the next push would upload. |
| `-w` / `-b` | Ignore all whitespace / changes in whitespace amount. |
| `--ext-diff` | Run the configured external diff tool per file. |
| `--exit-code` | Exit `1` when differences exist. |
| `--no-cache` | Re-read every working file instead of trusting the stat cache. |
| `--no-color`, `--no-pager` | Disable colors or the interactive pager. |
| `--json` | Structured output (cannot be combined with other formats or `--ext-diff`). |

Renames are detected automatically for every output format. Untracked files are
not part of a bare `nipa diff`; `nipa status` lists them.

The external diff tool is configured with `NIPA_EXTERNAL_DIFF` or the
`diffExternal` key in `~/.config/nipa/config.json`, and is called with git's
seven arguments: `<path> <old-file> <old-hash> <old-mode> <new-file>
<new-hash> <new-mode>`.

## Merge requests

All `nipa mr` commands read the organization and project from the current
working copy. `<number>` is the merge request number shown by `mr list`.

| Command | Purpose |
| --- | --- |
| `nipa mr create --title <t> [-d <desc>] [--source <b>] [--target <b>] [--draft]` | Open a request. Source defaults to the current branch, target to the project default branch. |
| `nipa mr list [--status open\|merged\|closed\|draft] [--author <id>] [--source <b>] [--target <b>] [--search <text>] [--assignee <id>] [--after <n>] [--limit <n>]` | List requests; `--after` continues from the last number of the previous page. |
| `nipa mr view <n>` | Detail: status, mergeability/blockers, assignees, commits, review summary and description. |
| `nipa mr update <n> [--title <t>] [-d <desc>] [--draft=false]` | Edit title/description or toggle draft. |
| `nipa mr ready <n>` | Mark a draft ready for review. |
| `nipa mr assign <n> <user-id>...` | Replace the assignees (base36 user ids). |
| `nipa mr close <n>` / `nipa mr reopen <n>` | Close or reopen. |
| `nipa mr merge <n> [--strategy ff\|merge\|squash\|rebase] [--delete-source]` | Merge the request. `ff` is the default; source deletion is best-effort. |
| `nipa mr diff <n>` | Three-dot diff (merge base vs source head). |
| `nipa mr checks <n>` | Status checks reported for the current source head. |
| `nipa mr check <n> --name <name> --state pending\|success\|failed [--url <link>]` | Report a status check. |
| `nipa mr review <n> --approve\|--request-changes -m <text>` | Submit a review decision (no flag = comment-only). |
| `nipa mr comments <n>` | List comment threads. |
| `nipa mr comment <n> -m <text> [--file <path> --new-line <n>\|--old-line <n>]` | Start a conversation or inline thread. |
| `nipa mr reply <n> <thread-id> -m <text>` | Reply in a thread. |
| `nipa mr resolve <n> <thread-id> [--unresolve]` | Resolve or reopen a thread. |
| `nipa mr timeline <n>` | Activity timeline. |
| `nipa mr requests <n>` | Pending review requests. |
| `nipa mr request-review <n> <user-id>` / `nipa mr unrequest-review <n> <user-id>` | Add or withdraw a reviewer request. |

`mr list`, `view`, `diff`, `comments`, `timeline`, `requests` and `checks`
accept `--json`.

Merge is gated by the target branch configuration: a draft request, live
`changes_requested` reviews, missing required approvals/reviewers, or failing
required status checks block it with a 409. Pushing to the source branch
dismisses stale approvals (unless the branch carries them over) and starts a
clean status-check slate.

## Binary file locks

Tracked binaries must be locked before they can land. Lock before editing:

```sh
nipa lock assets/textures/hero.png          # current branch scope
nipa lock assets/textures/ --branch main    # directory prefix, explicit branch
nipa lock list
nipa unlock assets/textures/hero.png
```

Rules:

- Modifying or removing a tracked binary requires a lock covering it at push
  time.
- Adding a brand-new binary needs no lock unless someone else's directory or
  pre-emptive lock covers it (a push then fails with the holder's name).
- Locks on the default branch are project-global (`mainline`); locks on other
  branches are scoped to that branch.
- An exact-path lock is released automatically when the push it guarded lands;
  directory locks stay until you unlock them.
- A merge request holds locks for its changed binary paths until it is merged or
  closed.

`nipa lock list` accepts `--json`. See [LOCKING.md](LOCKING.md) for the full
design.

## Ignore rules

Two files are honored when walking the working tree:

| File | Scope |
| --- | --- |
| `.nipaignore` at the repository root | Versioned and shared with the team. |
| `.nipa/ignore` | Clone-local, never tracked. |

The syntax is a pragmatic gitignore subset: `#` comments, `!` negation,
`*`/`?`, `**` across directories, leading or embedded `/` anchoring to the
root, and a trailing `/` for directories only; the last matching rule wins.
Local rules override shared ones.

Ignored files are hidden from `nipa status` and skipped by `nipa add`, except
for files that are already tracked or staged (tracking always wins).
`nipa add -f <path>` stages an ignored file explicitly. Sparse checkouts always
include the root `.nipaignore`.

## Sparse checkout

Limit a large repository to the directories you work with:

```sh
nipa sparse-checkout list
nipa sparse-checkout set src assets
nipa sparse-checkout add tools
nipa sparse-checkout remove assets
nipa sparse-checkout disable     # restore the full working copy
```

Each command re-syncs the working copy immediately. Files outside the set are
removed from disk but stay on the server; adding a prefix back downloads only
what is missing. Push and update work with the sparse set, and pushes use the
pinned head commit as the base so the server applies the delta to the full tree.
`merge` and `revert` require a full checkout; run `nipa sparse-checkout disable`
first.

## Permissions and groups

### `nipa acl`

Path-based access rules for the current project (project admin required).

| Command | Effect |
| --- | --- |
| `nipa acl list` | List rules: id, subject (`user:`/`group:`), path prefix, permissions. |
| `nipa acl grant --user <id> --path <prefix> --permission read,write` | Grant a rule to a user. |
| `nipa acl grant --group <id> --path <prefix> --permission read,write` | Grant a rule to a group. |
| `nipa acl revoke --id <rule-id>` | Delete a rule. |
| `nipa acl my` | Show your effective project permission and matching rules/defaults. |

Permissions are comma-separated: `read` (1), `write` (2), `lock` (4), `admin`
(65536). An empty `--path` means the whole project.

### `nipa acl permission`

Project-wide defaults per path prefix:

```sh
nipa acl permission list
nipa acl permission set --path assets --permission read,lock
nipa acl permission remove --path assets
```

Rules granted to users or groups override the defaults. Organization owners get
full access to every project in their organization; plain members need explicit
rules. See [PBAC.md](PBAC.md).

### `nipa group`

Organization groups (organization admin required), used as ACL subjects:

```sh
nipa group create artists --description "Art team"
nipa group list
nipa group add-member --group <group-id> --user <user-id>
nipa group remove-member --group <group-id> --user <user-id>
```

## Local daemon and AI agents

### `nipa serve`

Runs the loopback gRPC daemon that GUI clients (desktop client, Explorer
integration, engine plugins) connect to. It publishes `{pid, port, token,
version}` in `~/.config/nipa/daemon.json` (mode `0600`) and stops on
`SIGINT`/`SIGTERM`, draining in-flight work.

| Flag | Meaning |
| --- | --- |
| `--port <n>` | Bind a specific loopback port (default `0` = pick a free port). |
| `--endpoint <path>` | Discovery file path (default `~/.config/nipa/daemon.json`). |

See [DAEMON.md](DAEMON.md).

### `nipa mcp`

Serves the working copy over stdio as a Model Context Protocol server for AI
agents. Read-only tools are always registered; mutating tools require
`--allow-write`. `--repo <dir>` selects the working copy (defaults to the
current directory). Interactive prompts are disabled; log in normally first.
See [AI.md](AI.md).

## Scripting and machine-readable output

- `--json` on read commands: `status`, `diff`, `log`, `branch`, `tag`, `lock
  list`, `version`, and `mr list/view/diff/comments/timeline/requests/checks`.
- `NIPA_OUTPUT=json` makes JSON the default for those commands.
- `--dry-run [--json]` on `push`, `merge` and `revert` reports the plan without
  changing local state or contacting the server. `--json` on these commands is
  only valid together with `--dry-run`.
- Payloads go to stdout. Failures are one JSON envelope on stderr:
  `{"error":{"code","message","hint","action"}}`.
- Exit codes are stable:

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Generic failure (including `diff --exit-code` with differences) |
| `2` | Locked or precondition failed (HTTP 409), e.g. a missing binary lock |
| `127` | Not found (HTTP 404) |

## Client configuration

User configuration lives in `~/.config/nipa/config.json` (respects
`XDG_CONFIG_HOME`):

```json
{
  "diffExternal": "meld",
  "uploadWorkers": 16,
  "sshKeyPath": "~/.ssh/id_ed25519"
}
```

| Key | Environment override | Meaning |
| --- | --- | --- |
| `diffExternal` | `NIPA_EXTERNAL_DIFF` | Command used by `nipa diff --ext-diff`. |
| `uploadWorkers` | `NIPA_UPLOAD_WORKERS` | Pin concurrent chunk uploads; when unset the client tunes concurrency automatically between 4 and 64 (starting at 16). |
| `sshKeyPath` | `NIPA_SSH_KEY` | Default private key for SSH login. |

The environment always wins over the file. Other useful variables:
`NIPA_TOKEN_FILE` (file-backed token store), `NIPA_OUTPUT=json`,
`NIPA_TRACING` (client tracing, see [TRACING.md](TRACING.md)).

## Local state

A clone keeps everything under `.nipa/`:

| Path | Contents |
| --- | --- |
| `.nipa/config` | Repository URL, current branch, sparse prefixes, and the detached-tag marker. |
| `.nipa/nipa.db` | Snapshot of the synced tree, chunk lists, staging state and stat cache. |
| `.nipa/objects/` | Content-addressed chunk cache. Never erased; lets update skip unchanged content and lets diff rebuild the old side offline. |

Paths in commands are repository-root relative. The client searches up to 32
parent directories for `.nipa/config`, so most commands work from a
subdirectory.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `not a nipa repository (or any of the parent directories)` | Run the command inside a clone. |
| A command unexpectedly prompts for credentials | No stored session for that host. Log in once with `nipa login --host <url>`, or set `NIPA_TOKEN_FILE` in headless environments. |
| `changes are staged` when switching | Push the staged changes or unstage them (`nipa remove -a`) before `nipa switch`. |
| `binary file ... requires a lock` | `nipa lock <path>` (or cover it with a directory lock) before pushing. |
| `... is locked by <holder>` | Someone else's lock covers the path; ask them to unlock or use another path. |
| `merge`/`revert` refuses in a sparse clone | `nipa sparse-checkout disable`, merge or revert, then re-enable. |
| `push`, `merge` or `revert` refuses while detached at a tag | `nipa switch <branch>` to re-attach first. |
| `nipa update` did nothing on a large clone | Expected: unchanged files are skipped using the stat cache. Use `nipa status --no-cache` / `nipa diff --no-cache` to force a rehash. |
| Diff viewer does not open | `--ext-diff` is required, and a command must be configured via `NIPA_EXTERNAL_DIFF` or `diffExternal`. |

## Where to go next

- Server setup and first-time admin tasks: [SERVER_GUIDE.md](SERVER_GUIDE.md)
- Full agent-facing reference (JSON payloads, MCP tools): [AI.md](AI.md)
- Daemon protocol and GUI integration: [DAEMON.md](DAEMON.md), [INTEGRATIONS.md](INTEGRATIONS.md)
- SSH login: [SSH.md](SSH.md)
- Locks, permissions, merge requests in depth: [LOCKING.md](LOCKING.md), [PBAC.md](PBAC.md), [BRANCH_MERGING.md](BRANCH_MERGING.md)
