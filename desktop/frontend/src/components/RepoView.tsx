import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowDownToLine, FolderTree, GitBranch, GitMerge, Search, Tag, Trash2 } from "lucide-react";
import { desktop, type ChangeSet, type ConflictSet, type OpOutcome, type Repo, type RepoActivity } from "@/api/desktop";
import { BranchesView } from "@/components/branches/BranchesView";
import { ChangesView } from "@/components/changes/ChangesView";
import { SparseDialog } from "@/components/clone/SparseDialog";
import { StorageCard } from "@/components/details/StorageCard";
import { ConflictsView } from "@/components/conflicts/ConflictsView";
import { FilesView } from "@/components/files/FilesView";
import { HistoryView } from "@/components/history/HistoryView";
import { LocksView } from "@/components/locks/LocksView";
import { MergeRequestsView } from "@/components/mr/MergeRequestsView";
import { Progress } from "@/components/changes/CommitBox";
import { hasOpenConflicts, tabs, visibleTabs, type Intent, type TabId } from "@/components/tabs";
import { IgnoreRulesEditor } from "@/components/settings/IgnoreRulesEditor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { locksKey, useLockActions } from "@/hooks/useLocks";
import { useRepoOperation, type OpName } from "@/hooks/useRepoOperation";
import { usePersistedState } from "@/hooks/usePersistedState";
import { useShortcuts } from "@/hooks/useShortcuts";
import { RepoNavContext, type RepoNav } from "@/hooks/useTextTools";
import { shortId } from "@/lib/history";
import { repoSetting } from "@/lib/settings";
import { shortcutHint } from "@/lib/shortcuts";
import { normalizeSelection } from "@/lib/sparse";
import { cn, errorMessage } from "@/lib/utils";

const REPO_POLL_MS = 30_000;

const isTab = (v: unknown): v is TabId => tabs.some((t) => t.id === v);

export type Notify = (message: string, tone?: "error" | "success") => void;
export type { Intent, TabId };

type RepoViewProps = {
  repo: Repo;
  removing: boolean;
  onRemove: (root: string) => void;
  notify: Notify;
  activity?: RepoActivity;
  intent?: Intent;
  leading?: ReactNode;
  onSearch?: () => void;
};

