import type { Activity, DiffLine, FileDiff, Review, Thread, TimelineEvent } from "@/api/desktop";
import type { LineAnchor } from "@/components/changes/TextDiff";

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
  draft: "It is a draft. Mark it ready for review first.",
  changes_requested: "A reviewer requested changes.",
  insufficient_approvals: "It needs more approvals.",
  required_reviewers: "A required reviewer has not approved yet.",
  status_checks: "Required status checks have not passed.",
};

export function blockedReason(blockedBy: string): string {
  return blockers[blockedBy] ?? (blockedBy ? `Blocked: ${blockedBy.replace(/_/g, " ")}` : "");
}

const statuses: Record<string, string> = {
  mergeable: "Ready to merge",
  behind_target: "The target branch moved on. A fast-forward is not possible; pick another strategy.",
  up_to_date: "Nothing to merge: the target already has these changes.",
  invalid: "The source or target branch is gone.",
};

export const mergeabilityText = (status: string) => statuses[status] ?? status;

export const strategies = [
  { id: "merge", label: "Merge commit", hint: "Keeps every commit and adds a merge commit" },
  { id: "squash", label: "Squash", hint: "One commit with the request's title" },
  { id: "rebase", label: "Rebase", hint: "Replays the commits on the target" },
  { id: "ff", label: "Fast-forward", hint: "Only when the target has not moved" },
] as const;

export type Strategy = (typeof strategies)[number]["id"];

const events: Record<string, string> = {
  opened: "opened this request",
  pushed: "pushed new commits",
  review_dismissed: "dismissed a review",
  review_requested: "asked for a review from",
  review_request_removed: "removed the review request for",
  merged: "merged this request",
  closed: "closed this request",
  reopened: "reopened this request",
  ready_for_review: "marked this request ready for review",
};

export const eventText = (kind: string) => events[kind] ?? kind.replace(/_/g, " ");

export const reviewText: Record<string, string> = {
  approved: "approved these changes",
  changes_requested: "requested changes",
  commented: "reviewed",
};
