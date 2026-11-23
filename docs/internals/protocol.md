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
  costs no query, and left empty unless every chunk has a size). File `mode`
  is the `pb.FileMode` class, classified from the Unix permission by one rule,
  `domain.ClassifyPerm` (any exec bit → EXECUTABLE, else no write bit →
  READ_ONLY, else READ_WRITE; only `0o777` counts), shared by client status
  (`serverModeFromPerm`) and the push transport (`intToPBFileMode`); the server
  stores 0o755/0o444/0o644. Tree hashes include the mode class
  (`internal/treehash`): a file's serialized kind byte is 0 for read-write
  (exactly the pre-mode format, so read-write-only trees kept their hashes), 2
  for executable and 3 for read-only, classified by
  `domain.ClassifyStoredMode` (canonical values, legacy decimal 444/755, zero as
  read-write, anything else by `ClassifyPerm`), the same rule
  `domainFileModeToPB` uses. A mode-only push therefore gets a new tree hash
  and a real commit, and the hash-shared reads (`FileListByTree`,
  `TreeNodeListChildren`, `ChunkListByTree`, which resolve a node to the oldest
  content node with its hash) only ever share rows whose modes match.
  Hashes stored before this were recomputed by `usecase.BackfillTreeHashes`,
  which both servers run after migrating and before serving
  (`serverapp.BackfillTreeHashes`; migration `tree_hash_modes`): seeds are the
  nodes owning a file whose stored mode is not 0/0o644, changes propagate to
  parents and to the reference rows that resolved to a changed content node
  (a reference takes the new hash of the oldest content node with its old hash,
  so it keeps serving what it served), reads see the original hashes until the
  pass writes the changed rows in one transaction, and only changed rows are
  written. The single `tree_hash_backfill` row holds the highest tree node id
  covered and the start of the last pass, and, on postgres, is locked
  `FOR UPDATE` for the pass so pods run it one at a time. Each start scans the
  ids above the newest node created before the previous pass start minus
  `usecase.TreeHashBackfillOverlap` (2h; `TreeHashBackfillRescanFrom` walks the
  primary key back from the watermark): identity ids are taken at insert, not
  at commit, so a push that was running when the previous pass read MAX(id)
  can commit below its watermark, and is rescanned (idempotently) as long as
  it ran for less than the overlap. Rows an older pod writes during a rolling
  deploy are covered the same way: its old-format content rows are seeds or
  parents of seeds, and a reference it wrote with the hash a kept directory had
  before a pass rewrote it (so no content node resolves it and the directory
  would read empty) is repaired from `tree_hash_rewrites`, which maps the old
  hash of every rewritten content node that references resolved to (the oldest
  one with that hash) to its new hash; `TreeHashDanglingReferences` finds
  such references in the scan window, the pass gives them the new hash and
  propagates to their parents (recording legacy root hashes as usual). Until
  the next pass those directories read empty, so restart a new-version pod
  once the old version is gone. Commit hashes are not recomputed (they are
  identifiers fixed at creation). `tree_hash_legacy` keeps the pre-backfill hash
  of every changed root, and `Push.baseTreeMatches` accepts it as the base tree
  hash, so a client pushing against its stored `tree_hash` without a
  `base_commit_id` is not told the branch moved; the next `nipa update`/push
  stores the new hash. `GetBranch` takes
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
  client then PUTs/GETs bytes directly over HTTP through adaptive transfer
  limiters (`internal/client/grpc/transfer.go`), one per direction
  (`Client.uploads` / `Client.downloads`, each a `transferPool` of limiter +
  tuner): uploads start at 16 within [4, 64], downloads at 64 within
  [4, 256]. Pin them with `NIPA_UPLOAD_WORKERS` / `uploadWorkers` and
  `NIPA_DOWNLOAD_WORKERS` / `downloadWorkers` in `~/.config/nipa/config.json`
  (env > config > default; pinning disables that direction's tuner only).
  The tuner hill-climbs on throughput measured over windows of >= 64 MiB and
  >= 2 s (or 30 s on a slow link): a window > 5% above the best recent one
  adds an eighth of the limit; a flat window holds it (every 8 flat windows
  it probes one step up and undoes the step if nothing improves); a window
  below 70% of the best cuts a quarter; every retryable failed attempt
  (network error, stall, 5xx, 408, 429, reported from `retryTransfer`)
  halves it, at most once per 2 s. A cut clears the reference so the next
  window is the new baseline, followed by a probe up; the best decays by 10%
  per flat window so an old burst cannot hold the limit down. Completed jobs
  feed the tuner from `runChunks`; cancellations and final non-retryable
  failures do not. `defaultHTTPClient` pools 256 connections per host (the
  larger maximum), so the transport never caps a limit the tuner chose.
  **Chunk protocol and per-project scope.** Dedup is per project: a chunk
  belongs to exactly one project (`chunks.project_id`, `UNIQUE (project_id,
  hash)`), two projects holding the same bytes store them twice, and the
  project's chunk row is its link to the content (no possession proofs, no
  challenges, no claim round). The three chunk RPCs carry `chunk_protocol`
  (`chunkproof.Protocol`, 2) on request and response: the server answers an
  older or missing version with `FailedPrecondition` and an "upgrade nipa"
  hint (`internal/grpc/server/chunk.go` `checkChunkProtocol`), and the client
  refuses a response without the current version (an older server) with a
  409 and the hint "upgrade the nipa server" (`checkServerChunkProtocol`).
  **Presign** (`PresignUploadURLs`) looks up the page's hashes in the
  project's rows (one batched `ProjectChunkSizes`): no row = needs upload, no
  store check (B4: a fresh push makes no existence checks); a row and the
  object present (`Exists`, through a server-wide limit of 64 concurrent
  checks, `presignExistsLimit`) = `already_stored`, no URL; a row but the
  object lost = needs upload again. Every chunk that needs an upload gets a
  URL; `url` is empty only with `already_stored`. **Confirm**
  (`ConfirmUploads`) protects the batch from the GC and verifies every hash
  (a recorded row does not vouch for its object, since presign re-issues an
  upload when the object is gone; a row only keeps its size and charge):
  a valid receipt (`ConfirmChunkUploadsRequest.receipts`, `ChunkReceipt{hash,
  receipt}`, see below) means the server hashed the bytes itself, so its
  signed size is used with no store read; anything else (direct uploads, an
  invalid, expired or other-project receipt) is read back from the project's
  own key, streamed (`storage.ChunkOpener.Open`, which `LocalStore` and the
  S3 store implement; other stores fall back to `Get`) through
  `io.LimitReader(MaxChunkSize+1)` into `chunkproof.Verifier`, which computes
  the plain BLAKE3 hash; an object larger than `chunker.MaxChunkSize()` or
  not matching its hash counts as missing and is deleted from a
  `DirectTransferStore` (garbage at a create-only key would block the real
  content). All confirms share one server-wide semaphore of
  `CHUNK_VERIFY_CONCURRENCY` read-backs (default 64, `<= 0` = default,
  `Chunk.WithVerifyLimit` via `UsecaseDeps.Config`). Then, in one
  transaction (`Chunk.WithTransactor`, the `dbtx` transactor), the ledger
  attributes the new rows (`ledger.Attribute`, quota, S5) and the project's
  rows are inserted (`InsertChunksIfNotExist`: chunk rows plus their
  `project_chunk_uploads` markers, `pushbatch` JSON statements, deduplicated,
  sorted by hash so concurrent postgres confirms take index locks in the same
  order, 1000 rows per statement, `ON CONFLICT DO NOTHING`), so no row is
  visible before the quota accepted it and a refused quota leaves none. The
  recorded size is always measured by the server. A client runs at most
  `maxConcurrentConfirms` (3) confirms at once across its upload windows
  (`Client.confirmSlots`; a waiter gives up when its context ends), so one
  window's read-back overlaps the others' PUTs while the server's
  verification load per client stays bounded (SQLite servers rely on
  `_txlock=immediate` + `busy_timeout`). `UploadChunks` presigns, PUTs and
  confirms a window in batches of `uploadBatch` (1000) chunks, one confirm per
  attempt (already-stored chunks are confirmed too, which clears their GC
  marks); chunks reported missing are presigned and sent again (up to
  `maxUploadAttempts`, 3). A 412 on a create-only PUT means the object was
  stored meanwhile; the confirm reads it back.
  **Upload mode and receipts.** `CHUNK_UPLOAD_MODE` (`direct` default |
  `proxy`, validated at config load, `Chunk.WithUploadMode`) decides where a
  client PUTs to a `DirectTransferStore`: `direct` hands out the store's
  create-only presigned URLs; `proxy` makes `uploadTarget` hand out the signed
  server path (`chunkurl.UploadPath`) instead. It applies to the server's own
  store only: an own-storage project (`ProjectStore.External`) always uploads
  direct, and a store without direct transfers (free `nipad`, local) always
  uses the server path. `ChunkHandler.PutChunk` verifies the signed URL and
  the exact `Content-Length`, reserves the signed size from a server-wide byte
  budget (`CHUNK_PROXY_BUFFER_MIB`, default 128, `<= 0` = default,
  `Chunk.WithProxyBuffer`/`ReserveUploadBuffer`, a weighted semaphore; a body
  larger than the budget takes all of it, a request cancelled while waiting
  gets 503) before reading the body into one buffer of that size through
  `MaxBytesReader`, and gives it back once the store write returns or the
  body is rejected. `Chunk.StoreUploaded` checks BLAKE3 before any write
  (mismatch = 400, nothing written), clears the GC mark of the project's row
  (`ChunkGCGuard.Protect(project, hashes)`, so a sweep cannot remove the
  object between the write and the confirm that trusts the receipt), then
  writes the project's key: a store implementing `storage.ChunkOverwriter`
  (S3) gets an unconditional single-part `Overwrite` with no `Exists` first
  (one request; it replaces garbage an earlier direct upload left at the
  key), other stores keep the `Exists` skip. minio-go signs that PUT with
  `UNSIGNED-PAYLOAD` over https (no second SHA-256 pass) and with the
  streaming payload signature over plain http (`TestStore_PutPayloadSigning`).
  No row or quota is touched at PUT. The 204 carries `X-Nipa-Chunk-Receipt`
  (`chunkproof.ReceiptHeader`): `chunkproof.NewReceipt` is stateless,
  base64url(expiry ‖ size) plus an HMAC-SHA256 under `CHUNK_URL_SIGNING_KEY`
  with its own domain label `nipa-chunk-receipt:v1` over project id, hash,
  the server-measured size and the expiry (`CHUNK_PRESIGN_TTL_SECONDS`); both
  blue/green colours share the key. The receipt no longer proves possession
  (there is nothing to prove within one project); it only spares the
  read-back and carries the size. The client (`putChunkOnce`/`keepReceipt`)
  keeps the header only from a 2xx on a relative server path, never from an
  absolute store URL, and sends it in `ConfirmChunkUploadsRequest.receipts`.
  Receipts never skip the rows, the ledger or the quota. Server-generated
  merge content is stored with `Chunk.StoreForProject` (object only) and
  recorded by `AttributeProjectChunks` (ledger, then rows, one transaction)
  before `ApplyPushAll`.
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
  `Push.pushStaged` scans into 32MB windows (`uploadWindowBytes`; a client
  override `NIPA_UPLOAD_WINDOW_MIB` / `uploadWindowMiB`, clamped to 4..256 MiB,
  reaches the uploader through `Client.UploadWindowBytes`) handed to four
  uploader goroutines (queue depth 2), so hashing overlaps with the network;
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
  contention from concurrent PUTs); the project's chunk rows are written at
  confirm time. `ApplyPush` (both repositories) resolves every chunk a push
  references in one lookup of the project's own chunk rows before it writes
  anything (a chunk the project has no row for fails with
  `missing chunk data for <hash>`), then
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
  Chunk transfer URLs go through `transferURL`: a session whose server
  address is `https://` refuses an `http://` chunk URL (upload and download)
  unless `NIPA_ALLOW_INSECURE_CHUNK_URLS=1`, and the default HTTP client
  follows at most 3 redirects and none from https to http
  (`checkTransferRedirect`). A download is cancelled and retried when nothing
  arrives for `Client.stallTimeout` (60 s, `stallGuard`, covering the wait
  for the response and every gap in the body); the transport also bounds the
  wait for response headers to 2 minutes. An upload has the same guard on
  its request body (`putChunkOnce`, `stallGuard.wrapUpload`): the transport
  asks the body for more only after it wrote what it had, so no body read
  for `stallTimeout` means the connection stopped taking bytes, and the PUT
  is cancelled with `errTransferStalled` and retried like any transient
  failure. The guard is disarmed by the `httptrace` `WroteRequest` hook, once
  the whole request is written and flushed, so a slow answer is left to the
  response-header timeout; `Content-Length` is unchanged and `GetBody`
  returns a fresh guarded reader (re-arming the timer) for redirects and
  retried connections.
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
  Exists/Close, keyed by project and BLAKE3 hash, idempotent Put; every
  method takes the project id) is the single seam. Every backend lays chunks
  out as `p/<project base36>/<hash[:2]>/<hash[2:]>` below its root or prefix
  (`storage.ChunkKey`/`ParseChunkKey`/`ProjectPrefix`), so a project's
  content is one prefix; the old global layout (`<hash[:2]>/<hash[2:]>`) is
  not read (deploys wipe it, `docs/DEPLOY.md`). `storage.ProjectDeleter`
  (`DeleteProject`, local `RemoveAll` of the project directory, S3 listing +
  batched `RemoveObjects` of `p/<project>/`) serves the purge.
  Usecases reach it per project through `usecase.ChunkStores`
  (`chunk_stores.go`, `ForProject` → `ProjectStore{Store, External,
  ReadOnly}`; `UsecaseDeps.ChunkStores`, nil = `StaticStores(ChunkStore)`):
  `Chunk` resolves once per presign/confirm/store/download, `Branch` reads
  through `ProjectChunkReader` and stores merge output with
  `Chunk.StoreForProject`, and `/api/chunks` resolves the project from the
  signed slugs. `External` skips the storage ledger (chunk rows are still
  written); `ReadOnly` refuses upload presign/confirm/store with 402 `own
  storage requires the Own storage add-on`, and the same
  `Chunk.RequireWritable` gate (`WithStorageGate`) refuses
  `Push.CheckTarget`/`Push.Push` and every branch landing (`fastForward`,
  `MergeForMergeRequest`); reads stay open.
  `storage.NewLocalStore` (`CHUNK_STORAGE=local`, `CHUNK_STORAGE_DIR`) is the
  free `cmd/nipad`'s only backend — `CHUNK_STORAGE=s3` there fails with a hint
  to use `nipad-ee`. The enterprise `ee/cmd/nipad` is S3-only: empty or `s3`
  selects `ee/storage/s3` (`CHUNK_S3_*`), anything else is a startup error. The
  S3 store keys objects `<prefix>/p/<project>/<hash[:2]>/<hash[2:]>` (BYO
  stores the same under their own prefix) and probes the bucket at startup
  (`Config.SkipProbe` skips it; `Config.Transport` carries every request).
  `Store.Probe` checks every capability below on a throwaway
  `<prefix>/.nipa-probe-<rand>` object and returns a `*ProbeError` naming the
  failed check. `internal/` must never import `ee/` — only `ee/cmd/*` does (the
  free `cmd/nipad` links no ee package). A store implementing
  `storage.DirectTransferStore` (the S3 store does) hands clients absolute
  presigned backend URLs for the project's key: downloads via
  `PresignedGetObject`; `usecase.Chunk` type-switches on the capability,
  `PresignedChunkUrl` carries `method`/`form_data`, and the client POSTs
  multipart with the file part last (`Client.httpURL` passes absolute URLs
  through). Direct uploads are presigned PUTs (not POST policies, which
  Cloudflare R2 does not implement) to the project's key, signed with
  `If-None-Match: *` and the exact `Content-Length`: the backend refuses (412)
  a write when the object exists, even when a target is replayed, so a stored
  chunk can never be overwritten (S4). The client sends the target's
  `headers` and treats a 412 as "stored meanwhile". Because direct writes
  bypass the server, `ConfirmChunkUploads` hashes whatever it reads from the
  project's key for chunks without a row: an object that does not match its
  hash is deleted and reported missing, and the client presigns and uploads
  that chunk again (up to three attempts). The existence check stays because
  a project member could otherwise confirm a chunk nobody uploaded, which
  would commit a reference to missing content. Backends must support
  conditional create on presigned PUT (AWS S3, MinIO, R2);
  `TestIntegration_S3CreateOnlyUploads` verifies it against a real backend when
  the `NIPA_S3_TEST_*` variables are set (R2: endpoint
  `https://<account>.r2.cloudflarestorage.com`, region `auto`).
