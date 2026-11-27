# Nipa Path-Based Access Control (PBAC)

Design for server-enforced, path-scoped permissions, plus sparse checkout for
large binary-heavy repositories. PBAC answers *who may read or change which
paths*; sparse checkout answers *which permitted paths a workspace materializes*.

Related: `docs/LOCKING.md` reuses the same path-resolution rule (locks are write
leases per path).

---

## Permission bits

Existing bitmask (`internal/domain/permission.go`):

| Bit | Value | Meaning |
|-----|-------|---------|
| `PermissionRead` | 1 | Read tree metadata and content |
| `PermissionWrite` | 2 | Push changes under the prefix |
| `PermissionLock` | 4 | Acquire/release locks (future) |
| `PermissionAdmin` | 1 << 16 | Manage rules and force operations on the prefix |
| `PermissionAll` | Read\|Write\|Lock\|Admin | Admin bypass mask |

`Permission.Has(bit)` tests a bit; `Permission` values are OR-ed.

---

## Rule model

Two tables, allow-only rules, directory-prefix matching. No precedence puzzles:
rules grant, project defaults grant, and the deepest matching default is the
fallback for users without rules.

### `pbac_rules` — per user/group grants

| Column | Notes |
|--------|-------|
| `user_id` / `group_id` | Exactly one is set (CHECK) |
| `org_id` | Rule scope; always set |
| `project_id` | `NULL` = every project in the org |
| `path_prefix` | `''` = entire project, otherwise a directory prefix |
| `permission` | Bitmask granted |

### `project_paths_permissions` — project default access per folder

| Column | Notes |
|--------|-------|
| `project_id` | Project the default belongs to |
| `path_prefix` | Directory prefix, unique per project |
| `permission` | Default bitmask for that prefix; `0` = fallback deny |

A row makes a folder accessible by default to any authenticated user; rules add
permissions on top. `permission = 0` (or no row) is the "no access by default"
case: it only applies to users who have **no** matching rule for that path, so a
grant on a deeper or shallower prefix still works. This is the *fallback deny*.

### Path matching

Patterns are directory prefixes, not globs (Git cone-mode semantics):

- Normalized: leading/trailing `/` trimmed, empty segments collapsed, `..`
  rejected (`domain.NormalizePathPrefix`).
- `domain.PrefixCovers(prefix, path)`: `''` covers everything; otherwise `path`
  equals the prefix or starts with `prefix + "/"`.
- `domain.PrefixCanDescend(prefix, dir)`: whether any path below `dir` can match
  `prefix` — used to prune manifest traversal without missing deep grants.

---

## Evaluation

```
resolve(claim, project, path) -> Permission:
  admin/super-admin                    -> PermissionAll
  org owner                            -> PermissionAll
  mask := 0
  for rule in effective_rules(user, project):   # user rules + group rules
      if PrefixCovers(rule.path_prefix, path):
          mask |= rule.permission
  default := longest matching project_paths_permissions row
  if default != nil:
      mask |= default.permission                # fallback deny when 0
  return mask
```

- An owner of the project's organization (`org_members.role = 'owner'`)
  implicitly holds `PermissionAll` on every project in that org — the creator
  of an org can use the projects they create without seeding rules. Plain org
  members get nothing without rules. `Permission.load` resolves this per
  project through `pbacRepository.ProjectOrgID` + `orgAuthorizer.IsOrgOwner`
  (cached with the rest of the rule set; `Org` member changes and accepted
  invitations drop that cache on the instance that served them, other
  instances see owner add/demote/remove at the next TTL expiry).
- Effective rules are fetched with one query that joins `group_members` and
  resolves org scope via the project (`pbac.sql`, `PBACRuleListEffective`).
- `HasProjectAccess(project, bit)` = admin OR org owner OR any rule/default
  carries the bit; it gates project-wide operations (list branches, clone).
