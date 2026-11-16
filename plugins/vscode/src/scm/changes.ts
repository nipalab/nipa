import type { StatusResponse } from "../daemon/types";

export type ChangeKind = "added" | "modified" | "deleted" | "untracked" | "conflict" | "resolved";

export interface Change {
  path: string;
  kind: ChangeKind;
}

export interface Changes {
  branch: string;
  detachedTag?: string;
  pending?: string;
  pendingRemaining: number;
  staged: Change[];
  unstaged: Change[];
  conflicts: Change[];
}

export function groupStatus(status: StatusResponse): Changes {
  const added = new Set(status.added ?? []);
  const staged: Change[] = [
    ...(status.staged ?? []).map((path) => ({ path, kind: added.has(path) ? ("added" as const) : ("modified" as const) })),
    ...(status.deleted ?? []).map((path) => ({ path, kind: "deleted" as const })),
  ];
  const unstaged: Change[] = [
    ...(status.modified ?? []).map((path) => ({ path, kind: "modified" as const })),
    ...(status.missing ?? []).map((path) => ({ path, kind: "deleted" as const })),
    ...(status.untracked ?? []).map((path) => ({ path, kind: "untracked" as const })),
  ];
  const resolved = new Set(status.resolved ?? []);
  const conflicts: Change[] = (status.conflicts ?? []).map((path) => ({
    path,
    kind: resolved.has(path) ? "resolved" : "conflict",
  }));
  const byPath = (a: Change, b: Change) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
  return {
    branch: status.branch,
    detachedTag: status.head?.kind === "tag" ? status.head.name : undefined,
    pending: status.pending || undefined,
    pendingRemaining: status.pending_remaining ?? 0,
    staged: staged.sort(byPath),
    unstaged: unstaged.sort(byPath),
    conflicts: conflicts.sort(byPath),
  };
}

export const KIND_LETTER: Record<ChangeKind, string> = {
  added: "A",
  modified: "M",
  deleted: "D",
  untracked: "U",
  conflict: "C",
  resolved: "R",
};

export const KIND_LABEL: Record<ChangeKind, string> = {
  added: "Added",
  modified: "Modified",
  deleted: "Deleted",
  untracked: "Untracked",
  conflict: "Conflict",
  resolved: "Resolved",
};

export function hasSnapshot(kind: ChangeKind): boolean {
  return kind === "modified" || kind === "deleted" || kind === "conflict" || kind === "resolved";
}
