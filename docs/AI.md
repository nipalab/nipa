# Nipa for AI agents

This page summarizes the interfaces an automated agent can use: the MCP server
(tools and prompts), the machine-readable CLI output, dry runs, client-side
text generation and the REST API. Everything is
served by the regular client and server binaries; no separate agent service
exists. The final section collects proposed AI-native directions and how they
would build on these seams.

## MCP server

```sh
nipa mcp [--repo <dir>] [--allow <caps>] [--allow-write] [--no-agent-trailer]
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
| `nipa_diff` | working-copy or revision diff with structured hunks (offline without revisions), capped by `max_bytes` |
| `nipa_log` | commit history, newest first (`limit` default 50, at most 500) |
| `nipa_branch_list` | branches with head commit and protection state |
| `nipa_tag_list` | release tags and the commit each points at |
| `nipa_lock_list` | active binary file locks |
| `nipa_lock_query` | locks covering a path (exact or directory prefix) in the branch's lock scope (default branch: mainline locks, other branches: their own), with other scopes' covering locks in `other_scopes` |
| `nipa_conflicts` | pending merge/revert: base/ours/theirs revisions and conflicted files (offline) |
| `nipa_mr_list` | merge requests, optionally filtered by status (`limit` at most 200) |
| `nipa_mr_view` | one merge request with mergeability (`blocked_by`), reviews and status checks |
| `nipa_mr_diff` | merge-request patch per file (merge base vs source head), capped by `max_bytes` |
| `nipa_mr_threads` | conversation threads (top-level and inline) with replies |
| `nipa_mr_commits` | commits the request adds, newest first |
| `nipa_mr_timeline` | activity timeline |
| `nipa_asset_diff` | structured comparison of one asset (`path`, `old_path` for renames, `old` default `snapshot`, `new` default `working`, `max_rows`, `max_changes`); `include_images` attaches old/new PNG thumbnails as MCP image content |
| `nipa_describe_changes` | one asset-aware headline per changed file, same `revisions`/`paths`/`staged`/`merge_base` arguments as `nipa_diff`; `max_files` default 50, at most 200, the rest counted in `omitted` |
| `nipa_asset_check` | check changed files against the root `.nipaassets.yml` budgets (see [Asset budgets](#asset-budgets)), same `revisions`/`paths`/`staged`/`merge_base`/`no_cache` arguments as `nipa_describe_changes`; returns `{config, old, new, violations, checked, skipped}` |
| `nipa_mr_describe_changes` | the same headlines for a merge request (merge base vs source head; `base.kind` says when the target head or branch stood in for an unknown merge base) |
| `nipa_meta_warnings` | Unity `.meta` / Godot `.import`/`.uid` sidecar lint of pending changes (`engine` `unity`, `godot`, or detected; offline) |

Mutating tools are enabled per capability with `--allow` (comma list; an
unknown name fails at startup). `--allow-write` grants every capability.

| Capability | Tools |
| --- | --- |
| `stage` | `nipa_add`, `nipa_restore` |
| `push` | `nipa_push` |
| `sync` | `nipa_update`, `nipa_switch` (branch or `tag`) |
| `branch` | `nipa_branch_create`, `nipa_tag_create` |
| `lock` | `nipa_lock`, `nipa_unlock` |
| `mr` | `nipa_mr_create`, `nipa_mr_update` (title, description, `draft` true/false = draft/ready), `nipa_mr_comment` (top-level or `file` + `new_line`/`old_line`), `nipa_mr_reply`, `nipa_mr_resolve`, `nipa_mr_review`, `nipa_mr_request_review`, `nipa_mr_check` |
| `merge` | `nipa_merge` (incl. `abort`), `nipa_resolve_conflict` (`ours`, `theirs`, `content`, `working`, `delete`), `nipa_godot_merge` (structural `.tscn`/`.tres` merge of a conflicted file, written and staged only when clean), `nipa_mr_merge` (`strategy`, `delete_source`) |

For example `nipa mcp --allow stage,mr` lets an agent stage and review but
never push or merge. Tools that discard or overwrite work (`nipa_push`,
`nipa_restore`, `nipa_update`, `nipa_switch`, `nipa_unlock`, `nipa_merge`,
`nipa_resolve_conflict`, `nipa_godot_merge`, `nipa_mr_merge`) carry the MCP `destructiveHint`
annotation so clients can ask for confirmation.

Agent attribution: text written through MCP (push messages, merge-request
descriptions, comments, replies and review bodies) gets a trailer naming the
MCP client from its `initialize` handshake, for example
`Nipa-Agent: claude-code/2.1.0`. It is skipped when the client sends no
name, when the text already has a `Nipa-Agent:` line, for empty text, and
entirely with `--no-agent-trailer`.

Output budgets keep results inside an agent's context: `nipa_diff` and
`nipa_mr_diff` take `max_bytes` (default 256 KiB, at most 2 MiB, estimated
from the line text); once the budget runs out the current file is cut and
flagged `"truncated": true` and the remaining files are counted in
`omitted_files`. Narrow the request with `paths` or raise `max_bytes` to see
more.

Asset tools sides are `working`, `snapshot` (the last synced version, offline)
or any revision token (branch, tag, base36 commit id, `HEAD`/`@`); revision
sides download chunks through the commit-scoped path, so the caller's read
filter applies. Scenes (Unity YAML, Godot text resources) report object and
property changes with GUIDs resolved from the clone's `.meta` files, images
their size/format/pixel delta, models their triangle/vertex/bounds stats,
audio duration and loudness, CSV/TSV changed cells and text line counts.
Oversized files are reported in `error` instead of being read.

Tool results carry the same JSON shapes as the CLI `--json` payloads (see
below). Mutating tools are serialized by the server. Tool failures set
`isError` and include the human message plus any `hint:` and `next:` guidance
from the underlying error.

### MCP prompts

Three read-only prompts (`prompts/list`, `prompts/get`) are always registered.
Each returns one `user` message: the instructions, a `---` separator, then the
repository data. The connected agent's own model writes the text; `nipa mcp`
calls no LLM.

| Prompt | Arguments | Content |
| --- | --- | --- |
| `nipa_commit_message` | `repo` | staged changes (offline): changed files, asset summaries, `.nipaassets.yml` violations, text diff |
| `nipa_mr_description` | `repo`, `source` (default: working copy branch), `target` (default: project default branch) | commits since the merge base, the same change data for merge base vs source |
| `nipa_review_mr` | `repo`, `number` (required) | merge request metadata, commits, existing threads (capped), change data; asks for findings with path, line and severity, never an approval |

Errors (nothing staged, unknown request, missing configuration) come back as
JSON-RPC errors with the same `hint:`/`next:` guidance as tool failures.

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
- `nipa assets check --json`

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
  an upper bound of the actual transfer. With a root `.nipaassets.yml`, the
  staged files are checked against it: JSON gains `asset_violations`
  (omitted when empty, same objects as `nipa assets check`) and the text
  output ends with a warning block. The push itself does not enforce budgets
  yet.
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

## Asset budgets

`.nipaassets.yml` at the repository root is a versioned, team-wide budget
file (like `.nipaignore`; sparse manifests always include it). Example:

```yaml
version: 1            # optional; only 1 is accepted
rules:
  - name: textures
    paths: ["Assets/Textures/**", "*.png"]
    max_texture_size: 2048   # longest side in pixels
    power_of_two: true       # width and height
    max_bytes: 8MiB
  - name: ui
    paths: ["Assets/Textures/UI/**"]
    power_of_two: false      # overrides the textures rule for UI art
  - name: characters
    paths: ["Assets/Characters/**/*.glb", "**/*.gltf"]
    max_triangles: 50000
    max_vertices: 80000
  - name: audio
    paths: ["Assets/Audio/**"]
    max_audio_seconds: 120
    max_peak_dbfs: -1.0
  - name: naming
    paths: ["Assets/**"]
    name_pattern: "^[A-Za-z0-9_\\-.]+$"   # Go regexp on the file name
