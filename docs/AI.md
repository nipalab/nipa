# Nipa for AI agents

Reference for everything an automated agent can use: the MCP server (tools
and prompts), asset budgets, client-side text generation, machine-readable CLI
output, dry runs and the REST API.

## Overview and principles

- **Client-side only.** Every AI surface is served by the regular `nipa`
  binary over a working copy. There is no agent service, and `nipad` /
  `nipad-ee` never call an LLM.
- **Structured data, not prose.** Nipa exposes facts (statuses, hunks, scene
  object changes, image and mesh stats, budget violations); the connected
  agent or a model you configure writes the text.
- **Assets are first-class.** Binary-heavy repositories are the target, so the
  tools decode Unity and Godot scenes, images, 3D models, audio and tables
  instead of reporting "binary file changed".
- **Same permissions as the user.** The agent acts with the CLI's stored
  token; the server's read filter, locks, branch protection and review gates
  apply unchanged.
- **Opt-in writes.** Mutating tools exist only for the capabilities you allow.

## Quick start

1. Log in and clone from a terminal. `nipa mcp` never prompts (a prompt would
   corrupt the JSON-RPC stream), so it needs a stored token:

   ```sh
   nipa clone http://nipa.example.com/studio/game game
   ```

2. Register the server in your MCP client. It speaks MCP over stdio.

   Claude Code, from inside the clone:

   ```sh
   claude mcp add nipa -- nipa mcp --allow mr,lock
   ```

   Cursor (`.cursor/mcp.json`) and other clients with a JSON config:

   ```json
   {
     "mcpServers": {
       "nipa": {
         "command": "nipa",
         "args": ["mcp", "--repo", "/path/to/game", "--allow", "mr,lock"]
       }
     }
   }
   ```

3. Ask the agent to call `nipa_status` or `nipa_describe_changes`.

Every tool takes an optional `repo` argument. The working copy is resolved
from `repo`, then `--repo`, then the server's working directory, each walking
up to 32 parent directories for `.nipa/config`, so one server can serve
several clones.

## Capabilities

```sh
nipa mcp [--repo <dir>] [--allow <caps>] [--allow-write] [--no-agent-trailer]
```

Read-only tools and the prompts are always registered. Mutating tools are
registered per capability with `--allow` (comma list; an unknown name fails at
startup). `--allow-write` grants every capability. A tool outside the allowed
capabilities is not listed, and calling it is a JSON-RPC error
(`unknown tool "nipa_push"`).

| Capability | Tools |
| --- | --- |
| `stage` | `nipa_add`, `nipa_restore` |
| `push` | `nipa_push` |
| `sync` | `nipa_update`, `nipa_switch` |
| `branch` | `nipa_branch_create`, `nipa_tag_create` |
| `lock` | `nipa_lock`, `nipa_unlock` |
| `mr` | `nipa_mr_create`, `nipa_mr_update`, `nipa_mr_comment`, `nipa_mr_reply`, `nipa_mr_resolve`, `nipa_mr_review`, `nipa_mr_request_review`, `nipa_mr_check` |
| `merge` | `nipa_merge`, `nipa_resolve_conflict`, `nipa_godot_merge`, `nipa_mr_merge` |

For example `--allow mr,lock` lets an agent review merge requests and manage
locks but never stage, push or merge. Mutating calls are serialized by the
server. Tools that discard or overwrite work (`nipa_restore`, `nipa_push`,
`nipa_update`, `nipa_switch`, `nipa_unlock`, `nipa_merge`,
`nipa_resolve_conflict`, `nipa_godot_merge`, `nipa_mr_merge`) carry the MCP
`destructiveHint` annotation so clients can ask for confirmation; read tools
carry `readOnlyHint`.

**Agent trailer.** Text written through MCP (push messages, merge request
descriptions, comments, replies and review bodies; not titles) gets a trailer
naming the MCP client from its `initialize` handshake:

```
Adds the feature.

Nipa-Agent: claude-code/2.1.0
```

It is skipped when the client sends no name, when the text is blank or
already has a `Nipa-Agent:` line, and entirely with `--no-agent-trailer`.

## Tool reference

Results are the structured content of the tool call and use the same JSON
shapes as the CLI `--json` payloads. Failures set `isError` and carry the
message plus any `hint:` and `next:` guidance from the underlying error.

### Read tools

