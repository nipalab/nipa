# Nipa for AI agents

This page summarizes the interfaces an automated agent can use: the MCP server,
the machine-readable CLI output, dry runs and the REST API. Everything is
served by the regular client and server binaries; no separate agent service
exists.

## MCP server

```sh
nipa mcp [--repo <dir>] [--allow-write]
```

`nipa mcp` runs a Model Context Protocol server over stdio (JSON-RPC on
stdout, diagnostics on stderr). Register it as a local stdio server in any MCP
client. stdin/stdout are reserved for the protocol: authentication never
prompts, so log in with a normal CLI command in the clone first (for example
`nipa clone` or `nipa update`). Tokens come from the same store as the CLI.

Working-copy resolution order for every tool: the `repo` argument, then
`--repo`, then the process working directory (walking up to 32 parent
directories for `.nipa/config`).

Read-only tools (always available):

| Tool | Purpose |
| --- | --- |
| `nipa_status` | staged/deleted/modified/untracked/missing/conflicting paths |
| `nipa_diff` | working-copy or revision diff with structured hunks (offline without revisions) |
| `nipa_log` | commit history, newest first |
| `nipa_branch_list` | branches with head commit and protection state |
| `nipa_mr_list` | merge requests, optionally filtered by status |
| `nipa_lock_list` | active binary file locks |

Mutating tools (only with `--allow-write`):

| Tool | Purpose |
| --- | --- |
| `nipa_add` | stage paths for the next push |
| `nipa_push` | commit staged changes and upload them |
| `nipa_branch_create` | create a branch and switch the local clone to it |
| `nipa_lock` / `nipa_unlock` | acquire or release a binary file lock |
| `nipa_mr_create` | open a merge request |

Tool results carry the same JSON shapes as the CLI `--json` payloads (see
below). Mutating tools are serialized by the server. Tool failures set
`isError` and include the human message plus any `hint:` and `next:` guidance
from the underlying error.

## CLI JSON output

Read commands accept `--json`, and `NIPA_OUTPUT=json` enables it globally:

- `nipa status --json`
- `nipa diff --json` (structured hunks: `old_start`, `old_lines`,
  `new_start`, `new_lines`, per line `kind` (`context`/`add`/`delete`), `old`,
  `new`, `text`, `no_newline`)
- `nipa log --json`
- `nipa branch --json` and `nipa branch -a --json`
- `nipa tag --json` (listing; `-c`/`-d` reject `--json`)
- `nipa lock list --json`
- `nipa mr list --json`

Payloads are emitted on stdout as one JSON object with a trailing newline.
Errors are emitted on stderr as a single envelope:

```json
{"error":{"code":409,"message":"binary file \"a.psd\" requires a lock","hint":"...","action":"nipa lock a.psd"}}
```

`code` uses HTTP-style semantics (400 user input, 401 auth, 403 permission,
404 missing, 409 locked/blocked). Exit codes are stable: `0` success, `1`
generic failure (and `diff --exit-code` when differences exist), `2` blocked
by a lock or precondition, `127` not found.

## Dry runs

`push`, `merge` and `revert` accept `--dry-run` (and `--json` together with
it). A dry run reports the file-level changes, conflicts and, for push, the
upload estimate. It never stages, commits, pushes, rewrites working files or
saves pending state.

- `nipa push --dry-run [--json]` is fully offline. `upload_objects` /
  `upload_bytes` count chunks missing from the local object cache, so they are
  an upper bound of the actual transfer.
- `nipa merge <branch> --dry-run [--json]` contacts the server to resolve the
  merge base and both trees. It reports `up_to_date`, `fast_forward` and the
  would-land changes; conflicts are reported without failing the command.
- `nipa revert <commit|range> --dry-run [--json]` evaluates each target in
  memory, chaining them like the real sequence, and stops reporting at the
  first conflicting target. `--dry-run` cannot be combined with `--continue`,
  `--abort`, `--skip` or `--no-commit`.

Merge and revert dry runs may download missing content and cache intermediate
merge results in the local object cache to detect text conflicts; the working
copy, staging state and metadata are untouched.

## REST API

The server exposes an OpenAPI document at `/docs/api.json` (Swagger UI at
`/docs/`). Routes live under `/api/v1/orgs/{org}/projects/{project}/...` and
cover browsing, branches, tags, merge requests, reviews, file locks, permissions
and groups. Authenticate with a Bearer access token. The OpenAPI spec is generated
from the route definitions, so it is always current.

## Related documentation

- `docs/LOCKING.md` — binary lock rules, including the push gate.
- `docs/BRANCH_MERGING.md` — merge, revert and merge-request semantics.
- `docs/DAEMON.md` — the loopback daemon (`nipa serve`).
- `AGENTS.md` — repository-wide engineering context.