- **Chunk GC seam (`usecase.ChunkGCGuard`).** `Chunk.WithGCGuard` (nil in the
  free server; `UsecaseDeps.ChunkGCGuard`, set by `nipad-ee` unless
  `CHUNK_GC_MODE=off`) runs `Protect(project, hashes)` at the start of
  `ConfirmChunkUploads` (the whole batch), in `StoreUploaded` and in the
  server-side `upload` (merge output) before `Exists`. The ee guard
  (`ee/chunkgc.Guard`) clears `chunks.gc_marked_at` of the project's rows;
  that update waits for a sweep holding the row, so content a sweep removed
  reads as missing and the client re-uploads it (the three-attempt retry
  above) instead of a row being recorded for gone content. Every row insert
  happens after one of those calls. The collector itself
  (`ee/chunkgc.Collector`, every 6 h, one instance via
  `pg_try_advisory_lock`): mark chunk rows nothing in their own project
  references (no `file_chunks` row and no `project_chunk_uploads` marker of
  the row's project) in id ranges; adopt bucket objects older than the grace
  that have no row for their `(project, hash)` (`s3.Store.ListChunks` parses
  `p/<project>/...` keys, `CHUNK_GC_ORPHAN_SCAN`; objects of a project that
  no longer exists are left alone) as marked rows; sweep rows marked longer
  than `CHUNK_GC_GRACE_HOURS` (`FOR UPDATE SKIP LOCKED`, 100 per transaction:
  delete the project's objects, then the rows; a failed object delete rolls
  the batch back). `CHUNK_GC_MODE=dry-run` (default) marks and reports
  (`nipa_chunk_gc_*{phase="dry_run"}`) but deletes and adopts nothing.
  Own-storage (BYO) buckets are never touched. Project purge deletes the
  project's rows and its `p/<project>/` prefix (`ee/projectpurge`,
  `Purger.WithStore`). Plan and race analysis:
  `docs/plans/STORAGE_RECLAIM.md` (written for global chunks; the reference
  checks are now per project).
- **Storage accounting seam (free vs cloud).** `usecase.Chunk.WithStorageLedger`
  accepts an optional `StorageLedger` (`internal/usecase/storage_ledger.go`:
  `AttributedSizes`/`Headroom`/`Attribute`). The free version wires none (nil):
  presign/confirm skip all accounting and no quota applies. Cloud builds inject
  an `ee/` implementation at `ee/cmd/nipad` (same import pattern as `ee/storage/s3`);
  it charges each project for its own chunk rows (new rows only, checked
  under the owner lock in the confirm's transaction), keeps
  per-project/org/owner counters and enforces
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