| Tool | Inputs | Returns |
| --- | --- | --- |
| `nipa_status` | `no_cache` | `branch`, `head`, `staged`, `deleted`, `modified`, `untracked`, `missing`, `conflicts` (offline) |
| `nipa_diff` | `revisions` (0–2), `paths`, `staged`, `merge_base`, `unified`, `ignore_all_space`, `ignore_space_change`, `no_cache`, `max_bytes` | `changes` with structured hunks, `omitted_files`; offline without revisions |
| `nipa_log` | `limit` | `commits` of the configured branch, newest first |
| `nipa_branch_list` | | `current`, `branches` with head commit and protection |
| `nipa_tag_list` | | `tags` with the commit each points at |
| `nipa_lock_list` | | active `locks` |
| `nipa_lock_query` | `path`, `branch` | `scope` (`mainline` for the default branch, else `branch`), `locked`, covering `locks` in that scope, covering locks of other scopes in `other_scopes` |
| `nipa_conflicts` | | pending `op` (merge/revert), `base`/`ours`/`theirs`, `files` (`kind`, `binary`, `resolved`); offline |
| `nipa_mr_list` | `status`, `limit` | `merge_requests` |
| `nipa_mr_view` | `number` | `merge_request`, `mergeability` (`status`, `blocked_by`), `reviews`, `checks` of the current source head |
| `nipa_mr_diff` | `number`, `max_bytes` | `files` (merge base vs source head, unified `patch` lines), `omitted_files` |
| `nipa_mr_threads` | `number` | `threads` (top-level and inline, `resolved`, `outdated`, `comments`) |
| `nipa_mr_commits` | `number` | `commits` the request adds, newest first |
| `nipa_mr_timeline` | `number` | `timeline` events (`opened`, `pushed`, `review_submitted`, `merged`, ...) |

Revision tokens everywhere are branch names, tag names, base36 commit ids or
`HEAD`/`@`.

### Asset tools (read-only)

| Tool | Inputs | Returns |
| --- | --- | --- |
| `nipa_asset_diff` | `path`, `old_path` (renames), `old` (default `snapshot`), `new` (default `working`), `include_images`, `max_rows`, `max_changes` | one `Result`: `kind`, `status`, sizes and a per-kind block (`scene`, `image`, `model`, `audio`, `table`, `text`) or `error` |
| `nipa_describe_changes` | same as `nipa_diff` (`revisions`, `paths`, `staged`, `merge_base`, `no_cache`) plus `max_files` | `old`, `new`, one `files[]` entry per change (`path`, `status`, `kind`, `headline`), `omitted` |
| `nipa_mr_describe_changes` | `number`, `max_files` | `base`/`head` (`revision`, `kind`: which revision stood in for the merge base), `files`, `omitted` |
| `nipa_asset_check` | same as `nipa_describe_changes` without `max_files` | `.nipaassets.yml` report: `config`, `old`, `new`, `violations`, `checked`, `skipped` |
| `nipa_meta_warnings` | `engine` (`unity`, `godot`, empty = detect from `project.godot` or `ProjectSettings/ProjectVersion.txt`) | `engine`, `warnings` (`path`, `kind`: `missing-meta`, `orphan-meta`, `meta-not-staged`, `guid-changed`; Godot: `missing-sidecar`, `orphan-sidecar`, `sidecar-not-staged`, `uid-changed`), `related`, `message`); offline |

Asset sides are `working`, `snapshot` (the last synced version, offline) or a
revision. Revision sides download chunks through the commit-scoped path, so
the caller's read filter applies.

- **Scenes** (Unity Force Text YAML: `.unity`, `.prefab`, `.asset`, `.mat`,
  ...; Godot `.tscn`, `.tres`, `project.godot`, `.import`): objects added,
  removed and changed with their property changes, plus added/removed
  references. Unity GUIDs are resolved to asset paths through the clone's
  `.meta` files.
- **Images** (PNG, JPEG, GIF, BMP, TIFF, WebP, TGA, DDS, KTX2, EXR, HDR,
  PSD/PSB): size, format, changed pixels and bounds. `include_images`
  attaches old/new PNG thumbnails (at most 256 px) as MCP image content.
- **Models** (glTF/GLB, OBJ): meshes, vertices, triangles, materials,
  textures, joints, animations and bounds with deltas.
- **Audio** (WAV, AIFF, FLAC, Vorbis, MP3): format, duration, peak, loudness
  (LUFS) and clipped samples with deltas.
- **Tables** (CSV/TSV): changed rows and cells.
- **Text and other files**: line counts or size/hash deltas.

Files over the per-kind limit (16 MiB scenes and tables, 256 MiB images,
models and audio) are reported in `error` instead of being read.

### Write tools

