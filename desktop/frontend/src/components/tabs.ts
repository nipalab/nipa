import { FileDiff, Folders, GitMerge, GitPullRequest, History, Info, Lock, Tags } from "lucide-react";

export type TabId = "files" | "changes" | "conflicts" | "history" | "branches" | "mrs" | "locks" | "details";

export const tabs: { id: TabId; label: string; Icon: typeof FileDiff }[] = [
  { id: "files", label: "Files", Icon: Folders },
  { id: "changes", label: "Changes", Icon: FileDiff },
  { id: "conflicts", label: "Conflicts", Icon: GitMerge },
  { id: "history", label: "History", Icon: History },
  { id: "branches", label: "Branches & tags", Icon: Tags },
  { id: "mrs", label: "Merge requests", Icon: GitPullRequest },
  { id: "locks", label: "Locks", Icon: Lock },
  { id: "details", label: "Details", Icon: Info },
];

// RepoAction is something a palette or shortcut asks the repository view to
// do; target names the branch or tag for switch and checkout-tag.
export type RepoAction = "update" | "focus-message" | "switch" | "checkout-tag" | "new-mr" | "lock" | "refresh";

// Intent asks the view to show a tab (and a merge request, commit or file)
// or to run an action; seq changes for every request so the same target can
// be opened twice.
export type Intent = { tab: string; number?: number; commit?: string; path?: string; action?: RepoAction; target?: string; seq: number };
