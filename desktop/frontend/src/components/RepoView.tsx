import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowDownToLine, FileDiff, FolderTree, GitBranch, GitMerge, GitPullRequest, History, Info, Lock, Tag, Tags, Trash2 } from "lucide-react";
import { desktop, type ChangeSet, type ConflictSet, type OpOutcome, type Repo } from "@/api/desktop";
import { BranchesView } from "@/components/branches/BranchesView";
import { ChangesView } from "@/components/changes/ChangesView";
import { SparseDialog } from "@/components/clone/SparseDialog";
import { ConflictsView } from "@/components/conflicts/ConflictsView";
import { HistoryView } from "@/components/history/HistoryView";
import { LocksView } from "@/components/locks/LocksView";
import { MergeRequestsView } from "@/components/mr/MergeRequestsView";
import { Progress } from "@/components/changes/CommitBox";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useRepoOperation, type OpName } from "@/hooks/useRepoOperation";
import { cn, errorMessage } from "@/lib/utils";

export type Notify = (message: string, tone?: "error" | "success") => void;

type TabId = "changes" | "conflicts" | "history" | "branches" | "mrs" | "locks" | "details";

const tabs: { id: TabId; label: string; Icon: typeof FileDiff }[] = [
  { id: "changes", label: "Changes", Icon: FileDiff },
  { id: "conflicts", label: "Conflicts", Icon: GitMerge },
  { id: "history", label: "History", Icon: History },
  { id: "branches", label: "Branches & tags", Icon: Tags },
  { id: "mrs", label: "Merge requests", Icon: GitPullRequest },
  { id: "locks", label: "Locks", Icon: Lock },
  { id: "details", label: "Details", Icon: Info },
];

type RepoViewProps = { repo: Repo; removing: boolean; onRemove: (root: string) => void; notify: Notify };