| Tool | Cap | Inputs | Notes |
| --- | --- | --- | --- |
| `nipa_add` | `stage` | `paths` | returns the status |
| `nipa_restore` | `stage` | `paths` | discard working changes of tracked paths |
| `nipa_push` | `push` | `message` | returns `branch`/`tag`/`commit_id` |
| `nipa_update` | `sync` | | |
| `nipa_switch` | `sync` | `branch` or `tag` | a tag detaches |
| `nipa_branch_create` | `branch` | `name` | forks from the pinned commit and switches |
| `nipa_tag_create` | `branch` | `name`, `message`, `branch` or `commit` | |
| `nipa_lock` / `nipa_unlock` | `lock` | `path`, `branch` | the default branch locks project-wide |
| `nipa_mr_create` | `mr` | `title`, `description`, `source`, `target`, `draft`, `reviewers`, `assignees` | |
| `nipa_mr_update` | `mr` | `number`, `title`, `description`, `draft` (true = draft, false = ready) | |
| `nipa_mr_comment` | `mr` | `number`, `body`, `file` + `new_line` / `old_line` | top-level without `file` |
| `nipa_mr_reply` | `mr` | `number`, `thread_id`, `body` | |
| `nipa_mr_resolve` | `mr` | `number`, `thread_id`, `unresolve` | |
| `nipa_mr_review` | `mr` | `number`, `state` (`approved`, `changes_requested`, `commented`), `body` | authors may only comment on their own request |
| `nipa_mr_request_review` | `mr` | `number`, `user_id` | |
| `nipa_mr_check` | `mr` | `number`, `name`, `state` (`pending`, `success`, `failed`), `details_url`, `commit` | defaults to the commit the working copy is pinned to |
| `nipa_merge` | `merge` | `source`, `ff_only`, `no_ff`, `message`, `abort` | conflicts come back in `conflicts` with a `next` hint |
| `nipa_resolve_conflict` | `merge` | `path`, `choice` (`ours`, `theirs`, `content`, `working`, `delete`), `content` | |
| `nipa_godot_merge` | `merge` | `path` (`.tscn`/`.tres`) | structural three-way merge of a conflicted Godot file; a clean merge is written and staged as resolved, otherwise nothing is written and `conflicts` are listed |
| `nipa_mr_merge` | `merge` | `number`, `strategy` (`ff`, `merge`, `squash`, `rebase`), `delete_source` | |

### Output budgets

Results are capped so they fit an agent's context:

| Tool | Default | Maximum |
| --- | --- | --- |
| `nipa_diff`, `nipa_mr_diff` `max_bytes` | 256 KiB | 2 MiB |
| `nipa_log` `limit` | 50 | 500 |
| `nipa_mr_list` `limit` | 50 | 200 |
| `nipa_describe_changes`, `nipa_mr_describe_changes` `max_files` | 50 | 200 |
| `nipa_asset_diff` `max_rows` | 200 | 2000 |
| `nipa_asset_diff` `max_changes` | 500 | 5000 |

Diff budgets are estimated from the line text. Once a budget runs out the
current file is cut and flagged `"truncated": true` and the remaining files
are counted in `omitted_files` (summaries: `omitted`; scenes: `truncated`).
Narrow the request with `paths` or raise the limit to see more.

## Prompts

Three prompts (`prompts/list`, `prompts/get`) are always registered. Each
returns one `user` message: the instructions, a `---` separator, then the
repository data in tagged sections (`<changed_files>`, `<asset_summaries>`,
`<budget_violations>`, `<diff>`, and for merge requests `<merge_request>`,
`<description>`, `<commits>`, `<threads>`). The connected agent's model writes
the text; `nipa mcp` calls no LLM.

| Prompt | Arguments | Content |
| --- | --- | --- |
| `nipa_commit_message` | `repo` | the staged changes (offline) |
| `nipa_mr_description` | `repo`, `source` (default: working copy branch), `target` (default: project default branch) | commits since the merge base and the merge base vs source changes |
| `nipa_review_mr` | `repo`, `number` (required) | merge request metadata, commits, existing threads and its changes; asks for findings with path, line and severity, never an approval |

Errors (nothing staged, unknown request, invalid number) are JSON-RPC errors
with the same `hint:`/`next:` guidance as tool failures.

## Asset budgets

`.nipaassets.yml` at the repository root is a versioned, team-wide budget
file. Like `.nipaignore`, sparse checkouts always receive it.

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
  negation, a leading or embedded `/` anchors to the root, a bare name
  matches at any depth, a matching directory covers everything below it).
- Every rule whose `paths` match a file applies, in file order; a later rule
  overrides only the keys it sets, and a violation names the rule that set
  the limit. `name` defaults to `rules[<index>]`.