export function RepoView({ repo, removing, onRemove, notify, activity, intent, leading, onSearch }: RepoViewProps) {
  const queryClient = useQueryClient();
  const [tab, setTab] = usePersistedState<TabId>(repoSetting(repo.root, "tab"), "changes", { valid: isTab });
  const [updated, setUpdated] = useState(false);
  useEffect(() => setUpdated(false), [activity]);
  useEffect(() => {
    if (intent && tabs.some((t) => t.id === intent.tab)) setTab(intent.tab as TabId);
  }, [intent]);
  const openNumber = intent?.tab === "mrs" && intent.number ? { number: intent.number, seq: intent.seq } : undefined;
  const openCommit = intent?.tab === "history" && intent.commit ? { id: intent.commit, seq: intent.seq } : undefined;
  const intentFile = intent?.tab === "files" && intent.path ? intent.path : "";
  const [fileNav, setFileNav] = useState<{ path: string; seq: number }>();
  useEffect(() => {
    if (intentFile) setFileNav((prev) => ({ path: intentFile, seq: (prev?.seq ?? 0) + 1 }));
  }, [intentFile, intent?.seq]);
  const showFile = (path: string) => {
    setFileNav((prev) => ({ path, seq: (prev?.seq ?? 0) + 1 }));
    setTab("files");
  };
  const [historyNav, setHistoryNav] = useState<{ path: string; commit?: string; seq: number }>();
  const showHistory = (path: string, commit?: string) => {
    setHistoryNav((prev) => ({ path, commit, seq: (prev?.seq ?? 0) + 1 }));
    setTab("history");
  };
  const [message, setMessage] = useState("");
  const [focusMessage, setFocusMessage] = useState(0);
  const [createMR, setCreateMR] = useState(0);
  useEffect(() => {
    if (tab !== "mrs") setCreateMR(0);
  }, [tab]);
  const [editingSparse, setEditingSparse] = useState(false);

  const changes = useQuery({ queryKey: ["changes", repo.root], queryFn: () => desktop.changes(repo.root), enabled: !repo.error, refetchInterval: REPO_POLL_MS });
  const liveBranch = changes.data?.branch;
  useEffect(() => {
    if (!liveBranch || liveBranch === repo.branch) return;
    void queryClient.invalidateQueries({ queryKey: ["repos"] });
    for (const key of ["branches", "tags", "history"]) void queryClient.invalidateQueries({ queryKey: [key, repo.root] });
  }, [liveBranch, repo.branch, repo.root, queryClient]);
  const pending = changes.data?.pending;
  const conflicts = useQuery({ queryKey: ["conflicts", repo.root], queryFn: () => desktop.conflicts(repo.root), enabled: !repo.error && !!pending });
  const refresh = () => {
    for (const key of ["changes", "diff", "locks", "history", "branches", "tags", "conflicts", "conflict-texts", "conflict-diff", "disk-usage", "files", "file", "blame"]) void queryClient.invalidateQueries({ queryKey: [key, repo.root] });
  };
  const finishing = useRef(false);
  const tagCheckout = useRef("");
  const restored = useRef("");
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
      if (op === "restore") {
        notify(`${restored.current} is now the version from ${shortId(outcome.commitId ?? "")}. Push it to share it, or discard the change.`, "success");
        return;
      }
      void queryClient.invalidateQueries({ queryKey: ["repos"] });
      setUpdated(true);
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
  const hasConflicts = hasOpenConflicts(changes.data);
  const openConflicts = changes.data ? changes.data.conflicts.filter((p) => !changes.data.resolved.includes(p)).length : 0;
  const shownTabs = visibleTabs(hasConflicts);
  const current = tab === "conflicts" && !hasConflicts ? "changes" : tab;
  const incoming = updated || detached ? 0 : (activity?.incoming ?? 0);
  const incomingLabel = incoming >= 100 ? "100+" : String(incoming);

  const { lock } = useLockActions(repo.root, notify);
  const busyNote = () => notify(`Wait until ${opLabel[op!.op].toLowerCase()} finishes`);
  const startUpdate = () => {
    if (op) return busyNote();
    if (detached) return notify(`Checked out at tag ${detached}: switch back to a branch to update`);
    operation.run("update", () => desktop.update(repo.root));
  };
  const writeMessage = () => {
    setTab("changes");
    setFocusMessage((n) => n + 1);
  };
  const lockPath = async (path: string) => {
    try {
      const status = await queryClient.fetchQuery({ queryKey: locksKey(repo.root), queryFn: () => desktop.lockStatus(repo.root) });
      lock.mutate({ branch: status.branch, paths: [path] });
    } catch (err) {
      notify(`Locking failed: ${errorMessage(err)}`);
    }
  };
  const handled = useRef(0);
  useEffect(() => {
    if (!intent?.action || intent.seq === handled.current || repo.error) return;
    handled.current = intent.seq;
    const target = intent.target ?? "";
    switch (intent.action) {
      case "update":
        return startUpdate();
      case "focus-message":
        return writeMessage();
      case "refresh":
        return refresh();
      case "new-mr":
        setTab("mrs");
        return setCreateMR((n) => n + 1);
      case "lock":
        if (intent.path) void lockPath(intent.path);
        return;
      case "switch":
        if (op) return busyNote();
        return switchTo(target);
      case "checkout-tag":
        if (op) return busyNote();
        tagCheckout.current = target;
        return operation.run("switch", () => desktop.checkoutTag(repo.root, target));
    }
  });
  useShortcuts(
    {
      tab: ({ index }) => {
        const t = shownTabs[index];
        if (t) setTab(t.id);
      },
      update: startUpdate,
      refresh,
      focusMessage: writeMessage,
    },
    !repo.error,
  );
  const root = repo.root;
  const nav = useMemo<RepoNav>(
    () => ({
      showCommit: (id) => {
        setHistoryNav((prev) => ({ path: "", commit: id, seq: (prev?.seq ?? 0) + 1 }));
        setTab("history");
      },
      openAt: (path, line) => {
        desktop.openFileAt(root, path, line).catch((err) => notify(`Could not open ${path}: ${errorMessage(err)}`));
      },
    }),
    [root, notify],
  );

  return (
    <RepoNavContext.Provider value={nav}>
      <div className="flex h-full flex-col">
        <header className={cn("drag-region flex h-12 shrink-0 items-center gap-3 border-b border-border px-5", leading && "pl-2")}>
          {leading}
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
          {onSearch && (
              <Button variant="ghost" size="sm" className="no-drag text-muted-foreground" onClick={onSearch} aria-label="Search" title={`Search and run commands (${shortcutHint("palette")})`}>
                <Search />
                <kbd className="font-sans text-[10px]">{shortcutHint("palette")}</kbd>
              </Button>
            )}
          {!repo.error &&
            (op && op.op !== "push" ? (
              <div className="no-drag w-64">
                <Progress op={op} onCancel={operation.cancel} />
              </div>
            ) : (
              <Button
                size="sm"
                onClick={startUpdate}
                disabled={!!op || !!detached}
                title={`${incoming > 0 ? `${incomingLabel} new commit${incoming === 1 ? "" : "s"} on ${repo.branch}` : "Get the latest version of the branch"} (${shortcutHint("update")})`}
              >
                <ArrowDownToLine />
                Update
                {incoming > 0 && <span className="rounded-full bg-accent px-1.5 text-[10px] text-accent-foreground">{incomingLabel}</span>}
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
              {shownTabs.map(({ id, label, Icon }, i) => (
                <button
                  key={id}
                  type="button"
                  role="tab"
                  aria-selected={current === id}
                  onClick={() => setTab(id)}
                  title={i < 9 ? shortcutHint("tab").replace("1–9", String(i + 1)) : undefined}
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
              ) : current === "files" ? (
                <FilesView
                  repo={repo}
                  busy={!!op}
                  notify={notify}
                  open={fileNav}
                  onHistory={showHistory}
                  onRestore={(path, commit) => {
                    restored.current = path;
                    operation.run("restore", () => desktop.restoreRevision(repo.root, path, commit.id));
                  }}
                  onAddSparse={(path) => operation.run("sparse", () => desktop.setSparse(repo.root, normalizeSelection([...(repo.sparse ?? []), path])))}
                />
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
                  onOpenFile={showFile}
                  focusMessage={focusMessage}
                />
              ) : current === "history" ? (
                <HistoryView
                  key={historyNav?.seq ?? 0}
                  path={historyNav?.path}
                  root={repo.root}
                  branch={repo.branch}
                  detached={detached}
                  busy={!!op}
                  notify={notify}
                  onRevert={(commit) => operation.run("revert", () => desktop.revert(repo.root, { target: commit.id }))}
                  onSwitch={switchTo}
                  open={openCommit ?? (historyNav?.commit ? { id: historyNav.commit, seq: historyNav.seq } : undefined)}
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
                <MergeRequestsView root={repo.root} branch={repo.branch} notify={notify} open={openNumber} create={createMR} />
              ) : current === "locks" ? (
                <LocksView root={repo.root} notify={notify} />
              ) : (
                <Details repo={repo} busy={!!op} notify={notify} onEditSparse={() => setEditingSparse(true)} />
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
    </RepoNavContext.Provider>
  );
}

const opLabel: Record<OpName, string> = {
  push: "Push",
  update: "Update",
  sparse: "Checkout change",
  clone: "Clone",
  switch: "Switch",
  revert: "Revert",
  merge: "Merge",
  restore: "Getting the version",
};

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

function Details({ repo, busy, notify, onEditSparse }: { repo: Repo; busy: boolean; notify: Notify; onEditSparse: () => void }) {
  return (
    <div className="h-full overflow-y-auto">
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
      <section aria-label="Storage" className="m-5 max-w-xl rounded-lg border border-border bg-card p-4 text-xs">
        <StorageCard root={repo.root} busy={busy} notify={notify} onEditSparse={onEditSparse} />
      </section>
      <section aria-label="Ignore rules" className="m-5 max-w-xl rounded-lg border border-border bg-card p-4 text-xs">
        <IgnoreRulesEditor root={repo.root} notify={notify} />
      </section>
    </div>
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