- `HasPathAccess(project, path, bit)` checks `resolve`.
- A path with `Read` denied returns **404** (do not disclose existence); a path
  readable but not writable returns **403**.
- Results are cached per `(project, user)` in each server process with a 30 s
  TTL (`permissionCacheTTL`) and a per-project version counter bumped by every
  rule, path default, group-membership and org-member mutation that process
  serves. A change is therefore enforced at once by the instance that made it
  and within 30 s by every other `nipad-ee` instance (the free `nipad` is
  single-instance, so always at once). Nothing derived from the rules may be
  cached for longer or keyed without them: the chunk download set is cached
  under a fingerprint of the compiled read filter, like manifest view tokens.

`FilterTree` prunes a manifest to permitted paths, keeping file-less "trail"
nodes so a grant below a denied directory stays reachable.

---

## Enforcement map

| RPC | Check |
|-----|-------|
| `GetListBranch`, `GetBranch(ByName)`, `GetDefaultBranch` | project read |
| `GetTreeManifest` | project read; prune requested paths; hidden path 404 |
| `GetCommit`, `GetCommitLog`, `WalkCommits`, `GetMergeBase` | prune trees |
| `CreateBranch` | project write |
| `RenameBranch`, `DeleteBranch` | project write; project admin for protected branches; the default branch cannot be deleted |
| `SetDefaultBranch`, `SetBranchProtection` | project admin |
| `MergeFastForward` | write on every changed path (`diff.TreeDiff`); a protected target can only be moved by merging a merge request (project admin required for the MR) |
| `Push` | write per pushed and removed path; protected branches always refuse direct pushes and changes must land through a merge request |
| `GetChunkDownloadUrls` | hash must be in the permitted chunk set for the given commits and path prefixes; hidden hashes are omitted from the response |
| `GetChunkUploadUrls`, `ConfirmChunkUploads` | project write |
| rule/group management | admin or `PermissionAdmin` on the prefix |

Admin mutations invalidate the project cache.

### Chunk authorization

`GetChunkDownloadUrls` requests carry a project context, the commit IDs whose
trees the client is materializing, and optional path prefixes. The server unions
the readable chunks of those trees (reusing the manifest walk) and signs URLs
only for hashes in that set; anything else is omitted so hidden content cannot
be probed by hash. The set follows the caller's rules as closely as every
other check does (see Evaluation): the server keeps it for the download
windows of one clone, keyed by the rules it was computed under, so a revoke or
a grant applies to the next request, not after a cache lifetime. Merge, revert and diff pass every commit involved (target,
source, base/parent), because three-way materialization mixes chunks from
several trees. `GetChunkUploadUrls`/`ConfirmChunkUploads` only need project
write, so content can be uploaded before a push that is then validated per path.
Signed URLs are capabilities: the `/api/chunks` (download) and
`/api/chunk-bundles` (upload) HTTP endpoints verify the HMAC signature
(project, hash or bundle limits, expiry) instead of the Bearer token, so a URL
works without a session until it expires — also after the right it was signed
under is revoked: a revoke stops new URLs, it does not cancel issued ones, which
stay valid for at most `CHUNK_PRESIGN_TTL_SECONDS` (default 3600 s; lower it to
shorten that window).

