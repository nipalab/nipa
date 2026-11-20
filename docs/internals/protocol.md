# Proto, gRPC and chunk transfer internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

## Proto / gRPC

- Source is `internal/grpc/proto/server.proto`; service `NipaService` with
  `LoginWithUsernamePassword`, `LoginWithRefreshToken`; branch RPCs
  `GetListBranch`, `GetBranch`, `GetBranchByName`, `GetDefaultBranch`,
  `CreateBranch`, `RenameBranch`, `DeleteBranch`, `SetDefaultBranch`,
  `SetBranchProtection`; tag RPCs `ListTags`, `GetTagByName`, `CreateTag`,
  `DeleteTag`; history/tree RPCs `GetTreeManifest`, `GetCommitLog`,
  `GetCommit`, `WalkCommits`, `GetMergeBase`, `MergeFastForward`; merge-request
  RPCs `CreateMergeRequest`, `UpdateMergeRequest`, `ListMergeRequests`,
  `GetMergeRequest`, `MergeMergeRequest`, `CloseMergeRequest`,
  `ReopenMergeRequest`, `CheckMergeRequest`, `ListMergeRequestCommits`,
  `GetMergeRequestDiff`; review RPCs
  `SubmitMergeRequestReview`, `ListMergeRequestReviews`,
  `GetMergeRequestReviewState`, `WithdrawMergeRequestReview`,
  `DismissMergeRequestReview`, `ListMergeRequestThreads`,
  `AddMergeRequestComment`, `ReplyMergeRequestThread`,
  `UpdateMergeRequestComment`, `DeleteMergeRequestComment`,
  `ResolveMergeRequestThread`, `DeleteMergeRequestThread`,
  `ListMergeRequestReviewRequests`, `RequestMergeRequestReview`,
  `RemoveMergeRequestReviewRequest`, `ListMergeRequestTimeline`, `StreamMergeRequestDiff`; transfer RPCs `Push`, `PushStream`, `StreamTreeManifest`,
  `GetChunkUploadUrls`, `GetChunkDownloadUrls`, `ConfirmChunkUploads`; file-lock
  RPCs `LockFile`, `UnlockFile`, `ListFileLocks`; browsing RPCs
  `ListMyProjects` (readable projects of the caller's orgs) and
  `ListDirectory` (one directory of a branch or commit, via `Branch.TreeAt`),
  `ListProjectMembers` (reviewer/assignee candidates who can read the project);
  import RPCs `ImportLookup`, `ImportStream` (bidirectional) and
  `ImportSetRefs` (see the Git import flow);
  PBAC
  RPCs `GetMyPermissions`, `CreatePBACRule`, `ListPBACRules`, `DeletePBACRule`,
  `ListProjectPathPermissions`, `SetProjectPathPermission`,
  `DeleteProjectPathPermission`; group RPCs `CreateGroup`, `ListGroups`,
  `AddGroupMember`, `RemoveGroupMember`.
  `rpc` `GetTreeManifest` takes
  `{context{org,project}, branch, path, tree_hash?, recursive, paths,
  commit_id?}` (`paths` = directory prefixes to include, empty = whole tree;
  when `commit_id` is set the manifest is built from that commit's tree — the
  commit must belong to the project, and `tree_hash` is then ignored) and
  returns
  `{branch, root_tree}`. `TreeManifest` carries `tree_hash`, `path`, `sub_trees`,
  `files` (`path, mode, size_bytes, is_binary, chunk_hashes, encoding,
  chunk_sizes`; `chunk_sizes` parallels `chunk_hashes` and is filled by
  `domainFileToPB` from the chunk rows `ListFilesByTree` already joins, so it
  costs no query, and left empty unless every chunk has a size). `GetBranch` takes
  `branch_id` (base36) and `GetBranchByName` takes `name`; both return the branch
  with its head `commit_id`. `CreateBranch` takes `from_branch` and/or
  `from_commit_id`/`from_commit_hash` (exact fork point, base36/hex).
  `GetCommitLog` takes `{context, branch, start_commit_id?, limit, path?}`
  (`path` switches to `Branch.PathCommitLog`: only commits that changed that
  file or folder, scan capped at 500 commits); `ListTags` defaults a missing
  limit to 100.
  `GetCommit` takes
  `{context, commit_id}` (base36; **ID only**, no hash alias) and returns the
  commit detail plus its recursive tree manifest; `WalkCommits` takes
  `{context, start_commit_id, stop_commit_id?, limit}` and returns
  `CommitWalkEntry`s newest-first, both-parents, stop-exclusive. `GetMergeBase`
  takes branch names and/or optional `target_commit_id`/`source_commit_id`
  (base36, take precedence) so a merge base can be resolved for commit pairs;
  `omit_tree` (`Branch.GetMergeBaseCommits`) leaves `merge_base_tree` empty,
  and `client/grpc.GetMergeBase` always sets it and streams the tree, so the
  answer never carries a whole tree (absent = the old inline tree).
  All are project-scoped in the usecase because `CommitGet`/`CommitGetByHash`
  have no project filter, and commit IDs are server-local (snow) identifiers.
- **Chunk transfer is presigned HTTP, not gRPC streaming.** `GetChunkUploadUrls`
  (project write; refs carry `hash` + exact `size_bytes`) and
  `GetChunkDownloadUrls` (`commit_ids` + optional `paths` prefixes + `hashes`;
  the visible set is the union of the referenced commit trees narrowed by the
  prefixes — `Branch.VisibleChunks(projectID, commitIDs, paths)`, cached per
  caller, project, commits and paths in `visibleChunkCache` (2 min idle TTL,
  `CHUNK_VISIBILITY_CACHE_HASHES` hashes, default 2M, one walk for concurrent misses) so a clone's download windows walk
  the trees once; a path rule change reaches them after the TTL, project access
  is checked on every call — and requested hashes are filtered against it first) return one
  page of signed paths built by `internal/chunkurl` (`/api/chunks/{org}/{project}/{hash}
  ?op=put|get&size=&exp=&sig=`, HMAC-SHA256 with `CHUNK_URL_SIGNING_KEY`,
  constant-time verify, TTL `CHUNK_PRESIGN_TTL_SECONDS` default 3600s) — or
  absolute backend presigned URLs when the store implements
  `storage.DirectTransferStore` (S3). The
  client then PUTs/GETs bytes directly over HTTP through an adaptive transfer
  limiter (`internal/client/grpc/transfer.go`): concurrency starts at 16, is
  auto-tuned within [4, 64] from observed throughput, and can be pinned with
  `NIPA_UPLOAD_WORKERS` or `uploadWorkers` in `~/.config/nipa/config.json`
  (pinned disables tuning). `ConfirmChunkUploads` measures the stored content
  size server-side (never a client-supplied value) and writes the chunk
  metadata rows, so `Push` can rely on them; linked chunks without a proof take
  their size from the row in one batched `LinkedProjectChunkSizes` lookup
  (presign checks every ref with 16 concurrent `Exists` calls, so a lost
  object is uploaded again); confirmations are
  serialized client-side under a mutex so concurrent upload windows never stack
  SQLite metadata writes. `UploadChunks` presigns, claims and confirms a window
  in batches of `uploadBatch` (1000) chunks, so a window of many small files
  never builds a presign or confirm message near the gRPC size limit. Chunks the project is already linked to come back with `already_stored` so
  clients skip the transfer. A project may reference a chunk only after it proved it
  holds the bytes (`internal/chunkproof`): `GetChunkUploadUrls` hands every
  chunk the project is not linked to an HMAC-signed, project- and hash-bound
  `proof_challenge`, the client answers with `BLAKE3(challenge || bytes)` in
  `ConfirmChunkUploads.proofs`, and the server reads the stored object back,
  checks it against its hash and the proof, and links the project in one batch
  (`project_chunk_uploads`). The client claims with proofs first, so content the
  server already holds for another project is linked without being sent again;
  only chunks reported missing are uploaded and then confirmed with their proofs.
  Unproven hashes are reported missing, `ApplyPush` joins through the link table,
  and server-generated merge content is linked via `AttributeProjectChunks`.
  The client pushes through the client-streaming `PushStream` only (the unary
  `Push` stays for older clients; an older server answers `Unimplemented`,
  reported with an upgrade hint): `PushStreamHeader` first, then
  `PushStreamFiles`/`PushStreamRemoved` batches of about 1 MiB
  (`internal/client/grpc/push.go`, raw 32-byte hashes) and `PushStreamChunks`
  continuations that append to the last file when its chunk list does not fit
  one part. The stream carries its own bearer token, refresh and rate-limit
  retry (`Client.callAuthed`, no unary interceptor). The server
  (`internal/grpc/server/push_stream.go`) runs `usecase.Push.CheckTarget`
  (write access, message, branch, protection, `base_commit_id`) on the header
  before reading the file list, caps files plus removed paths at
  `PUSH_MAX_FILES` (default 2000000, `Push.WithMaxFiles`, also applied to the
  unary `Push`) and the encoded manifest at 1 GiB, and applies the push only
  after the client closed the stream. The maintenance stream interceptor checks
  the first message of a client stream for its org/project. The header carries
  `base_commit_id` (base36; preferred over
  `base_tree_hash`, so sparse/permission-filtered clones push deltas against the
  full server tree) and `parent_2_commit_hash` for merge commits. The daemon
  loopback (`daemon.MaxMessageBytes`, 256 MiB) applies to the daemon server,
  the JSON bridge client and the desktop connector. `Transport.Connect` adds a
  `MaxRecvMessageBytes` (64 MiB) default `MaxCallRecvMsgSize` so remaining
  unary answers (`WalkCommits`, `ListFileLocks`, ...) are not cut at gRPC's
  4 MiB default (caller dial options still override it); the server limits
  are unchanged. Manifests come the same
  way: the client reads every tree through the server-streaming
  `StreamTreeManifest` (`GetTreeManifestRequest` in, `TreeManifestPart`s
  out; `internal/grpc/server/manifest_stream.go` writes about 1 MiB parts,
  `internal/client/grpc/manifest.go` rebuilds a `pb.TreeManifest`):
  entries are depth first, a `TreeManifestDir` (depth 0 = root, its parent is
  the last directory one level up) followed by its own `TreeManifestFile`s,
  `TreeManifestChunks` continue the last file's chunk hashes and sizes, no
  entry means no tree. The first part carries the branch and, for a recursive
  request of a whole commit (no paths), the `CommitDetail` from
  `Branch.GetCommit`, so the client's `GetCommit` is one streamed call. The
  daemon's `ProxyTreeManifest`/`ProxyCommitGet` stream through the optional
  `manifestStreamer` method of the `ProxyConn` (`withProxyConn`). The unary
  `GetTreeManifest`/`GetCommit` stay for older clients; the maintenance
  stream interceptor checks a server stream's request like a unary one.
  Pushes never store an empty directory: a rebuilt directory left without
  files or subdirectories is dropped (cascading to its parents) and a kept
  child with the empty-tree hash is not carried over, and
  `Branch.loadTreeManifest` skips nodes with the empty-tree hash without a
  query, since the content-node lookup of an empty hash scans every row
  sharing it.
  `Push.pushStaged` scans into 16MB windows handed to
  two uploader goroutines (queue depth 2), so hashing overlaps with the network;
  window chunks are cached locally before upload (text and pinned raw files
  only) and the scanner only blocks
  when the bounded queue is full. Chunk sizes are content-aware
  (`internal/chunker/profile.go`): text and in-place-edited formats (`.blend`,
  `.psd`, `.mb`, `.sqlite`, ...) keep the 64KB default, packed assets (textures,
  audio, video, models, archives) use 4MB average chunks, unknown binaries 1MB;
  the server accepts refs up to `chunker.MaxChunkSize()` (16MB). Text is
  zstd-compressed per file (`chunker.Encode`): files up to 8MB raw become one
  compressed chunk (≤4MB), larger text falls back to CDC chunks of the plaintext
  (256KB avg) compressed individually. Every file carries an `encoding` column
  (`internal/chunker.EncodingRaw`/`EncodingZstd`, proto `FileNode.encoding`,
  `files.encoding` on both server and localrepo); hashes always refer to the
  stored bytes, and every client hashing path (push, add/status, diff,
  merge/revert materialization, guarded remove) must go through
  `chunker.Encode`/`chunker.ConfigForFile`, passing the tracked encoding for
  stored files (`storedEncoding`), so file hashes stay consistent; decode at
  assembly points with `chunker.Decode` (client materialization,
  `diff.LoadContent`, merge's `loadFileContent`). Decoding is per chunk: the
  shared zstd decoder caps one chunk's plaintext at
  `chunker.MaxDecodedChunkSize` (8MB single-blob max + 1MB headroom), so a
  decompression bomb fails with `zstd.ErrDecoderSizeExceeded` before its output
  is allocated; `DecodeAppend` decodes into `dst`'s spare capacity because the
  library counts `dst`'s length against the cap, and the server merge
  (`loadMergedContent`) appends chunk by chunk instead of decoding the joined
  file. The signed routes live in a `/api/chunks` webService (go-restful
  `os.Exit`s on duplicate root paths so it cannot share `/api/v1`) and are NOT
  behind the Bearer filter — the signature is the capability. HTTP PUT pins the
  exact size (Content-Length check + `MaxBytesReader` + BLAKE3 verify) and only
  writes content (`Chunk.StoreUploaded`, no DB writes, avoiding SQLite lock
  contention from concurrent PUTs); DB metadata and project links are written at
  confirm time. `ApplyPush` (both repositories) resolves every chunk a push
  references in one project-scoped lookup before it writes anything (a chunk the
  project is not linked to fails with `missing chunk data for <hash>`), then
  inserts the files and their chunk links in bulk through
  `internal/repository/pushbatch`: one JSON document per batch of 5000 files or
  20000 links, expanded by `json_each` on sqlite and `jsonb_array_elements` on
  postgres, with ids matched back by `(tree_id, name)`. A 10k-file push is a
  handful of statements instead of ~30k round trips.
  `DownloadChunksStream` is `DownloadChunks` plus an optional
  `domain.ChunkStream`: a chunk its `Wants` picks is not buffered, the
  response body (capped at `chunker.MaxChunkSize()`) is handed to `Write`,
  which verifies and places it and runs concurrently on the transfer workers.
  An attempt whose body read failed is retried and calls `Write` again for the
  same chunk; an error of `Write` itself is final. `treeManifest` (every
  `Get*TreeManifest`) assembles the stream directly into the domain tree
  (`treeAssembler`), with no protobuf/hex copy; `StreamTreeManifest` still
  returns the protobuf tree for the daemon proxy and `GetCommit`.
  `internal/client/grpc/chunks.go`'s `UploadChunks`/`DownloadChunks` take the
  `ChunkScope{Org, Project, CommitIDs, Paths}` that scopes download visibility;
  download callbacks are
  serialized under a mutex because transfers run concurrently (the client
  usecase's `downloadChunks` hands each delivered chunk to a worker pool that
  verifies its hash and passes it to a sink: `StoreChunks`, a direct
  `WriteAt` into working-file temp files, or both). Downloads request
  signed URLs in windows of 1000 hashes (the server's default page size) and
  prefetch the next window while the current one transfers, so URLs never wait
  behind a long clone past their TTL and the worker pool is fed continuously
  (`runChunks`). A transient per-chunk failure (network error, truncated body,
  5xx, 408, 429) is retried up to 3 attempts with a 250 ms doubling backoff plus
  jitter (uploads too: PUTs are content-addressed and idempotent); a 429 has its
  own budget (10 retries honouring `Retry-After`, capped at 30 s) and halves the
  transfer concurrency, and unary RPCs rejected by the server rate limiter are
  repeated the same way (`internal/client/grpc/retry.go`); 4xx answers (expired or unknown URL) fail immediately, a GET body (or
  `Content-Length`) over `chunker.MaxChunkSize()` fails immediately without a
  retry (`fetchChunk`/`readChunkBody` read at most that many bytes and
  preallocate from a valid `Content-Length`), and a chunk
  reaches the callback only after a complete read, so a retry never delivers it
  twice. The server only
  returns relative paths: the client connects with the address from
  `domain.NipaUrl.Origin` (`scheme://host[:port]`; a bare host keeps cleartext
  h2c, `https://` dials TLS with system roots) and `Client.httpURL` prefixes
  `http://` when the target has no scheme. Tokens are still keyed by the bare
  host (`domain.HostForAddress`); `Auth`/`Session` normalize any address they
  receive so the transport may carry a scheme while `securestorage` never does.
- **Chunk content backends**: `internal/storage.ChunkStore` (Put/Get/Size/
  Exists/Close, content-addressed by BLAKE3, idempotent Put) is the single seam.
  Usecases reach it per project through `usecase.ChunkStores`
  (`chunk_stores.go`, `ForProject` → `ProjectStore{Store, External,
  ReadOnly}`; `UsecaseDeps.ChunkStores`, nil = `StaticStores(ChunkStore)`):
  `Chunk` resolves once per presign/confirm/store/download (existence and
  dedup are per store), `Branch` reads through `ProjectChunkReader` and stores
  merge output with `Chunk.StoreForProject`, and `/api/chunks` resolves the
  project from the signed slugs. `External` skips the storage ledger (chunk
  rows and project links are still written); `ReadOnly` refuses upload
  presign/confirm/store with 402 `own storage requires the Own storage
  add-on`, and the same `Chunk.RequireWritable` gate (`WithStorageGate`)
  refuses `Push.CheckTarget`/`Push.Push` and every branch landing
  (`fastForward`, `MergeForMergeRequest`); reads stay open.
  `storage.NewLocalStore` (`CHUNK_STORAGE=local`, `CHUNK_STORAGE_DIR`) is the
  free `cmd/nipad`'s only backend — `CHUNK_STORAGE=s3` there fails with a hint
  to use `nipad-ee`. The enterprise `ee/cmd/nipad` is S3-only: empty or `s3`
  selects `ee/storage/s3` (`CHUNK_S3_*`), anything else is a startup error. The
  S3 store keys objects `<prefix>/<hash[:2]>/<hash[2:]>` and probes the bucket
  at startup (`Config.SkipProbe` skips it; `Config.Transport` carries every
  request). `Store.Probe` checks every capability below on a throwaway
  `<prefix>/.nipa-probe-<rand>` object and returns a `*ProbeError` naming the
  failed check. `internal/` must never import `ee/` — only `ee/cmd/*` does (the
  free `cmd/nipad` links no ee package). A store implementing
  `storage.DirectTransferStore` (the S3 store does) hands clients absolute
  presigned backend URLs: downloads via `PresignedGetObject`, uploads via a
  POST policy whose `content-length-range` pins the exact declared chunk size
  (S3 rejects oversized bodies with EntityTooLarge); `usecase.Chunk`
  type-switches on the capability, `PresignedChunkUrl` carries
  `method`/`form_data`, and the client POSTs multipart with the file part last
  (`Client.httpURL` passes absolute URLs through). Direct uploads are
  presigned PUTs (not POST policies, which Cloudflare R2 does not implement) to
  the shared key, signed with `If-None-Match: *` and the exact `Content-Length`:
  the backend refuses (412) a write when the object exists, even when a target is
  replayed, so a stored chunk can never be overwritten and one project cannot
  corrupt content another relies on. The client sends the target's `headers`
  and treats a 412 as "stored meanwhile". A chunk that already exists gets no
  upload target at all (`url` empty, only a `proof_challenge`): the project
  claims it with the possession proof, which costs no write. Because direct
  writes bypass the server, `ConfirmChunkUploads` hashes whatever it reads from
  the shared key (for chunks the project is not linked to yet and for linked
  ones the client supplies a proof for): an object that does not match its hash is deleted and
  reported missing, and the client presigns and uploads that chunk again (up to
  three attempts). The side effect is that a project writer who knows a hash
  learns that its content exists (no target is issued). Backends must support
  conditional create on presigned PUT (AWS S3, MinIO, R2);
  `TestIntegration_S3CreateOnlyUploads` verifies it against a real backend when
  the `NIPA_S3_TEST_*` variables are set (R2: endpoint
  `https://<account>.r2.cloudflarestorage.com`, region `auto`).
- **Storage accounting seam (free vs cloud).** `usecase.Chunk.WithStorageLedger`
  accepts an optional `StorageLedger` (`internal/usecase/storage_ledger.go`:
  `AttributedSizes`/`Headroom`/`Attribute`). The free version wires none (nil):
  presign/confirm skip all accounting and no quota applies. Cloud builds inject
  an `ee/` implementation at `ee/cmd/nipad` (same import pattern as `ee/storage/s3`);
  it links chunks to projects, keeps per-project/org/owner counters and enforces
  the owner's quota at both `GetChunkUploadUrls` (projected new bytes) and
  `ConfirmChunkUploads` (authoritative). Quota failures use
  `domain.NewErrorQuotaExceeded` (HTTP 402) and cross gRPC as
  `ResourceExhausted`, which the client maps back to a 402 domain error so
  `nipa push` reports it. The cloud implementation (ee tables, per-user quota,
  purchase, recount) is planned in `docs/CLOUD_STORAGE_QUOTA.md`.
- `Parsec`/`ParseNipaUrl` (`internal/client/domain/url.go`): `/org/project[/path]`.
  `path` is threaded through to the manifest request so a missing subpath returns a
  404, and cloning a repo with an empty (no-commit) branch returns an empty tree,
  not an error (branch root only).

