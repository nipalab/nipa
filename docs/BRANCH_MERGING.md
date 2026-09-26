# Branch Merging & Merge Requests

Design doc for merging branches and for Git-style Merge Requests (MRs) in Nipa's
centralized model. Covers the "MR is behind the target branch" problem and how
authors bring their branch up to date (merge or rebase) before an MR lands.

## Current model — what already supports this

- `commits` table has **both** `parent_1_id` and `parent_2_id` columns
  (`db/migrations/sqlite/000003_branch.up.sql`); push only ever writes `parent_1_id`.
- `domain.Commit` has `Parent1ID`/`Parent2ID *snow.ID`.
- `treehash.CommitHash(rootHash, parentHashes, message)` takes a **slice** of
  parent hashes — a 2-parent commit hash is already computable.
- Trees are content-addressed copy-on-write: `tree_nodes` unique on
  `(parent_tree_id, name)`, `files` unique on `(tree_id, name)`, chunks global +
  immutable. Unchanged subtrees are reused **by hash** across commits, so a
  merged tree costs only the rows that actually changed.
- `Push` (internal/usecase/push.go) validates `baseTreeHash` equals the current
  branch head tree (optimistic concurrency), then builds the new tree via
  `buildNewTree` (treeBuilder) and writes a commit whose parent is the old head.
- `GetTreeManifest` (proto) returns `{branch, root_tree}` recursively for a
  branch + optional `path`; `GetBranch`/`CreateBranch`/`ListBranches` exist.
- Client localrepo (`meta` table) pins the last pushed commit
  (`commit_id`/`commit_hash`, via `SaveCommit`/`LoadCommit`).

No schema migration is required for either feature — the DAG was built for this.

---

## Part 1 — Local branch merge: `nipa merge <source>`

Runs from a working copy, interactive, on a checkout. One **merge commit** on the
target branch whose parents are `(target_head, source_head)` and whose tree is
the 3-way merged result.

### Semantics — three flow cases

1. **Already up to date** — the merge base *is* the source head (source is fully
   contained in target). No-op, message `Already up to date.`
2. **Fast-forward** — the merge base *is* the target head (target has not moved
   since the branches diverged). Pure branch-pointer move to the source head,
   **no new commit** (`--no-ff` forces a merge commit).
