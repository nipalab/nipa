# Merge request feature gaps

Status: **audit (2026-10-05); Batches A, B, C1 and C2 implemented
(2026-10-08); C4 + assignees/search/WIP/mark-merged (2026-10-09)**. This is a
revisit list, not a plan of record. The MR feature is functional end to end
(lifecycle, reviews, threads, locks, diff, commits, webhooks, SPA, daemon
proxy); the items below are things that are absent or half-built, ordered
roughly by impact. Every item cites the code that proves the gap so it can be
re-verified after any of them lands.

Batch A landed the approval gate (item 1, configurable
`branches.required_approvals` plus the always-on objection block), the
lifecycle timeline events (item 5), gRPC parity for reopen/check/diff/commits
(item 6), and the merge/close/reopen half of item 10. Batch B landed the SPA
edit form and inline review comments (7, 8), per-action review webhooks (11),
list filters plus number-keyset pagination (13) and the CLI review surface with
`GetMergeRequest` (9). Batch C1 landed draft requests (3), the
dismiss-stale-approvals protection setting and protected-branch delete refusal
(4, first half) and dropped the never-written `merge_request_comments.system`
column (10). Items are marked **[done]** below with what shipped; the rest are
still open.

## What exists today (end to end)

- MR lifecycle: open / list / get / edit title+description / check
  mergeability / merge / close / reopen (`internal/usecase/merge_request.go`).
- Per-project sequential numbering, transaction-safe allocation
  (`db/queries/sqlite/merge_request.sql`, `MergeRequestCreate`).
- Decision reviews (`approved` / `changes_requested` / `commented`) keyed to
  the source head round, auto-dismissed on new pushes
  (`internal/usecase/merge_request_review.go`, `Push.WithReviews`).
- Manual dismiss (project writer) / withdraw (own review), review requests
  with timeline events, top-level + line-anchored threads with outdated
  detection, resolve/reopen threads, comment reply/edit/delete.
- Three-dot diff (`TreeDiffBetween`) and first-parent commit walk endpoints.
- Binary file-lock integration in every transition (create/merge/close/
  reopen/push, admin-merges-author lock top-up).
- Protected-target merge restricted to project admins
  (`internal/usecase/merge.go:117-146`).
- Webhooks: `mr.created/updated/synchronized/merged/closed/reopened` plus
  `mr.review_submitted/review_dismissed/review_requested/
  review_request_removed/comment_created` (`internal/domain/webhook.go`).
- SPA: tabbed MR page (Conversation / Commits / File changes), title/description
  edit dialog, review composer with inline-comment drafts, threads, reviewer
  requests, timeline, merge box with `blocked_by` hints; list page has
  status/author/source/target filters and Load-more pagination
  (`web/src/components/repo/MergeRequestOverview.tsx`,
  `web/src/pages/MergeRequestsPage.tsx`).
- CLI: `nipa mr create|update|list|view|close|reopen|merge|review|comments|
  comment|reply|resolve|timeline|requests|request-review|unrequest-review|diff`
  (`internal/client/cli/mr.go`, `mr_review.go`).
- Daemon proxy for 11 MR/review RPCs (`internal/client/daemon/proxy_mr.go`).

## Missing features

### Core workflow gaps

1. **[done] No approval gate on merge.** `Merge`
   (`internal/usecase/merge_request.go`) now fills
   `Mergeability.BlockedBy` through the `WithReview` seam: live
   `changes_requested` always blocks and a target branch's
   `branches.required_approvals` must be met by live approvals; `Merge`
   returns a 409 with the reason. The policy is set through
   `SetBranchProtection` (`PUT .../branches/{name}/protection`, optional
   `required_approvals` kept when absent) and surfaced in the SPA merge box.
   Remaining follow-up: none known.

2. **[done] Fast-forward-only merging.** The server authors commits for the
   `merge` (two-parent merge commit), `squash` (single commit with the request
   title) and `rebase` (per-commit replay) strategies, reusing the hoisted
   `internal/merge` three-way engine; conflicts are refused with the conflicted
   paths and nothing lands. `delete_source` removes the source branch after a
   successful merge. Surfaced through REST `POST .../merge {strategy,
   delete_source}`, gRPC `MergeMergeRequestRequest.strategy/delete_source`,
   `nipa mr merge --strategy/--delete-source` and the SPA merge box.

3. **[done] No draft / WIP state.** `merge_requests.is_draft` (base migration
   `000006`) carries the state; `Create` takes a draft flag and `Update` PATCH
   toggles it (`SetDraft`), `Check`/`Merge` block drafts with
   `blocked_by: "draft"`, marking ready emits the `ready_for_review` timeline
   event and `mr.ready_for_review` webhook, and the list filters on `draft`.
   CLI `mr create --draft` / `mr update --draft=false` / `mr ready`; SPA
   composer, edit dialog, list filter and merge box. No WIP title-prefix
   detection (item 15).