```

- `paths` use the `.nipaignore` pattern syntax (`*`, `?`, `**`, `!`
  negation, leading/embedded `/` anchors to the root, a bare name matches at
  any depth, a matching directory covers everything below it).
- Every rule whose `paths` match a file applies, in file order; a later rule
  overrides only the keys it sets, and a violation names the rule that set
  the limit. `name` defaults to `rules[<index>]`.
- `max_bytes` takes an integer or a size with `B`, `KB`/`MB`/`GB` (decimal)
  or `K`/`M`/`G`/`KiB`/`MiB`/`GiB` (binary). Limits must be positive (except
  `max_peak_dbfs`), `name_pattern` must compile, unknown keys are errors with
  the line number.
- Only the new side of each change is checked; removed files are skipped.
  Image, model and audio limits decode the file with `internal/assetdiff`
  (images: PNG, JPEG, TGA, DDS, KTX2, EXR, HDR, PSD...; models: glTF/GLB/OBJ;
  audio: WAV/AIFF/FLAC/Vorbis/MP3). A covered file that cannot be decoded
  (unsupported such as FBX, too large, corrupt) is listed in `skipped` and
  only checked for `max_bytes` and `name_pattern`.
- The budget is always read from the working copy, also when two revisions
  are compared.

`nipa assets check [<rev1> [<rev2>]] [--staged] [--no-cache] [--json] [-- <path>...]`
selects the compared sides like `nipa diff` (no revision: working tree vs
the last synced snapshot, offline; `<a>..<b>` and `<a>...<b>` work). Text
output prints `path: limit: message (rule name)` per violation, skipped files
and a summary; `--json` prints
`{"config":true,"violations":[{"path","rule","limit","actual","max","message"}],"checked":N,"skipped":[{"path","reason"}]}`.
Exit code `2` when a budget is exceeded, `0` otherwise. Without the file it
prints `no .nipaassets.yml` (JSON `{"config":false,"violations":[],"checked":0}`)
and exits `0`. The MCP tool `nipa_asset_check` returns the same report.

## Client-side generation (`--generate`)

```sh
nipa push --generate [--yes] [--dry-run [--json]]
nipa mr create --generate [--yes] [--title <t>] [--source <b>] [--target <b>]
```

`nipa push --generate` drafts the commit message of the staged changes and
`nipa mr create --generate` the description (and the title unless `--title`
is given: first answer line = title, the rest = description) from the
commits since the merge base and the merge base vs source changes. The draft
is printed and confirmed with `Use it? [y/N]`; `--yes` (`-y`) skips the
question, and without a terminal the command refuses unless `--yes` is
given. `--generate` is mutually exclusive with `-m` (push) and
`-d`/`--description` (mr create). `nipa push --dry-run --generate` only
prints the draft (JSON: `generated_message` in the plan payload) and pushes
nothing. Generated text gets a `Nipa-Generated: <model>` trailer.

The prompts are the same as the MCP prompts above (`internal/client/aiprompt`,
gathered by `internal/client/aicontext`). The endpoint is any
OpenAI-compatible chat completion API: nipa sends one non-streaming
`POST <endpoint>/chat/completions` with `{model, messages: [system, user],
temperature: 0.2}`, a bearer key when one is set, a 60 s timeout and a 1 MiB
response cap. The endpoint is used as given (a trailing `/` is dropped) and
`/chat/completions` is appended, so configure the API base, including `/v1`
where the server needs it:

| Server | Endpoint |
| --- | --- |
| Ollama | `http://localhost:11434/v1` |
| vLLM / llama.cpp server | `http://localhost:8000/v1` |
| OpenAI | `https://api.openai.com/v1` |