- `max_bytes` takes an integer or a size with `B`, `KB`/`MB`/`GB` (decimal)
  or `K`/`M`/`G`/`KiB`/`MiB`/`GiB` (binary). Limits must be positive (except
  `max_peak_dbfs`), `name_pattern` must compile, and unknown keys are errors
  with the line number.
- Only the new side of each change is checked; removed files are skipped. A
  covered file that cannot be decoded (an unsupported format such as FBX, too
  large or corrupt) is listed in `skipped` and only checked for `max_bytes`
  and `name_pattern`.
- The budget is always read from the working copy, also when two revisions
  are compared.

Surfaces:

- `nipa assets check [<rev1> [<rev2>]] [--staged] [--no-cache] [--json] [-- <path>...]`
  selects the sides like `nipa diff` (no revision: working tree vs the last
  synced snapshot, offline; `<a>..<b>` and `<a>...<b>` work). Text output
  prints `path: limit: message (rule name)` per violation; `--json` prints
  `{"config":true,"violations":[{"path","rule","limit","actual","max","message"}],"checked":N,"skipped":[{"path","reason"}]}`.
  Exit code `2` when a budget is exceeded, `0` otherwise (also without the
  file: `{"config":false,"violations":[],"checked":0}`).
- `nipa push --dry-run [--json]` checks the staged files and adds
  `asset_violations` (JSON) or a warning block (text). The push itself does
  not enforce budgets.
- The MCP tool `nipa_asset_check` returns the same report, and the prompts
  include violations in `<budget_violations>`.

## Generation (`--generate`)

```sh
nipa push --generate [--yes] [--dry-run [--json]]
nipa mr create --generate [--yes] [--title <t>] [--source <b>] [--target <b>]
```

`nipa push --generate` drafts the commit message for the staged changes;
`nipa mr create --generate` drafts the description (and the title unless
`--title` is given: the first answer line becomes the title) from the commits
and changes since the merge base. The draft is printed and confirmed with
`Use it? [y/N]`; `--yes` (`-y`) skips the question, and without a terminal the
command refuses unless `--yes` is given. `--generate` cannot be combined with
`-m` (push) or `-d`/`--description` (mr create). `nipa push --dry-run
--generate` only prints the draft (JSON: `generated_message`). Generated text
gets a `Nipa-Generated: <model>` trailer. The prompts are the MCP prompts
above.

The endpoint is any OpenAI-compatible chat completion API: one non-streaming
`POST <endpoint>/chat/completions` with `{model, messages: [system, user],
temperature: 0.2}`, a bearer key when one is set, a 60 s timeout and a 1 MiB
response cap. Configure the API base including `/v1` where the server needs
it:

| Server | Endpoint |
| --- | --- |
| Ollama | `http://localhost:11434/v1` |
| vLLM / llama.cpp server | `http://localhost:8000/v1` |
| OpenAI | `https://api.openai.com/v1` |

Configuration in `~/.config/nipa/config.json` (the environment wins):

```json
{ "llm": { "endpoint": "http://localhost:11434/v1", "model": "qwen2.5-coder", "apiKeyEnv": "OPENAI_API_KEY" } }
```

| Key | Environment | Meaning |
| --- | --- | --- |
| `llm.endpoint` | `NIPA_LLM_ENDPOINT` | API base URL (http or https) |
| `llm.model` | `NIPA_LLM_MODEL` | model name sent in the request and the trailer |
| `llm.apiKeyEnv` | `NIPA_LLM_API_KEY` | the config names the variable holding the key; `NIPA_LLM_API_KEY` holds the key itself and wins. Keys are never written to the config file, logs or error messages |

Without an endpoint or model `--generate` fails with `set NIPA_LLM_ENDPOINT
and NIPA_LLM_MODEL or llm in ~/.config/nipa/config.json`. There is no default
endpoint: nothing is sent anywhere unless you configure it.

## CLI JSON, dry runs and REST

**JSON output.** These commands accept `--json`, and `NIPA_OUTPUT=json`
enables it globally: `nipa status`, `nipa diff`, `nipa log`, `nipa branch`
(`-a`), `nipa tag` (listing), `nipa lock list`, `nipa mr list|view|diff|comments|timeline|requests|checks`,
`nipa assets check` and `nipa version`. Payloads are one JSON object on
stdout. Errors are one envelope on stderr:

```json
{"error":{"code":409,"message":"binary file \"a.psd\" requires a lock","hint":"...","action":"nipa lock a.psd"}}
```