4. **[done] Branch protection beyond a boolean.** `SetBranchProtection`
   carries `required_approvals`, `dismiss_stale_approvals` (off carries live
   decisions onto the new head), `require_status_checks` + required check
   names, and named `required_reviewers` whose approval is mandatory. Deleting
   a protected branch returns a 409 until it is unprotected. Disallow
   force-push is moot — the server never rewrites a branch head.

### Half-built things (existing seams with unused surface)

5. **[done] Timeline events `opened`/`merged`/`closed`/`reopened` were
   defined but never emitted.** `MergeRequestReview.NoteEvent` is now called by
   `Create` (inside the tx), `Merge` (with the landed commit id+hash),
   `Close` and `Reopen` through the `WithReview` seam; the SPA's existing
   render code for those kinds is live.

6. **[done] gRPC parity holes.** `ReopenMergeRequest`, `CheckMergeRequest`,
   `ListMergeRequestCommits` and `GetMergeRequestDiff` are implemented, plus
   `MergeabilityDetail.blocked_by`, `MergeRequestDetail.review` (List attaches
   summaries) and `Branch.required_approvals`.

7. **[done] SPA never edits MR title/description.** The MR page has an Edit
   dialog for the author or a project admin while the request is open
   (`MergeRequestPage.tsx`, PATCH via `updateMergeRequest`).

8. **[done] Review composer cannot create inline comments.** The composer now
   keeps inline-comment drafts (file, side, line, body) and posts them as
   `comments[]` on the decision.

9. **[done] No CLI review surface.** `nipa mr` gained `view`, `reopen`,
   `review`, `comments`, `comment`, `reply`, `resolve`, `timeline`, `requests`,
   `request-review`, `unrequest-review` and `diff` (`internal/client/cli/
   mr_review.go`), backed by the `GetMergeRequest` RPC and transport wrappers;
   the daemon proxies get/check/reopen/submit-review. Remaining nicety: the
   review-request commands take a base36 user id (no user-lookup RPC).

10. **[done] `system` comments never written.** The never-written
    `merge_request_comments.system` column and its proto field were dropped;
    the timeline is the single representation for lifecycle and review
    bookkeeping.

### Ecosystem gaps

11. **[done] No webhooks for review activity.** `mr.review_submitted`,
    `mr.review_dismissed`, `mr.review_requested`, `mr.review_request_removed`
    and `mr.comment_created` are subscribable and emitted by the review
    usecase through the log-only hook seam.

12. **[partial] Assignees done, labels deferred.** `merge_request_assignees`
    (base-set through `POST .../assignees` / gRPC
    `SetMergeRequestAssignees` / `nipa mr assign`) with list filters and SPA
    pickers. Labels/milestones were consciously skipped: milestones need an
    issue tracker, labels are deferred until needed.

13. **[done] No search / filtering / pagination.** The list takes
    `status/author/source/target/draft/assignee` plus free-text `search`
    (title/description LIKE) and paginates by number keyset
    (`after`/`next_cursor`); the SPA list page filters, searches and loads
    more.

14. **[done] No CI / status checks.** `merge_request_checks` is keyed by
    (request, head commit, name); `POST/GET .../checks`, gRPC
    `ReportMergeRequestCheck`/`ListMergeRequestChecks` and `nipa mr check`/
    `mr checks` report and list. A target branch with `require_status_checks`
    blocks merging (`blocked_by: "status_checks"`) until every required check
    succeeds for the current head; a push starts a clean slate. Reporting
    emits `mr.check_reported` and the SPA merge box lists checks.

15. **[partial] Miscellaneous.** "Mark merged manually" exists (`POST
    .../mark-merged`, `nipa mr mark-merged`) for changes that landed outside
    the flow, and WIP/Draft title prefixes auto-draft (removing the prefix
    marks ready). Still absent: email notifications (webhooks only) and MR
    templates.

## Suggested batches (when revisiting)

- **Batch A — done:** approval gate on merge (1), timeline events (5), gRPC
  parity for reopen/check/diff/commits (6), lifecycle half of item 10.
- **Batch B — done:** MR edit affordance in SPA (7), inline comments from the
  review composer (8), review/comment webhooks (11), list filters + number
  keyset pagination (13), CLI review surface + `GetMergeRequest` (9).
- **Batch C — done:** C1 — draft state (3), dismiss-stale setting +
  protected-delete refusal (4), `system` column dropped (10). C2 — non-FF
  merge strategies + delete source branch (2). C3 — assignees (12, labels
  deferred). C4 — required reviewers (4), status checks (14), free-text search
  (13), WIP detection and mark-merged (15). Email notifications and MR
  templates remain open.
