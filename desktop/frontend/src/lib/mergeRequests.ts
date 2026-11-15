import type { Activity, DiffLine, FileDiff, Person, Review, Thread, TimelineEvent } from "@/api/desktop";
import type { LineAnchor } from "@/components/changes/TextDiff";
import { translate } from "@/i18n";

export type ConversationItem = { kind: "event"; at: string; event: TimelineEvent } | { kind: "review"; at: string; review: Review } | { kind: "thread"; at: string; thread: Thread };

// conversation merges the timeline, review decisions and every thread, line
// comments included, in time order; review events are shown through their
// review, which knows the decision.
export function conversation(activity: Activity): ConversationItem[] {
  const items: ConversationItem[] = [
    ...activity.timeline.filter((e) => e.kind !== "review_submitted").map((event) => ({ kind: "event" as const, at: event.createdAt, event })),
    ...activity.reviews.map((review) => ({ kind: "review" as const, at: review.createdAt, review })),
    ...activity.threads.map((thread) => ({ kind: "thread" as const, at: thread.createdAt, thread })),
  ];
  return items.sort((a, b) => a.at.localeCompare(b.at));
}

// threadSnippet returns the diff lines a line comment points at: the anchored
// line and up to context lines above it within the same hunk. It is empty for
// top-level threads and anchors the diff no longer shows.
export function threadSnippet(files: FileDiff[], thread: Thread, context = 3): DiffLine[] {
  if (!thread.path) return [];
  const file = files.find((f) => f.path === thread.path || (!!f.oldPath && f.oldPath === thread.path));
  for (const hunk of file?.hunks ?? []) {
    const lines = hunk.lines ?? [];
    const at = lines.findIndex((l) => (thread.newLine ? l.new === thread.newLine : l.old === thread.oldLine && l.kind !== "added"));
    if (at >= 0) return lines.slice(Math.max(0, at - context), at + 1);
  }
  return [];
}

export function threadAt(thread: Thread, anchor: LineAnchor): boolean {
  if (anchor.newLine) return thread.newLine === anchor.newLine;
  return !thread.newLine && thread.oldLine === anchor.oldLine;
}

export function fileThreads(threads: Thread[], path: string, oldPath = ""): { inline: Thread[]; outdated: Thread[] } {
  const mine = threads.filter((t) => t.path === path || (!!oldPath && t.path === oldPath));
  return { inline: mine.filter((t) => !t.outdated), outdated: mine.filter((t) => t.outdated) };
}

const blockers: Record<string, string> = {
  draft: translate("mergeRequests.itIsADraftMark"),
  changes_requested: translate("mergeRequests.aReviewerRequestedChanges"),
  insufficient_approvals: translate("mergeRequests.itNeedsMoreApprovals"),
  required_reviewers: translate("mergeRequests.aRequiredReviewerHasNot"),
  status_checks: translate("mergeRequests.requiredStatusChecksHaveNot"),
};

export function blockedReason(blockedBy: string): string {
  return blockers[blockedBy] ?? (blockedBy ? translate("mergeRequests.blocked", { value: blockedBy.replace(/_/g, " ") }) : "");
}

const statuses: Record<string, string> = {
  mergeable: translate("mergeRequests.readyToMerge"),
  behind_target: translate("mergeRequests.theTargetBranchMovedOn"),
  up_to_date: translate("mergeRequests.nothingToMergeTheTarget"),
  invalid: translate("mergeRequests.theSourceOrTargetBranch"),
};

export const mergeabilityText = (status: string) => statuses[status] ?? status;

export const strategies = [
  { id: "merge", label: translate("mergeRequests.mergeCommit"), hint: translate("mergeRequests.keepsEveryCommitAndAdds") },
  { id: "squash", label: translate("mergeRequests.squash"), hint: translate("mergeRequests.oneCommitWithTheRequestS") },
  { id: "rebase", label: translate("mergeRequests.rebase"), hint: translate("mergeRequests.replaysTheCommitsOnThe") },
  { id: "ff", label: "Fast-forward", hint: translate("mergeRequests.onlyWhenTheTargetHas") },
] as const;

export type Strategy = (typeof strategies)[number]["id"];

const events: Record<string, string> = {
  opened: translate("mergeRequests.openedThisRequest"),
  pushed: translate("mergeRequests.pushedNewCommits"),
  review_dismissed: translate("mergeRequests.dismissedAReview"),
  review_requested: translate("mergeRequests.askedForAReviewFrom"),
  review_request_removed: translate("mergeRequests.removedTheReviewRequestFor"),
  merged: translate("mergeRequests.mergedThisRequest"),
  closed: translate("mergeRequests.closedThisRequest"),
  reopened: translate("mergeRequests.reopenedThisRequest"),
  ready_for_review: translate("mergeRequests.markedThisRequestReadyFor"),
};

export const eventText = (kind: string) => events[kind] ?? kind.replace(/_/g, " ");

export const reviewText: Record<string, string> = {
  approved: translate("mergeRequests.approvedTheseChanges"),
  changes_requested: translate("mergeRequests.requestedChanges"),
  commented: translate("mergeRequests.reviewedVerb"),
};

// personNames maps user ids to display names from what the request already
// shows (timeline actors and subjects, reviewers) and the project's people.
export function personNames(activity?: Activity, people: Person[] = []): Record<string, string> {
  const names: Record<string, string> = {};
  const add = (p?: Person) => {
    if (p?.id && p.name) names[p.id] = p.name;
  };
  for (const e of activity?.timeline ?? []) {
    add(e.actor);
    add(e.subject);
  }
  for (const r of activity?.reviews ?? []) add(r.reviewer);
  people.forEach(add);
  return names;
}