**Packs and path rules.** With `CHUNK_PACKS=on` (`nipad-ee`, S3, proxy
uploads) one pack object holds every chunk of one upload bundle, so chunks of
folders with different read rules share an object, and the store's presigned
URL of a pack authorizes the whole object (the `Range` is not signed). Such a
URL is therefore issued only to a caller who may read every path of the
project (`PathFilter.Unrestricted`: admin, org owner, a rule on the project
root, or a root path default with no default below it that withholds read).
Everyone else gets, for a packed chunk, the server's own signed
`/api/chunks/{org}/{project}/{hash}` URL, which serves exactly that chunk and
shows no pack id, offset or pack size; they pay one request per chunk through
the server instead of ranged GETs to the store. The test is
`ee/e2e/path_access_packs_test.go`. Loose objects (free `nipad`, S3 direct
mode, own-storage projects) hold one chunk each and are not affected.
Remaining exposure, by design: a pack URL issued to a whole-project reader
stays valid for the presign TTL after that reader is restricted, and covers
the whole pack (not only the chunks they asked for), including bytes of
uploads that were never committed. Separately, dedup is per project and has
no possession proof: a caller with write on any path learns from
`GetChunkUploadUrls` (`already_stored`) whether a chunk hash exists anywhere
in the project, and may reference an existing hash in a file it pushes to a
path it can read, which makes knowing a hidden chunk's hash equivalent to
reading it. Hashes of hidden chunks are not served (manifests and download
URLs are filtered, and a file hash is a hash over chunk hashes), so this
needs the content or its hash from elsewhere.

Bundle URLs bind the project, the
largest body and the most chunks per body, and every chunk of a bundle is
BLAKE3-checked before anything is stored; chunk metadata rows are only written
by `ConfirmChunkUploads` after the content exists.

### Admin surface

`GetMyPermissions` is available to any authenticated user. Rule and project
default management requires `IsAdmin`/`IsSuperAdmin`, an owner of the project's
organization, or a rule granting
`PermissionAdmin` on the whole project (prefix `""`); org-wide rules (no
project) require a global admin, and the gRPC API currently creates
project-scoped rules only. Rule targets are validated: the user must exist and
the group must exist and belong to the rule's organization. Deleting an
org-wide rule requires a global admin too, so a project admin cannot remove a
rule that governs every project in the org. Group create/list/member management
requires a global admin or an owner of the group's organization
(`org_members.role = 'owner'`). Every rule, default, group-membership and org-member change
invalidates the permission cache of the instance that served it immediately;
other instances follow within the 30 s TTL.

The CLI exposes this as `nipa acl list|grant|revoke|my`,
`nipa permission list|set|remove` and `nipa group create|list|add-member|remove-member`,
using the organization/project of the current working copy.

---

## Sparse checkout

Sparse checkout is a working-copy filter on top of PBAC: PBAC decides what a
user *may* see, sparse paths decide what the client *wants* to materialize.

- `.nipa/config` stores `sparse: ["assets/textures", "src"]`; absent/empty means
  a full clone of the permitted tree.
- `GetTreeManifestRequest.paths` carries the prefixes; the server returns a tree
  pruned by permission and sparse prefixes while keeping repo-relative paths.
- Update/switch reconcile only the sparse set; deselected unmodified files are
  removed, cached chunks are kept for cheap re-adding.
- Push sends the pinned `base_commit_id` (a sparse snapshot hash is not the real
  head tree hash) and the server rebuilds the new tree from the full base, so
  files outside the sparse set are never touched. Write is checked per path.
- CLI: `nipa clone <url> <target> --sparse a,b` and
  `nipa sparse-checkout list|set|add|remove|disable`.
- Merge and revert still require a full checkout and are rejected in sparse or
  subdirectory clones; `nipa diff` works on the filtered trees.
- The server persists untouched directories as hash references in the new
  commit (rather than re-materializing them), so incremental and sparse pushes
  stay proportional to the change and manifests resolve shared directories by
  content hash.

---

## Roadmap

| Step | Deliverable |
|------|-------------|
| M0 | Schema + queries + repository + `Permission` usecase (evaluation, pruning, cache) |
| M1 | Manifest/history pruning, `paths` on `GetTreeManifest` |
| M2 | Authorized `GetChunkDownloadUrls`, gated `GetChunkUploadUrls`/`ConfirmChunkUploads` |
| M3 | Per-path `Push`, fast-forward changed-path check |
| M4 | Rule/group management RPCs + `nipa acl`/`group` CLI |
| M5 | Sparse clone/update/push + `sparse-checkout` CLI |
| M6 | Lazy subtree fetch, web UI ACL admin (Postgres parity: `ee/repository/postgres`) |