3. **True merge** — build a merged tree; if clean, push a 2-parent commit
   immediately; if conflicting, write conflict state and let the user resolve,
   then complete with a normal `nipa push` (Git's "commit finishes the merge").

### Where the work runs

- **Server** owns the commit DAG: computes the **merge base** (LCA over
  `parent_1_id`/`parent_2_id`), returns it as a recursive manifest, and performs
  fast-forward pointer moves.
- **Client** fetches base + source manifests (3 recursive manifests total; chunks
  streamed through the existing cache), runs the 3-way tree merge in the working
  copy, drives resolution UX, and pushes the merge commit. This fits the existing
  push model — the client stages content, the server reuses unchanged subtrees by
  content address. Client-side conflicts are also the right call for game dev:
  binary assets cannot be marker-merged, and resolution UI belongs in the editor
  plugins (`docs/INTEGRATIONS.md`).

### Server changes

1. **`GetMergeBase(target, source)`** — walks both branch heads back to the LCA
   via `GetCommit` (new `GetCommitByID`/`CommitListForProject` repo reads) and
   returns `{base_commit_id, target_commit_id, source_commit_id, base_tree
   (recursive manifest)}`. Refactor `loadTreeManifest` into a reusable
   `treeManifestForCommit(commitID)` that `GetTreeManifest` also uses.
2. **`MergeFastForward(target, source)`** — validates base == target head and
   source head is reachable, then `BranchUpdateCommit` moves target's `commit_id`
   to the source head. No new commit.
3. **Push with a second parent** — add `parent_2_commit_hash` to the push
   request; extend sqlc `CommitInsert` to set `parent_2_id` (column already
   exists); commit hash = `CommitHash(root.Hash, [p1.Hash, p2.Hash], message)`.
   The existing `baseTreeHash == head` check stays as the concurrency guard — a
   merge push is a normal push whose base is the target head.

### Client 3-way tree merge

New `internal/client/merge/` package:

- Flatten base/target/source into `path → file` maps (reuse the `flattenTree`
  pattern from `internal/client/usecase/update.go`).
- Per-path decision:
  - unchanged / one-sided change / both-sides-same  → take the changed/kept side.
  - both sides changed differently → **conflict**:
    - both text → line-level **diff3** (`<<<<<<<` / `=======` / `>>>>>>>`
      markers). Implement a small Myers/LCS differ (e.g. difflib-style
      SequenceMatcher); do not invent smart merging in v1.
    - otherwise (either side binary) → conflict **file** that records a pick
      (take ours / take theirs / keep both), with a placeholder on disk. Never
      write merge markers into a binary.
  - add/add (different content) → conflict. modify/delete, delete/modify →
    conflict. deleted on both → gone.
- Writes the merged tree to the working copy (`syncWorkingCopy`-style) and a
  conflict list in `.nipa/` (`conflicts.json`).
- Persists a pending-merge state in localrepo `meta`
  (`merge_source_hash`, `merge_base_hash`) so the follow-up push knows the second
  parent.

### Completing / aborting

- After conflict resolution, a normal `nipa push` auto-attaches
  `parent_2_commit_hash` from the pending-merge meta and clears it.
- `nipa merge --abort` restores the pre-merge snapshot (localrepo keeps the
  pre-merge snapshot via the normal SaveCommit/LoadCommit mechanism) and clears
  pending-merge state.

### CLI

```
nipa merge <branch>          # merge <branch> into the current branch
nipa merge --abort           # cancel a conflicted merge
nipa merge --ff-only         # error instead of creating a merge commit
nipa merge --no-ff           # always create a merge commit
nipa status                  # shows files in conflict state (C)
```

---

## Part 2 — Merge Requests: `nipa mr`

An MR is a **server-side record** linking `source_branch → target_branch` with a
lifecycle. Merge *authoring* stays client-side (Part 1); MR *landing* is a
server operation.

### Entity & lifecycle

```sql
CREATE TABLE merge_requests (
    id                 INTEGER PRIMARY KEY,
    project_id         INTEGER NOT NULL REFERENCES projects(id),
    source_branch_id   INTEGER NOT NULL REFERENCES branches(id),
    target_branch_id   INTEGER NOT NULL REFERENCES branches(id),
    title              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'open',  -- open | merged | closed
    merge_commit_id    INTEGER REFERENCES commits(id), -- set on merge
    merge_base_commit_id INTEGER REFERENCES commits(id), -- snapshot at create
    created_by         INTEGER NOT NULL REFERENCES users(id),
    created_at         TIMESTAMP NOT NULL DEFAULT now,
    updated_at         TIMESTAMP NOT NULL DEFAULT now
);
```

Lifecycle: `open` → `merged` | `closed` (reopen allowed from `closed`). A merged
MR cannot be merged again. ID via `snow.ID` like other server records.

### Statuses & mergeability

Computed live on any check/merge (never stored as truth):

- `open` — created, not yet merged.
- `behind_target` — source head does not contain the target head's changes
  (target advanced since the branchpoint). The MR **cannot land until the author
  updates their branch** (Part: "Making an MR up to date").
- `conflicted` — a 3-way merge of (base, target, source) has conflicts.
- `mergeable` — up to date (per policy) and conflict-free.
- `merged` / `closed`.

Server returns a structured status, e.g.
`CheckMergeRequest → {status, up_to_date, conflicted, ff_able, merge_base, ...}`
so the CLI/plugins can render the right CTAs.

### The "MR is behind the target" problem

When `target` has moved since the branches diverged, the MR source is **behind**.
Merging it straight into the target would overwrite the target's recent changes
(3-way absorption) or silently create an MDG-styled merge bubble the team may not
want. The fix is always: **make the source branch contain the target's changes
first**, then land. Two strategies:

**Strategy A — merge target into your branch (default, safe, no history rewrite):**

```
nipa mr update            # = nipa merge <target> on source branch, then push
```

1. Fetch target head + base manifests, run the Part 1 3-way merge into the
   working copy, resolve conflicts locally.
2. `nipa push` creates a **2-parent commit** on the source branch
   (`parent_1 = source head`, `parent_2 = target head`). No force needed.
3. Source is now up to date with target; MR becomes `mergeable`.

This is GitHub's "Update branch" / GitLab's merge-based button. Preferred when
history accuracy matters and as the default UX.

**Strategy B — rebase onto target (linear history, needs force-with-lease):**

```
nipa mr update --rebase   # = nipa rebase <target> on source branch
```

1. Fetch `target` head tree/manifest and the merge base.
2. Walk the commits on the source branch back to the base (new
   `WalkCommits(project, start, stop)` RPC returning ordered
   `{commit_id, commit_hash, tree_hash, message}`).
3. Replay oldest→newest: for each commit, diff its tree vs its parent's tree,
   apply that delta onto the moving rebuilt tree using the same treeBuilder
   semantics `buildNewTree` already implements ("base tree + changed paths +
   removed paths → new tree").
4. A conflict during replay **stops the rebase** at the offending commit;
   resolve, `nipa rebase --continue`. `--abort` restores the branch head.
5. Final push moves the source branch head to the rebuilt (rewritten) head whose
   tree is based on `target`'s head, **not** a descendant of the old source head.
   Normal push would fail the `baseTreeHash` check, so rebase uses
   **force-with-lease**: the push carries `parent = target head` plus an
   `expected_old_head_hash`; the server moves the head to a *non-descendant*
   commit **only if** the current head still equals the expected old head —
   rejecting if someone else pushed meanwhile.

Rebase rewrites published history; on a shared feature branch it is opt-in
(`--rebase`) and may require coordination.

### Landing the MR (server-side merge)

When the author requests a merge (`nipa mr merge <id>`), the server:

1. Re-checks status in one transaction — MR is `open`, source and target exist,
   policy satisfied (up-to-date if required), 3-way is conflict-free.
2. **Fast-forward**: if merge base == target head, move target's `commit_id` to
   source head (no commit).
3. Otherwise create a **merge commit** server-side: tree = clean 3-way
   (base, target, source), parents `(target_head, source_head)`, set target head
   and `merge_commit_id`. Because it is server-side and content-addressed, the
   merged tree reuses untouched subtrees by hash; only truly-merged paths are new
   rows.
4. Post-merge event hooks (CI, notifications) fire on the new commit.

Conflicted MRs are rejected until the author resolves locally (Part 1) — the
server never auto-resolves conflicts.

### Up-to-date policy (per project)

- `require_up_to_date` (GitLab-style, recommended default): merge is refused
  unless source contains target head. Forces the Part-2 reconciliation loop.
- `allow_behind` (GitHub-style): server 3-way merge is still *correct* because it
  diffs against the **fresh** base read inside the merge tx; the "behind" state
  is informational only.
- Optional **merge train / queue** (later): merges are serialized; each queued MR
  is merged on top of the previous merge result and re-validated, guaranteeing
  linear, always-mergeable history. Model after GitLab merge queues.

---

## Server surface (new)

### Proto (`internal/grpc/proto/server.proto`)

```
MergeRequestInfo { id, title, description, status, source_branch, target_branch,
                   created_at, updated_at, merge_commit_id? }
GetMergeBaseReq { context, target_branch, source_branch }
GetMergeBaseResp { target_commit_id, source_commit_id, base_commit_id,
                   base_tree (TreeManifest) }
WalkCommitsReq { context, start_commit_id, stop_commit_id }
WalkCommitsResp { repeated CommitInfo { commit_id, commit_hash, tree_hash, message } }
PushRequest  += optional string parent_2_commit_hash
PushRequest  += optional string force_with_lease_commit_hash   // rebase rewrite
CreateMergeRequestReq { context, source_branch, target_branch, title, description }
Get/List/Update/Close/Check/Merge MR messages...

NipaService:
  rpc GetMergeBase(...)
  rpc WalkCommits(...)
  rpc CreateMergeRequest / GetMergeRequest / ListMergeRequests /
      UpdateMergeRequest / CloseMergeRequest / ReopenMergeRequest /
      CheckMergeRequest / MergeRequest
```

### Usecase / repo

- `internal/usecase/merge.go` — `GetMergeBase` (LCA walk via `GetCommit`),
  `FastForward` (reachability + `BranchUpdateCommit`).
- `internal/usecase/mr.go` — CRUD + status computation + `Merge` (single tx:
  re-check, build 3-way tree, insert merge commit with both parents, move target
  head, set `merge_commit_id`).
- `internal/usecase/push.go` — `ApplyPushRequest` gains `ParentID2 *snow.ID` and
  `ForceWithLease *snow.ID`; `ApplyPush` verifies the lease inside the tx.
- `internal/repository/sqlite/merge_repository.go` —
  `CommitWalkByParent`, `CommitInsert(parent2)`, `TreeManifestForCommit`,
  MR CRUD.
- sqlc: new `db/queries/sqlite/merge_request.sql`, extend
  `db/queries/sqlite/branch.sql` (push.sql) for parent2 + lease.

---

## Client surface (new)

| Command | What it does |
|---|---|
| `nipa merge <branch>` | Part 1 local merge (merge <branch> into current checkout) |
| `nipa merge --abort` | cancel conflicted merge |
| `nipa rebase <branch>` | replay current branch onto <branch>, stop on conflict |
| `nipa rebase --continue` / `--abort` | resolve / cancel a stopped rebase |
| `nipa mr create <target> -t <title>` | open an MR for current branch → target |
| `nipa mr list` / `nipa mr show <id>` | list/show MRs (status, mergeable flags) |
| `nipa mr update` / `nipa mr update --rebase` | bring source up to date (merge or rebase target into source) |
| `nipa mr merge <id>` | request server-side merge |
| `nipa mr close <id>` / `nipa mr reopen <id>` | lifecycle |

Packages: `internal/client/usecase/mr.go`, `internal/client/merge/*.go`
(three-way + diff3 + rebase replay), new CLI files `cmd/.../merge.go`,
`mr.go`, `rebase.go`. Localrepo `meta` gains `merge_parent_hash` /
`mr_paths` pending state; a plain `nipa push` finishes a pending merge/rebase.

---

## Edge cases

| Case | Handling |
|---|---|
| Source advanced while merging | `parent_2` = the exact commit(s) resolved against (snapshot). Server accepts any same-project commit hash as parent2. |
| Concurrent pushes to target | `baseTreeHash`/lease checks reject; loser refreshes and re-merges (existing optimistic concurrency). |
| Merge races a fast-forward | `MergeRequest` re-checks inside its tx with a fresh base; if target became an ancestor, degrade to pointer move. |
| Add/add, modify/delete, delete/modify | Always a conflict, never silent. |
| Binary conflicts | Conflict file records the pick (ours/theirs/both). Resolution lives in the editor plugins (Godot/Unity/Unreal) later. |
| MR source branch deleted | MR status → `invalid`; merge refused. |
| Merged MR merge again | Rejected (`merge_commit_id` set). |
| Source already fully inside target | MR merge = fast-forward of target (or direct push of the merge commit with **parent1 only**, since base==target means parent2==parent1 chain is trivial) — prefer pointer move. |
| Rebase while target is behind source | No-op (`source already contains target`); nothing to rewrite. |
| Rebasing a branch with conflict at commit N of M | Stop at N; treeBuilder apply is restartable — continue replays N after resolution. |
| Empty branch head on either side | Merge base walk handles `commit_id == NULL` (empty branch = empty tree at base). |

---

## Implementation order

1. **Server**: merge-base LCA walk + `GetMergeBase` + `treeManifestForCommit`.
2. **Server**: `CommitInsert` parent2 + `PushRequest.parent_2_commit_hash` +
   `ApplyPushRequest.ParentID2` (local merge push path).
3. **Server**: `MergeFastForward` + ff detection plumbing.
4. **Client**: 3-way tree merge + diff3 + `nipa merge` + conflict state +
   pending-merge meta + push parent2 passthrough.
5. **Client**: `nipa merge --abort` / auto-complete via plain push. E2E: clone →
   branch → diverge → merge, clean + conflicting + binary-pick.
6. **Server**: MR schema + CRUD + `CheckMergeRequest` (up-to-date / conflicted /
   ff detection) + server-side auto merge with re-check in one tx.
7. **Client**: `nipa mr` commands.
8. **Rebase**: `WalkCommits` + force-with-lease push (`force_with_lease_commit_hash`
   checked inside `ApplyPush` tx) + `nipa rebase` replay + `--continue/--abort`.
9. **Policy & trains** (later): `require_up_to_date` toggle, merge queues.
---

## Implemented in v1 (server)

Merge requests are live over the REST API with the recommended policy:

- `GET/POST /orgs/{org}/projects/{p}/merge-requests`, `GET .../{id}`,
  `GET .../{id}/check`, `GET .../{id}/diff` (three-dot vs merge base),
  `POST .../{id}/merge|close|reopen`. `{id}` is the per-project sequential
  number (`#1`, `#2`, ...), assigned at creation and unique per project.
- Policy: **require up to date** (GitLab-style). `Check` reports
  `mergeable`, `behind_target`, `up_to_date`, `invalid`, or the terminal
  `merged`/`closed` status. A request is only landable when the target head is
  an ancestor of the source head.
- Landing moves the target pointer to the source head (fast-forward) with the
  usual per-path write checks, plus an optimistic `UpdateCommitIf` CAS on the
  target head. Protected branches can only be updated this way: direct `Push`
  and the direct `MergeFastForward` RPC refuse them, and merging a request into
  a protected target requires project admin.
- `Merge` on a request that is `behind_target`/`up_to_date`/terminal returns a
  409 and the author updates the branch (`nipa mr update`) before retrying.

Not in v1 (tracked for later): `allow_behind` policy, server-side merge commits
(3-way tree build), and `nipa mr` CLI commands.

---

## Merge request reviews (v2)

Reviews are advisory: they are recorded, shown and counted, but they do not gate
merging (which stays fast-forward-only).

### Model

- A **review** is one decision per reviewer per review round, where a round is
  the source head commit it was given for. States: `approved`,
  `changes_requested`, `commented`. A review for an older head is **stale** and
  stops counting; the latest review per reviewer and head wins.
- On every push to the source branch, prior decisions are **dismissed**
  (`dismissed_reason = new_commits`) and the dismissal is written to the
  timeline. Comment-only reviews are not dismissed.
- A reviewer cannot approve or request changes on their own merge request, but
  may comment. A project writer may **dismiss** a review (history is kept); a
  reviewer may **withdraw** their own review (the row is deleted, comment
  threads are unlinked via `ON DELETE SET NULL`).
- **Threads** are top-level conversations (`file_path` empty) or inline comments
  anchored to `old_line`/`new_line` of a line the current diff shows. A thread
  whose anchored line is no longer shown by the new head is **outdated**;
  top-level threads are never outdated. Threads resolve/reopen and their
  comments can be edited/deleted by their author.
- **Review requests** name a user to review; submitting any review by that user
  answers the request, and re-requesting the same reviewer keeps the original
  requester.
- The **timeline** records `pushed`, `review_requested`, `review_request_removed`,
  `review_submitted` and `review_dismissed`, with a `subject` actor for events
  about another user (e.g. the requested reviewer).

### Server surface

- REST (all project read unless noted; mutations need project write):
  `GET .../{id}/reviews`, `GET .../{id}/review-state`,
  `POST .../{id}/reviews` (with inline comment inputs), `DELETE
  .../{id}/reviews/{reviewId}`, `POST .../{id}/reviews/{reviewId}/dismiss`,
  `GET/POST .../{id}/threads`, `POST .../{id}/threads/{threadId}/comments`,
  `PATCH/DELETE .../comments/{commentId}`, `POST .../{id}/threads/{threadId}/resolve`,
  `DELETE .../{id}/threads/{threadId}`, `GET/POST/DELETE
  .../{id}/review-requests`, `GET .../{id}/timeline`.
- Merge request list/detail responses carry a `review` summary
  (`approvals`, `changes_requested`, `dismissed_approvals`,
  `outstanding_reviewers`); only merge requests with a live decision have it.
- gRPC: `SubmitMergeRequestReview`, `ListMergeRequestReviews`,
  `GetMergeRequestReviewState`, `WithdrawMergeRequestReview`,
  `DismissMergeRequestReview`, `ListMergeRequestThreads`,
  `AddMergeRequestComment`, `ReplyMergeRequestThread`, `UpdateMergeRequestComment`,
  `DeleteMergeRequestComment`, `ResolveMergeRequestThread`,
  `DeleteMergeRequestThread`, `ListMergeRequestReviewRequests`,
  `RequestMergeRequestReview`, `RemoveMergeRequestReviewRequest`,
  `ListMergeRequestTimeline`.
- `Push` notifies the review usecase after a successful apply
  (`usecase.Push.WithReviews`), dismissing stale decisions and appending the
  push event. Bookkeeping failures are logged and never fail the push.

### Web UI

The merge request page (`web/src/pages/PullPage.tsx`) renders a review panel
(summary, review form, reviewer picker, requests, history, timeline) next to
the structured diff (`web/src/components/repo/MergeRequestDiff.tsx`), which
supports line-level comments and filtering to lines with open threads. The
merge request list shows the live approval/changes-requested counts.