`code` uses HTTP semantics (400 input, 401 auth, 403 permission, 404 missing,
409 locked/blocked). Exit codes are stable: `0` success, `1` generic failure
(and `diff --exit-code` with differences), `2` blocked by a lock or
precondition (and asset budget violations), `127` not found.

**Dry runs.** `push`, `merge` and `revert` accept `--dry-run` (with `--json`).
A dry run reports file-level changes, conflicts and, for push, the upload
estimate; it never stages, commits, pushes, rewrites working files or saves
pending state.

- `nipa push --dry-run` is offline. `upload_objects` / `upload_bytes` count
  chunks missing from the local object cache (an upper bound of the
  transfer), plus `asset_violations`.
- `nipa merge <branch> --dry-run` resolves the merge base and both trees on
  the server and reports `up_to_date`, `fast_forward` and the would-land
  changes; conflicts do not fail the command.
- `nipa revert <commit|range> --dry-run` evaluates each target in memory,
  chaining them like the real sequence, and stops at the first conflicting
  target. It cannot be combined with `--continue`, `--abort`, `--skip` or
  `--no-commit`.

Merge and revert dry runs may download content and cache intermediate merge
results in the local object cache; the working copy, staging and metadata are
untouched.

**REST API.** The server publishes its OpenAPI document at `/docs/api.json`
(Swagger UI at `/docs/`), generated from the route definitions. Routes live
under `/api/v1/orgs/{org}/projects/{project}/...` and cover browsing,
branches, tags, merge requests, reviews, checks, file locks, permissions and
groups. Authenticate with a Bearer access token, or for CI with a project
service token (`nipa_st_...`, scopes `read`, `checks:write`,
`webhooks:write`; see `docs/WEBHOOKS.md`).

## Security and privacy

- **What leaves the machine.** MCP tools talk only to the Nipa server the
  clone points at, with the user's token; what the MCP client then sends to
  its model is up to that client. `--generate` sends one prompt to the
  configured endpoint: repo-relative paths, change statuses, text diff hunks
  (64 KiB in total, long lines cut at 512 bytes, the rest counted as
  `[... N more files omitted]`), commit subjects, merge request title,
  description and thread comments (capped), structured asset summaries and
  budget violations. Binary contents never leave: binary files appear as a
  path with `[binary]`, and any patch line with NUL bytes or invalid UTF-8 is
  replaced by `[non-text content omitted]`. Point the endpoint at a local
  server to keep everything on the machine.
- **Permissions are inherited.** The agent can only read what the user's
  path rules allow (revision content goes through the same permission-filtered
  downloads) and only write what the server accepts from that user: locks,
  branch protection, required reviews and status checks still apply, and an
  author still cannot approve their own merge request. Grant the smallest
  `--allow` set the task needs, or run with a dedicated, less privileged
  account.
- **Attribution.** `Nipa-Agent:` (MCP writes) and `Nipa-Generated:`
  (`--generate`) trailers keep machine-written text visible in history and
  review threads.
- **Repository content is untrusted data.** File contents, commit messages,
  merge request descriptions and comments are written by other people and can
  contain instructions aimed at the agent (prompt injection). Tool results and
  prompt sections are data, never commands; keep destructive capabilities off
  for agents that read untrusted merge requests, and let the MCP client
  confirm `destructiveHint` tools.

## Roadmap

Deferred, not implemented:

- **Reference webhook worker (`nipa-bot`).** An external worker reacting to
  `mr.synchronized` / `mr.check_reported`, running `nipa assets check` and the
  review prompt, and reporting status checks and comments. It needs new
  service-token scopes for merge request diffs, blob reads and comments
  (today: `read`, `checks:write`, `webhooks:write`).
- **Server-side MCP.** Streamable HTTP on `nipad` so agents can query a
  repository without a clone, JWT-authenticated and permission-filtered.
- **Search.** Full-text search over paths and decoded text (SQLite FTS5
  first), vector search only once asset metadata is rich enough; indexing
  asynchronous and outbox-driven, never in the push path.
- **Server-side asset metadata.** Per-format extractors run asynchronously at
  confirm time into a sidecar table, powering server-side budgets, summaries
  and search.

Guardrails for any of these: no LLM calls in the server core, nothing
O(entire repo) in request paths, the free/enterprise boundary unchanged, and
every surface honors the caller's permission-filtered read scope.

## Related documentation

- `docs/LOCKING.md` — binary lock rules, including the push gate.
- `docs/BRANCH_MERGING.md` — merge, revert and merge request semantics.
- `docs/WEBHOOKS.md` — webhook payloads and service token scopes.
- `docs/DAEMON.md` — the loopback daemon (`nipa serve`).
- `AGENTS.md` — repository-wide engineering context.
