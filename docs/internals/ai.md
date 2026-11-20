# MCP server and generation internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for `nipa mcp` (reference in `docs/AI.md`): `internal/client/mcp` is a
go-sdk MCP server over stdio (`Serve`/`NewServer`; `cmd/nipa/mcp.go` wires a
`nonInteractiveInput` prompter, since a login prompt would corrupt stdout).
`Options.NewUseCases` (`newMCPUseCases`) builds a fresh `UseCases` graph per
call (one gRPC client binds one host) whose `Close` releases the client and
localrepo handles; the working copy is the `repo` argument, then `--repo`,
then the cwd (`FindRepoRootFrom`). Read tools (`tools_read.go`,
`tools_mr.go`, `tools_asset.go`, `tools_provenance.go`) are always registered; write tools only per
capability in `Options.Allow` (`ParseAllow`, unknown names fail; `--allow-write`
= `AllCaps`): `stage` add/restore, `push`, `sync` update/switch, `branch`
branch/tag create, `lock` lock/unlock, `mr` create/update/comment/reply/
resolve/review/request-review/check, `merge` merge/resolve-conflict/
`nipa_godot_merge`/`nipa_mr_merge`; an unregistered tool is a JSON-RPC
error. Writes are serialized by `writeMu` and annotated `DestructiveHint`
when they discard or overwrite work. `nipa_lock` takes a `ttl` (default
10m); `keepalive.go` re-locks every TTL lock the server took every ttl/3
(re-lock = renew, injectable ticker; a permanent covering lock is not
tracked, entries are matched by lock id), drops a lock on a successful
`nipa_unlock` (a failed non-404 unlock keeps renewing), a failed renew
(`slog.Warn`) or when a push landed it (`forgetLanded`), and stops on
shutdown, so a crashed agent frees its locks within one TTL. `withAgentTrailer` appends
`Nipa-Agent: <clientInfo.name>/<version>` from the initialize handshake to
push messages, MR descriptions, comments, replies and review bodies (not
titles; skipped for blank text, an existing trailer or `--no-agent-trailer`).
Budgets (`budget.go`): `capDiff`/`capMRDiff` trim `nipa_diff`/`nipa_mr_diff`
to `max_bytes` (default 256 KiB, max 2 MiB; `truncated` per file,
`omitted_files`), `nipa_log` 50/500, `nipa_mr_list` ≤200, summaries 50/200
files, asset tables 200/2000 rows, scene changes 500/5000. Asset tools read
both sides through `assetscan.BlobLoader` over `usecase.Diff.ReadBlob`
(working/snapshot offline, revisions through the commit-scoped download) with
`assetdiff.WorkingGUIDs`; `nipa_meta_warnings` lints status through
`unitymeta`/`godotmeta`; `nipa_asset_refs`/`nipa_asset_impact` run
`assetscan.Refs`/`Impact` (asset references flow below); `nipa_godot_merge` runs `assetdiff.MergeGodot` on the
pending merge's base/ours/theirs and resolves only a clean result. Prompts
(`prompts.go`) are always registered and call no LLM. `toolError` appends the
domain `hint:`/`next:` to failures. Unit tests stub the usecases
(`mcp_test.go`, `phase1_test.go`, `tools_asset_test.go`, `prompts_test.go`);
e2e in `tests/suite/mcp_test.go` + `mcp_workflow_test.go`.

Flow for generation (reference in `docs/AI.md`; the server never calls an
LLM): `internal/client/aiprompt` (pure, deterministic) builds
`Prompt{System, User}` for `CommitMessage`, `MRDescription` and `ReviewMR`
from a `Changeset` (`FileText` patch lines via `FromFileDiffs`/`FromMRDiff`,
`assetdiff` summaries, `assetbudget` violations, diff text capped at
`DefaultDiffBytes` 64 KiB with `[... N more files omitted]` markers; lines with
NUL/invalid UTF-8 become `[non-text content omitted]`, binary files only show
their path), plus `CleanOutput`/`SplitTitle`/`AddTrailer`
(`Nipa-Generated: <model>`). `internal/client/aicontext.Collector` gathers the
input over small interfaces (`Differ`, `MRReader`, `History`, `Connector`):
`Commit` (staged diff, offline), `MRDescription` (merge-base diff + source
commits since the merge base, default target = the branch with `IsDefault`)
and `Review` (MR view/diff/commits/threads, sides from `MRSides`, shared with
the MCP asset tools). The MCP prompts `nipa_commit_message`,
`nipa_mr_description` and `nipa_review_mr` (`mcp/prompts.go`, always
registered) return one user message (system text, `---`, user text).
`internal/client/generate` holds the non-interactive part: `Generator`
(`LLM`/`Sources` seams; `Model`, `CommitInput`, `MRInput`, `Commit`,
`MergeRequest`), `CommitMessage`/`MergeRequestDraft`/`Text` (clean output,
`SplitTitle` for MRs, `Result{Title, Text, Model, Source, Target}` without the
trailer), `Ping`, and `Settings`/`NewLLM`/`Default` (`config.ResolveLLM`,
then the `securestorage` `llm-api-key` keyring entry — the `NIPA_TOKEN_FILE`
entry in file-backed mode — read only once endpoint+model are set, errors =
no key; a missing endpoint/model wraps `ErrNotConfigured`). `nipa push
--generate` and `nipa mr create --generate` (`cli/generate.go`, flags, trailer
placement and `y/N` only) call it; the model is `internal/client/llm` — one non-streaming OpenAI-compatible
`POST <endpoint>/chat/completions` (endpoint used as-is, trailing `/` trimmed,
60 s timeout, 1 MiB response cap, key redacted from errors) — configured by
`config.ResolveLLM` (`llm: {endpoint, model, apiKeyEnv}` in the client config,
`NIPA_LLM_ENDPOINT`/`NIPA_LLM_MODEL`/`NIPA_LLM_API_KEY` win, key order
`NIPA_LLM_API_KEY` > `apiKeyEnv` > keyring, keys never in the config file;
`config.SetLLM` writes endpoint/model and keeps the rest);
the draft is confirmed with `y/N` (`--yes` skips, non-TTY refuses) and
`push --dry-run --generate` only prints it (`generated_message` in JSON).

