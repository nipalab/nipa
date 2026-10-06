# Merge request feature gaps

Status: **audit (2026-10-05); Batches A and B implemented (2026-10-06)**. This
is a revisit list, not a plan of record. The MR feature is functional end to
end (lifecycle, reviews, threads, locks, diff, commits, webhooks, SPA, daemon
proxy); the items below are things that are absent or half-built, ordered
roughly by impact. Every item cites the code that proves the gap so it can be
re-verified after any of them lands.

Batch A landed the approval gate (item 1, configurable
`branches.required_approvals` plus the always-on objection block), the
lifecycle timeline events (item 5), gRPC parity for reopen/check/diff/commits
(item 6), and the merge/close/reopen half of item 10. Batch B landed the SPA
edit form and inline review comments (7, 8), per-action review webhooks (11),
list filters plus number-keyset pagination (13) and the CLI review surface with
`GetMergeRequest` (9). Items are marked **[done]** below with what shipped; the
rest are still open.

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

10. **[partial] `system` comments never written.** Lifecycle timeline events
    now cover open/merge/close/reopen, but `merge_request_comments.system`
    still has no writer: resolving a thread, pushing to a source branch and
    dismissing a review produce timeline events, not system comments. Either
    emit system comments for those or drop the column.

### Ecosystem gaps

11. **[done] No webhooks for review activity.** `mr.review_submitted`,
    `mr.review_dismissed`, `mr.review_requested`, `mr.review_request_removed`
    and `mr.comment_created` are subscribable and emitted by the review
    usecase through the log-only hook seam.

12. **No labels, milestones, or assignees.** Only reviewers exist
    (`merge_request_review_requests`); the UI synthesizes the reviewer list
    from reviews + pending requests. No labels/milestones tables, fields, or
    code anywhere.

13. **[done] No search / filtering / pagination.** The list takes
    `status/author/source/target` and paginates by number keyset
    (`after`/`next_cursor`); the SPA list page filters and loads more. Free-text
    search is still absent.

14. **No CI / status checks.** Mergeability is solely branch-topology based;
    nothing in the codebase (no check-run model, no external status hook).

15. **Miscellaneous.** No "mark merged manually" for diverged requests
    (`UpdateStatus` is not exposed; close is the only alternative); no email
    notifications anywhere (webhooks only); no MR templates; no WIP title
    prefix detection.

## Suggested batches (when revisiting)

- **Batch A — done:** approval gate on merge (1), timeline events (5), gRPC
  parity for reopen/check/diff/commits (6), lifecycle half of item 10.
- **Batch B — done:** MR edit affordance in SPA (7), inline comments from the
  review composer (8), review/comment webhooks (11), list filters + number
  keyset pagination (13), CLI review surface + `GetMergeRequest` (9).
- **Batch C (schema-heavy, next):** non-FF merge strategies + delete source
  branch (2), draft state (3), branch protection settings (4), labels/
  milestones/assignees (12), CI status checks (14).
