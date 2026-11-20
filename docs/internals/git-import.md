# Git import internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for `nipa import git <git-path-or-url> <nipa-url>` (design and deviations
in `docs/GIT_IMPORT.md`, how-to in `docs/CLIENT_GUIDE.md`): the pure
`internal/gitimport` (no client/server imports) opens the repository with
go-git (`Source`), plans the commits of the refs matching `--refs` (default
`refs/heads/*,refs/tags/*`) parents first, cut by `--depth`/`--since` or one
root commit per tip with `--snapshot`, octopus parents beyond two dropped with a
warning (`Planner`, `Options.Known` stops at origins the server has), and
turns each commit into the delta against parent 1 (`Converter`: blobs through
`chunker.Encode` like push, memoized by (git sha, profile) through a
`BlobCache`; symlinks skipped or stored as files, submodules skipped,
`DryRun` report with blobs/bytes, invalid paths, LFS objects).
`internal/gitimport/lfs` parses pointers, reads the local stores, discovers the
endpoint (`lfs.url`, `.lfsconfig`, `remote.<r>.lfsurl`, the remote URL) and
downloads through the batch API with `git credential` helpers (`--lfs
auto|local|off`; missing objects stop `local` and endpoint-less `auto` runs
before any write). The client runner `internal/client/gitimport` (`Importer.Run`)
mirrors a remote URL into `<user cache>/nipa/git-import/<sha256(redacted
url)>` (`remote.go`; `file://` through an in-process go-git transport, scp form
needs `user@`), keeps `<git dir>/nipa-import/state.db` (`state.go`: `blobs`
memo with an `uploaded` flag, `lfs` record, `meta` target checked against
host/org/project/source key, `--reset-state`), and drives the server through
`Sink` over `Transport` (`sink.go`): `ImportLookup` first (also in a dry run;
it requires project admin, so writers are refused before any upload), the
dry-run report, `ImportLookup` with `estimated_upload_bytes` (the report's
bytes; a 402 stops a real run before the prompt and marks a dry run
`quota_exceeded`), then per commit the chunk upload through
`usecase.ChunkUploader` and the commit on one `ImportStream` (pipelined two
commits deep), and finally `ImportSetRefs`. `--author-map` (`Name <email> =
user-id` or `<email> = user-id`) sets `author_user_id`. Server
`usecase.Import` (`import.go`): `CheckAccess` (project admin + writable store)
on `Lookup`, `EstimateUpload` (`Chunk.CheckUploadHeadroom` →
`StorageLedger.Headroom`, nothing without a ledger), the first stream header
and `SetRefs`; `Commit` validates the origin, resolves up to two parent
origins, builds the tree from parent 1 (`buildNewTree`, push checks), picks
the author user (mapped org member, else the org member with that verified
email, else the importer) and writes through `PushRepository.ApplyImport`
(no branch move, locks, hooks or audit; an existing origin returns the stored
commit). Imported commits fill the `commits` columns `author_name`,
`author_email` and `origin` (sqlite `000025`, postgres `000012`, partial
unique index `commits_project_origin (project_id, origin) WHERE origin IS NOT
NULL`, `CommitsByOrigins`), store the author date in `created_at` and hash
with `treehash.CommitHashWithOrigin`. An origin is
`<system>:<source>:<native-id>[:<qualifier>]` (`domain.ParseOrigin`: systems
`git`/`hg` hex ids, `svn`/`p4` decimal + qualifier, 512 bytes), `git:<source
key>:<sha>` for Git (`--source-key`, default `default`). `SetRefs` runs in one
transaction under `LockProject`: branches `created`/`updated` (fast-forward
only with `update`)/`unchanged`/`exists`/`diverged`/`protected`/`invalid`,
tags `created`/`unchanged`/`exists`/`invalid` (tags never move), the default
branch `updated`/`unchanged`/`invalid`; it records `project.git_imported`
(`auditedWrite`) and after the commit emits one `push` webhook per created or
moved branch with an empty file list and `tag.created` per new tag. Readers
show imported commits through `CommitLogEntry`/`CommitDetail`/`CommitWalkEntry`
`author_name`/`author_email`/`origin` (REST DTOs too): `nipa log` prints the
Git author and `(imported <short sha>)`, blame the author, the web UI
`ImportedLabel` ("Imported from Git" + short id) and the desktop
`ImportedBadge`. Tests: `internal/gitimport` and `lfs` unit tests,
`internal/client/gitimport` against a fake server, `internal/e2e/import_test.go`,
`ee/e2e/import_test.go` and `tests/suite/import_git_test.go`.

