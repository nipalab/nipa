# Nipa for AI agents

This page summarizes the interfaces an automated agent can use: the MCP server,
the machine-readable CLI output, dry runs and the REST API. Everything is
served by the regular client and server binaries; no separate agent service
exists. The final section collects proposed AI-native directions and how they
would build on these seams.

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

## AI-native directions (proposal)

Evaluations of proposed AI features plus follow-up ideas. The guiding
principle: Nipa stays the system of record and exposes structured data; AI
lives at the edges (MCP tools, webhook workers), never in the server core or
request path.

### 1. Asset-aware MCP tools (build first)

The current MCP tools are workflow tools bound to a local working copy. What
is missing for agents is asset-centric remote querying, served through the
daemon's gRPC proxy so a multi-gigabyte repository can be inspected without a
full clone:

| Proposed tool | Purpose | Backing seam |
| --- | --- | --- |
| `nipa_tree_manifest` | list a subtree with `mode`, `size_bytes`, `is_binary`, `chunk_hashes` for a branch/tag/commit, narrowed by path prefixes | `GetTreeManifest` (paths, recursive, `commit_id`) |
| `nipa_lock_query` | report locks covering a path (exact or directory prefix) and their holders | `ListFileLocks` filtered client-side |
| `nipa_asset_fetch` | fetch one asset's content for agent-side analysis | presigned `GetChunkDownloadUrls` (commit-scoped) |
| `nipa_describe_binary_changes` | structured delta per changed binary between two revisions | manifests + chunk-overlap comparison (see 3) |

All four are client-side (`internal/client/mcp` plus the existing gRPC
transports); no server changes. Every tool must honor the caller's read
filter, like `Branch.VisibleChunks` already does.

### 2. Asset quality linters as external workers

The plumbing ships today: status checks (`ReportMergeRequestCheck`, exposed as
`nipa mr check`) feed branch protection's `require_status_checks`, and
webhooks (`mr.synchronized`, `mr.check_reported`) plus commit-scoped
presigned downloads let an external worker see exactly what changed. A linter
is therefore a separate daemon, not server code:

webhook → download changed binaries → format-specific analysis →
`POST .../merge-requests/{id}/checks` → merge gate blocks on failure.

This shape works for both editions and keeps AI (and GPU workloads) out of
`nipad`. Caveat: chunk hashes say *which bytes changed*, not "120k polygons" —
per-format metadata extractors (FBX, blend, texture headers) are the real
work; AI is the judgment layer on top of extracted metadata, not the chunk
reader. Pair the worker with an asset budget file (below) so linting is
deterministic first.

### 3. Binary change summaries via chunk deltas

The machinery for a format-agnostic v0 already exists: `diff.DetectRenames`
computes chunk-overlap similarity, and manifests carry per-file
`chunk_hashes`. The `nipa_describe_binary_changes` MCP tool returns structured
deltas (chunk overlap percentage, size delta, encoding change) and the
connected agent writes the prose summary itself. No LLM runs inside `nipad`;
if automated summaries on the timeline are wanted later, the webhook worker
from section 2 posts them as comments or check output.

### 4. Semantic search — metadata first, vectors later

A vector index is heavy (embedding pipeline, new storage, push-path sync) and
only as good as the metadata underneath. A pragmatic v0:

- SQLite FTS5 over paths plus decoded text chunks (`chunker.Decode` exists
  server-side; text is zstd-compressed, cheap to decode).
- Asset metadata from extractors (below) as indexed documents.
- Vector search only once extractors produce metadata rich enough to embed.

Indexing must be asynchronous and outbox-driven (like `email_deliveries`),
never in the push request path.

### Additional suggestions

- **Asset metadata layer** (`internal/assetmeta`): pluggable per-format
  extractors (png/exif, fbx, gltf, blend, psd, ...) run asynchronously at
  confirm time into a sidecar table. This is the backbone powering linting
  budgets (2), summaries (3) and search (4).
- **Asset budget file** (`.nipaassets.yml` at the repo root, versioned like
  `.nipaignore`): polycount, texture resolution and naming rules per path.
  The linter enforces them deterministically; AI only explains failures.
- **Reference webhook worker**: a small first-party example (`nipa-bot`)
  demonstrating sections 2 and 3 end to end, kept out of the server binaries.
- **Server-side MCP** (streamable HTTP on `nipad`): optional later phase so
  agents can query a remote repository with no clone at all — JWT
  authenticated and permission-filtered. Bigger server surface; the stdio +
  daemon path covers most needs first.
- **Client-side generation**: `nipa commit`/`nipa mr create --generate`
  drafting messages from diff data. Pure client work, no server AI.
- **Privacy stance**: studios will not ship unreleased assets to cloud AI.
  Workers must support local models and a rules-only (no-AI) mode; Nipa core
  never calls an LLM.

### Suggested build order

1 → 3 (MCP tools) → 2 with the budget file and reference worker → asset
metadata layer → 4 (FTS first, vectors later).

### Guardrails

- No LLM calls in the `nipad`/`nipad-ee` core; AI lives in MCP tools and
  external workers.
- Nothing O(entire repo) in request paths; indexing and extraction run
  through the async outbox pattern.
- Free/enterprise boundary respected: `internal/` never imports `ee/`.
- Every new surface honors the caller's permission-filtered read scope.

## Related documentation

- `docs/LOCKING.md` — binary lock rules, including the push gate.
- `docs/BRANCH_MERGING.md` — merge, revert and merge-request semantics.
- `docs/MERGE_REQUEST_GAPS.md` — audit of missing merge-request features for
  later revisit.
- `docs/DAEMON.md` — the loopback daemon (`nipa serve`).
- `AGENTS.md` — repository-wide engineering context.