Configuration (`~/.config/nipa/config.json`, environment wins):

```json
{ "llm": { "endpoint": "http://localhost:11434/v1", "model": "qwen2.5-coder", "apiKeyEnv": "OPENAI_API_KEY" } }
```

| Key | Environment | Meaning |
| --- | --- | --- |
| `llm.endpoint` | `NIPA_LLM_ENDPOINT` | API base URL (http or https) |
| `llm.model` | `NIPA_LLM_MODEL` | model name sent in the request and the trailer |
| `llm.apiKeyEnv` | `NIPA_LLM_API_KEY` | the config names the variable holding the key; `NIPA_LLM_API_KEY` holds the key itself and wins. Keys are never written to the config file, logs or error messages |

Without an endpoint or model `--generate` fails with
`set NIPA_LLM_ENDPOINT and NIPA_LLM_MODEL or llm in ~/.config/nipa/config.json`;
nothing is ever sent to a default or cloud endpoint.

Privacy: what leaves the machine is the prompt text only — repo-relative
paths, change statuses, text diff hunks (64 KiB in total by default, long
lines cut at 512 bytes, the rest counted as `[... N more files omitted]`),
commit subjects, merge request title/description and thread comments
(capped), structured asset summaries (for example image size, triangle
counts, loudness) and `.nipaassets.yml` violations. Binary file contents never
leave: binary files appear as a path with `[binary]`, and any patch line with
NUL bytes or invalid UTF-8 is replaced by `[non-text content omitted]`. Point
the endpoint at a local server to keep everything on the machine. The Nipa
server never calls an LLM.

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

### 1. Asset-aware MCP tools (shipped)

Shipped as `nipa_asset_diff`, `nipa_describe_changes`,
`nipa_mr_describe_changes`, `nipa_meta_warnings` and `nipa_godot_merge` (see
the MCP tables above), plus `nipa_lock_query`. They run client-side over the
working copy and the commit-scoped chunk downloads; no server changes.
Remote-only querying without a clone (`nipa_tree_manifest`, server-side MCP)
stays deferred.

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

### 3. Binary change summaries (shipped)

`internal/assetdiff` decodes both versions with `internal/preview` and turns
them into structured deltas and one-line headlines (`nipa_describe_changes`,
`nipa_mr_describe_changes`); the connected agent writes the prose. No LLM runs
inside `nipad`; automated timeline summaries remain a webhook-worker idea
(section 2).

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
- **Asset budget file** (shipped client-side, see [Asset budgets](#asset-budgets)):
  `nipa assets check`, `nipa_asset_check` and the push dry run. A webhook
  worker can run the same check as a status check; AI only explains failures.
- **Reference webhook worker**: a small first-party example (`nipa-bot`)
  demonstrating sections 2 and 3 end to end, kept out of the server binaries.
- **Server-side MCP** (streamable HTTP on `nipad`): optional later phase so
  agents can query a remote repository with no clone at all — JWT
  authenticated and permission-filtered. Bigger server surface; the stdio +
  daemon path covers most needs first.
- **Client-side generation** (shipped, see
  [Client-side generation](#client-side-generation---generate) and
  [MCP prompts](#mcp-prompts)): `nipa push --generate`, `nipa mr create
  --generate` and the `nipa_commit_message`/`nipa_mr_description`/
  `nipa_review_mr` prompts. Pure client work, no server AI.
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
