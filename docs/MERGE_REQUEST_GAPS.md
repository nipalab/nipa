# Merge request feature gaps

Status: **audit (2026-10-05); Batch A implemented (2026-10-05)**. This is a
revisit list, not a plan of record. The MR feature is functional end to end
(lifecycle, reviews, threads, locks, diff, commits, webhooks, SPA, daemon
proxy); the items below are things that are absent or half-built, ordered
roughly by impact. Every item cites the code that proves the gap so it can be
re-verified after any of them lands.

Batch A landed the approval gate (item 1, configurable
`branches.required_approvals` plus the always-on objection block), the
lifecycle timeline events (item 5), gRPC parity for reopen/check/diff/commits
(item 6), and the merge/close/reopen half of item 10. Items are marked
**[done]** below with what shipped; the rest are still open.

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
- Webhooks: `mr.created/updated/synchronized/merged/closed/reopened`
  (`internal/domain/webhook.go`).
- SPA: tabbed MR page (Conversation / Commits / File changes), review
  composer, threads, reviewer requests, timeline, merge box
  (`web/src/components/repo/MergeRequestOverview.tsx`).
- CLI: `nipa mr create|update|list|merge|close` (`internal/client/cli/mr.go`).
- Daemon proxy for 7 MR/review RPCs (`internal/client/daemon/proxy_mr.go`).

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

2. **Fast-forward-only merging.** The MR path is strictly
   `FastForwardForMergeRequest` (`internal/usecase/merge.go:117`): no merge
   commit, no squash, no rebase, no "delete source branch after merge"
   option. A diverged source yields `behind_target` (409) and catch-up has to
   happen client-side via `nipa merge`
   (`tests/suite/mr_test.go:173`). Needs non-FF strategies (three-way/squash)
   or an explicit "merge commit" generation step, plus the delete-source
   checkbox.

3. **No draft / WIP state.** Statuses are only `open|merged|closed`
   (`internal/domain/merge_request.go:9-13`, same CHECK in migration
   `000006`). Draft MRs (hidden from merge, shown in list with a marker) are a
   common expectation.

4. **Branch protection is nearly a bare boolean.** `SetBranchProtection`
   now also carries `required_approvals` (item 1), but everything else is
   missing: dismiss-stale-approvals is hard-wired on (every push dismisses
   decisions), and there are no required reviewers, disallow-force-push /
   disallow-delete settings beyond the current single `is_protected` flag.

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

7. **SPA never edits MR title/description.** `updateMergeRequest` is defined
   (`web/src/api/endpoints.ts:218`) but no edit form uses it; only
   `updateMergeRequestComment` is wired. An edit affordance on the MR page is
   missing entirely.

8. **Review composer cannot create inline comments.** The SPA always posts
   `comments: []` (`MergeRequestOverview.tsx`); inline threads can only be
   created from the Files tab. The backend fully supports
   `comments[]{file_path, old_line, new_line, body}` on review submission.

9. **No CLI review surface.** `nipa mr` is create/update/list/merge/close
   only; there is no `mr view`, and no review/comment/thread-resolve
   subcommands — reviews are unreachable from the terminal. Item 6 landed, so
   the gRPC groundwork for `mr view`/`mr reopen` now exists.

10. **[partial] `system` comments never written.** Lifecycle timeline events
    now cover open/merge/close/reopen, but `merge_request_comments.system`
    still has no writer: resolving a thread, pushing to a source branch and
    dismissing a review produce timeline events, not system comments. Either
    emit system comments for those or drop the column.

### Ecosystem gaps

11. **No webhooks for review activity.** Only the six lifecycle events are
    subscribable (`internal/usecase/webhook.go:53-58`); review
    submitted/dismissed, comments, and review requests emit nothing. Add
    `mr.review.*` / `mr.comment.*` event kinds or fold them into a generic
    `mr.activity` event.

12. **No labels, milestones, or assignees.** Only reviewers exist
    (`merge_request_review_requests`); the UI synthesizes the reviewer list
    from reviews + pending requests. No labels/milestones tables, fields, or
    code anywhere.

13. **No search / filtering / pagination.** `List(projectID, status, limit)`
    only — no author/source/target filters, no free-text search, no cursor
    pagination (tags have a keyset cursor; MRs do not). The SPA list page has
    no pagination UI either (`web/src/pages/MergeRequestsPage.tsx`).

14. **No CI / status checks.** Mergeability is solely branch-topology based;
    nothing in the codebase (no check-run model, no external status hook).

15. **Miscellaneous.** No "mark merged manually" for diverged requests
    (`UpdateStatus` is not exposed; close is the only alternative); no email
    notifications anywhere (webhooks only); no MR templates; no WIP title
    prefix detection.

## Suggested batches (when revisiting)

- **Batch A — done:** approval gate on merge (1), timeline events (5), gRPC
  parity for reopen/check/diff/commits (6), lifecycle half of item 10.
- **Batch B (schema-light, next):** MR edit affordance in SPA (7), inline
  comments from review composer (8), review/comment webhooks (11), list filters
  + keyset pagination (13), CLI review surface + `mr view` (9).
- **Batch C (schema-heavy):** non-FF merge strategies + delete source branch
  (2), draft state (3), branch protection settings (4), labels/milestones/
  assignees (12), CI status checks (14).