export function RepoView({ repo, removing, onRemove, notify }: RepoViewProps) {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<TabId>("changes");
  const [message, setMessage] = useState("");
  const [editingSparse, setEditingSparse] = useState(false);

  const changes = useQuery({ queryKey: ["changes", repo.root], queryFn: () => desktop.changes(repo.root), enabled: !repo.error });
  const pending = changes.data?.pending;
  const conflicts = useQuery({ queryKey: ["conflicts", repo.root], queryFn: () => desktop.conflicts(repo.root), enabled: !repo.error && !!pending });
  const refresh = () => {
    for (const key of ["changes", "diff", "locks", "history", "branches", "tags", "conflicts", "conflict-texts", "conflict-diff"]) void queryClient.invalidateQueries({ queryKey: [key, repo.root] });
  };
  const finishing = useRef(false);
  const tagCheckout = useRef("");
  const operation = useRepoOperation(repo.root, {
    onDone: (op, outcome) => {
      refresh();
      if (op === "push") {
        setMessage("");
        const what = finishing.current ? "Merged and pushed" : "Pushed";
        finishing.current = false;
        notify(`${what} to ${repo.branch}${outcome.commitId ? ` (${outcome.commitId})` : ""}`, "success");
        return;
      }
      if (op === "revert") {
        notify(...describeRevert(outcome));
        if (outcome.revert?.conflicts?.length) setTab("conflicts");
        return;
      }
      if (op === "merge") {
        notify(...describeMerge(outcome, repo.branch));
        if (outcome.merge?.conflicts?.length) setTab("conflicts");
        return;
      }
      void queryClient.invalidateQueries({ queryKey: ["repos"] });
      if (op === "sparse") notify("Checkout updated", "success");
      else if (op === "switch") notify(tagCheckout.current ? `Checked out tag ${tagCheckout.current}` : `Switched to ${outcome.branch || repo.branch}`, "success");
      else notify(`Up to date with ${outcome.branch || repo.branch}`, "success");
    },
    onError: (op, err) => {
      finishing.current = false;
      refresh();
      notify(`${opLabel[op]} failed: ${errorMessage(err)}`);
    },
    onCancelled: (op) => {
      refresh();
      notify(`${opLabel[op]} cancelled`);
    },
  });
  const op = operation.state;
  const detached = changes.data?.detached;
  const switchTo = (branch: string) => {
    tagCheckout.current = "";
    operation.run("switch", () => desktop.switchBranch(repo.root, branch));
  };
  const revert = (mode: "continue" | "skip" | "abort") => operation.run("revert", () => desktop.revert(repo.root, { mode }));
  const merge = (source: string) => operation.run("merge", () => desktop.merge(repo.root, source));
  const abortMerge = () => operation.run("merge", () => desktop.abortMerge(repo.root));
  const finishMerge = () => {
    finishing.current = true;
    const source = conflicts.data?.theirs.label;
    operation.run("push", () => desktop.push(repo.root, source ? `Merge branch '${source}' into '${repo.branch}'` : `Merge into '${repo.branch}'`));
  };
  const hasConflicts = !!pending && (changes.data?.conflicts.length ?? 0) > 0;
  const openConflicts = changes.data ? changes.data.conflicts.filter((p) => !changes.data.resolved.includes(p)).length : 0;
  const shownTabs = tabs.filter((t) => t.id !== "conflicts" || hasConflicts);
  const current = tab === "conflicts" && !hasConflicts ? "changes" : tab;

  return (
    <div className="flex h-full flex-col">
      <header className="drag-region flex h-12 shrink-0 items-center gap-3 border-b border-border px-5">
        <h1 className="truncate text-sm font-semibold">{repo.name}</h1>
        {!repo.error &&
          (detached ? (
            <Badge className="text-warning" title={`Checked out at tag ${detached}; the clone's branch is ${repo.branch}`}>
              <Tag />
              {detached}
            </Badge>
          ) : (
            <Badge>
              <GitBranch />
              {repo.branch}
            </Badge>
          ))}
        <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={repo.root}>
          {repo.root}
        </span>
        {!repo.error &&
          (op && op.op !== "push" ? (
            <div className="no-drag w-64">
              <Progress op={op} onCancel={operation.cancel} />
            </div>
          ) : (
            <Button
              size="sm"
              onClick={() => operation.run("update", () => desktop.update(repo.root))}
              disabled={!!op || !!detached}
              title="Get the latest version of the branch"
            >
              <ArrowDownToLine />
              Update
            </Button>
          ))}
        <Button variant="danger" size="sm" onClick={() => onRemove(repo.root)} disabled={removing || !!op} title="Remove from list (files stay on disk)">
          <Trash2 />
          Remove
        </Button>
      </header>

      {repo.error ? (
        <div className="m-5 flex items-start gap-3 rounded-lg border border-border bg-danger-soft p-4" role="alert">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-danger" />
          <div className="space-y-1">
            <p className="text-sm font-medium">This clone is unavailable</p>
            <p className="text-xs text-muted-foreground">{repo.error}</p>
            <p className="text-xs text-muted-foreground">It may have been moved or deleted. Remove it from the list or restore the folder.</p>
          </div>
        </div>
      ) : (
        <>
          {changes.data && (
            <Banners
              changes={changes.data}
              conflicts={conflicts.data}
              branch={repo.branch}
              busy={!!op}
              onSwitch={switchTo}
              onRevert={revert}
              onResolve={() => setTab("conflicts")}
              onFinishMerge={finishMerge}
              onAbortMerge={abortMerge}
            />
          )}
          <nav aria-label="Views" role="tablist" className="flex shrink-0 gap-1 border-b border-border px-4">
            {shownTabs.map(({ id, label, Icon }) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={current === id}
                onClick={() => setTab(id)}
                className={cn(
                  "flex items-center gap-1.5 border-b-2 px-2 py-2 text-[13px]",
                  current === id ? "border-accent font-medium" : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="size-3.5" />
                {label}
                {id === "conflicts" && openConflicts > 0 && <span className="rounded-full bg-warning/20 px-1.5 text-[10px] text-warning">{openConflicts}</span>}
              </button>
            ))}
          </nav>
          <div className="min-h-0 flex-1">
            {current === "conflicts" ? (
              <ConflictsView root={repo.root} busy={!!op} notify={notify} />
            ) : current === "changes" ? (
              <ChangesView
                root={repo.root}
                op={op}
                message={message}
                onMessage={setMessage}
                onPush={(msg) => operation.run("push", () => desktop.push(repo.root, msg))}
                onCancelOp={operation.cancel}
                onError={(msg) => notify(msg)}
                notify={notify}
              />
            ) : current === "history" ? (
              <HistoryView
                root={repo.root}
                branch={repo.branch}
                detached={detached}
                busy={!!op}
                notify={notify}
                onRevert={(commit) => operation.run("revert", () => desktop.revert(repo.root, { target: commit.id }))}
              />
            ) : current === "branches" ? (
              <BranchesView
                root={repo.root}
                branch={repo.branch}
                detached={detached}
                busy={!!op}
                notify={notify}
                onSwitch={switchTo}
                onMerge={merge}
                onCheckoutTag={(name) => {
                  tagCheckout.current = name;
                  operation.run("switch", () => desktop.checkoutTag(repo.root, name));
                }}
              />
            ) : current === "mrs" ? (
              <MergeRequestsView root={repo.root} branch={repo.branch} notify={notify} />
            ) : current === "locks" ? (
              <LocksView root={repo.root} notify={notify} />
            ) : (
              <Details repo={repo} busy={!!op} onEditSparse={() => setEditingSparse(true)} />
            )}
          </div>
        </>
      )}
      {editingSparse && (
        <SparseDialog
          repo={repo}
          onClose={() => setEditingSparse(false)}
          onApply={(paths) => {
            setEditingSparse(false);
            operation.run("sparse", () => desktop.setSparse(repo.root, paths));
          }}
        />
      )}
    </div>
  );
}

const opLabel: Record<OpName, string> = { push: "Push", update: "Update", sparse: "Checkout change", clone: "Clone", switch: "Switch", revert: "Revert", merge: "Merge" };

export function describeMerge(outcome: OpOutcome, branch: string): [string, "error" | "success" | undefined] {
  const m = outcome.merge;
  const conflicts = m?.conflicts ?? [];
  if (conflicts.length > 0) return [`The merge stopped: ${conflicts.length} file${conflicts.length === 1 ? "" : "s"} conflict. Resolve them, then finish the merge.`, "error"];
  if (m?.aborted) return ["Merge aborted; the clone is back at the branch head", "success"];
  if (m?.upToDate) return [`${branch} already has those changes`, "success"];
  if (m?.fastForwarded) return [`Fast-forwarded ${branch}`, "success"];
  return [`Merged and pushed to ${branch}`, "success"];
}

export function describeRevert(outcome: OpOutcome): [string, "error" | "success" | undefined] {
  const r = outcome.revert;
  const conflicts = r?.conflicts ?? [];
  if (conflicts.length > 0) return [`Revert stopped: ${conflicts.length} file${conflicts.length === 1 ? "" : "s"} conflict. Resolve them, then continue.`, "error"];
  if (r?.aborted) return ["Revert aborted; the clone is back where it was", "success"];
  if (r?.skipped) return ["Skipped that commit", "success"];
  if (r?.noChange) return ["Nothing to revert: the changes are already gone", "success"];
  return ["Reverted and pushed", "success"];
}

type BannersProps = {
  changes: ChangeSet;
  conflicts?: ConflictSet;
  branch: string;
  busy: boolean;
  onSwitch: (branch: string) => void;
  onRevert: (mode: "continue" | "skip" | "abort") => void;
  onResolve: () => void;
  onFinishMerge: () => void;
  onAbortMerge: () => void;
};

function Banners({ changes, conflicts: set, branch, busy, onSwitch, onRevert, onResolve, onFinishMerge, onAbortMerge }: BannersProps) {
  const open = changes.conflicts.filter((p) => !changes.resolved.includes(p)).length;
  const resolveButton = open > 0 && (
    <Button size="sm" disabled={busy} onClick={onResolve}>
      <GitMerge />
      Resolve conflicts
    </Button>
  );
  if (changes.pending === "revert") {
    const conflicts = open;
    const remaining = changes.pendingRemaining ?? 0;
    return (
      <div role="status" className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-danger-soft px-5 py-2 text-[12px]">
        <AlertTriangle className="size-3.5 text-danger" />
        <span className="mr-auto">
          {conflicts > 0
            ? `A revert is waiting: ${conflicts} conflicting file${conflicts === 1 ? "" : "s"}. Fix ${conflicts === 1 ? "it" : "them"}, then continue.`
            : "A revert is waiting to be continued."}
          {remaining > 1 && ` ${remaining - 1} more commit${remaining === 2 ? "" : "s"} to revert after this one.`}
        </span>
        {resolveButton}
        <Button size="sm" variant="primary" disabled={busy || open > 0} title={open > 0 ? "Resolve every conflict first" : undefined} onClick={() => onRevert("continue")}>
          Continue
        </Button>
        <Button size="sm" disabled={busy} onClick={() => onRevert("skip")} title="Leave this commit as it is and go on with the next">
          Skip
        </Button>
        <Button size="sm" disabled={busy} onClick={() => onRevert("abort")} title="Undo the revert and restore the clone">
          Abort
        </Button>
      </div>
    );
  }
  if (changes.pending === "merge") {
    const source = set?.theirs.label;
    return (
      <div role="status" className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-danger-soft px-5 py-2 text-[12px]">
        <AlertTriangle className="size-3.5 text-danger" />
        <span className="mr-auto">
          {source ? `Merging ${source} into ${branch}: ` : "A merge is waiting: "}
          {open > 0 ? `${open} conflicting file${open === 1 ? "" : "s"} left to resolve.` : "everything is resolved. Finish to push the merge commit."}
        </span>
        {resolveButton}
        <Button size="sm" variant="primary" disabled={busy || open > 0} title={open > 0 ? "Resolve every conflict first" : "Push the merge commit"} onClick={onFinishMerge}>
          Finish merge
        </Button>
        <Button size="sm" disabled={busy} onClick={onAbortMerge} title="Drop the merge and restore the branch head">
          Abort
        </Button>
      </div>
    );
  }
  if (changes.detached) {
    return (
      <div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-muted px-5 py-2 text-[12px]">
        <Tag className="size-3.5 text-warning" />
        <span className="mr-auto">
          Checked out at tag <span className="font-medium">{changes.detached}</span>. You can look and build, but not push: switch back to a branch to make changes.
        </span>
        <Button size="sm" disabled={busy} onClick={() => onSwitch(branch)}>
          <GitBranch />
          Back to {branch}
        </Button>
      </div>
    );
  }
  return null;
}

function Details({ repo, busy, onEditSparse }: { repo: Repo; busy: boolean; onEditSparse: () => void }) {
  return (
    <dl className="m-5 max-w-xl space-y-3 rounded-lg border border-border bg-card p-4 text-xs">
      <Detail label="Server" value={repo.url} />
      <Detail label="Branch" value={repo.branch} />
      <Detail label="Location" value={repo.root} />
      <div>
        <dt className="mb-1 text-muted-foreground">Checkout</dt>
        <dd className="flex flex-wrap gap-1">
          {repo.sparse && repo.sparse.length > 0 ? repo.sparse.map((p) => <Badge key={p}>{p}</Badge>) : <span>Full checkout</span>}
        </dd>
        <Button size="sm" className="mt-2" onClick={onEditSparse} disabled={busy}>
          <FolderTree />
          Change folders…
        </Button>
      </div>
    </dl>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="mb-0.5 text-muted-foreground">{label}</dt>
      <dd className="select-text break-all font-mono text-[11px]">{value}</dd>
    </div>
  );
}
