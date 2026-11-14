import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowDownToLine, FolderTree, GitBranch, GitMerge, Search, Tag, Trash2 } from "lucide-react";
import { desktop, type ChangeSet, type ConflictSet, type OpOutcome, type Repo, type RepoActivity, type ShelveInput } from "@/api/desktop";
import { BranchesView } from "@/components/branches/BranchesView";
import { ChangesView } from "@/components/changes/ChangesView";
import { useSyncGuard, type SyncAction } from "@/components/changes/SyncGuard";
import { SparseDialog } from "@/components/clone/SparseDialog";
import { StorageCard } from "@/components/details/StorageCard";
import { ConflictsView } from "@/components/conflicts/ConflictsView";
import { FilesView } from "@/components/files/FilesView";
import { HistoryView } from "@/components/history/HistoryView";
import { ActivityView } from "@/components/activity/ActivityView";
import { LocksView } from "@/components/locks/LocksView";
import { MergeRequestsView } from "@/components/mr/MergeRequestsView";
import { Progress } from "@/components/changes/CommitBox";
import { hasOpenConflicts, tabs, visibleTabs, simpleTabIds, type Intent, type TabId } from "@/components/tabs";
import { IgnoreRulesEditor } from "@/components/settings/IgnoreRulesEditor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { locksKey, useLockActions } from "@/hooks/useLocks";
import { useRepoOperation, type OpName } from "@/hooks/useRepoOperation";
import { usePersistedState } from "@/hooks/usePersistedState";
import { useShortcuts } from "@/hooks/useShortcuts";
import { RepoNavContext, type RepoNav } from "@/hooks/useTextTools";
import { shortId } from "@/lib/history";
import { labels, useSimple } from "@/lib/mode";
import { repoSetting } from "@/lib/settings";
import { shortcutHint } from "@/lib/shortcuts";
import { normalizeSelection } from "@/lib/sparse";
import { cn, errorMessage } from "@/lib/utils";
import { Trans, useTranslation } from "react-i18next";
import { translate } from "@/i18n";

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
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const simple = useSimple();
  const [tab, setTab] = usePersistedState<TabId>(repoSetting(repo.root, "tab"), "changes", { valid: isTab });
  const [pinned, setPinned] = useState<TabId>();
  const [updated, setUpdated] = useState(false);
  useEffect(() => setUpdated(false), [activity]);
  useEffect(() => {
    if (intent && tabs.some((t) => t.id === intent.tab)) {
      setTab(intent.tab as TabId);
      setPinned(simple && !simpleTabIds.includes(intent.tab as TabId) ? (intent.tab as TabId) : undefined);
    }
  }, [intent]);
  useEffect(() => {
    if (pinned && (!simple || tab !== pinned)) setPinned(undefined);
  }, [pinned, simple, tab]);
  const intentMr = intent?.tab === "mrs" && intent.number ? intent.number : 0;
  const [openNumber, setOpenNumber] = useState<{ number: number; seq: number }>();
  useEffect(() => {
    if (intentMr) setOpenNumber((prev) => ({ number: intentMr, seq: Math.max((prev?.seq ?? 0) + 1, intent?.seq ?? 0) }));
  }, [intentMr, intent?.seq]);
  const showMergeRequest = (number: number) => {
    setOpenNumber((prev) => ({ number, seq: (prev?.seq ?? 0) + 1 }));
    setTab("mrs");
  };
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
    for (const key of ["changes", "diff", "locks", "history", "branches", "tags", "conflicts", "conflict-texts", "conflict-diff", "disk-usage", "files", "file", "blame", "shelves", "shelf-files", "activity"]) void queryClient.invalidateQueries({ queryKey: [key, repo.root] });
  };
  const finishing = useRef(false);
  const tagCheckout = useRef("");
  const restored = useRef("");
  const afterShelve = useRef<(() => void) | null>(null);
  const operation = useRepoOperation(repo.root, {
    onDone: (op, outcome) => {
      refresh();
      if (op === "shelve") {
        const [text, tone, conflicted] = describeShelve(outcome);
        notify(text, tone);
        if (conflicted) setTab("conflicts");
        const next = afterShelve.current;
        afterShelve.current = null;
        if (next && outcome.shelf) setTimeout(next, 0);
        return;
      }
      if (op === "push") {
        setMessage("");
        const what = finishing.current ? translate("repoView.mergedAndPushed") : translate("repoView.pushed");
        finishing.current = false;
        notify(translate("repoView.to", { what, branch: repo.branch, commitId: outcome.commitId ? translate("repoView.text2", { commitId: outcome.commitId }) : "" }), "success");
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
        notify(translate("repoView.isNowTheVersion", { current: restored.current, value: shortId(outcome.commitId ?? "") }), "success");
        return;
      }
      void queryClient.invalidateQueries({ queryKey: ["repos"] });
      setUpdated(true);
      if (op === "sparse") notify(translate("repoView.checkoutUpdated"), "success");
      else if (op === "switch") notify(tagCheckout.current ? translate("repoView.checkedOutTag", { current: tagCheckout.current }) : translate("repoView.switchedTo", { branch: outcome.branch || repo.branch }), "success");
      else notify(translate("repoView.upToDateWith", { branch: outcome.branch || repo.branch }), "success");
    },
    onError: (op, err) => {
      finishing.current = false;
      afterShelve.current = null;
      refresh();
      notify(translate("repoView.failed", { value: opLabel[op], value2: errorMessage(err) }));
    },
    onCancelled: (op) => {
      afterShelve.current = null;
      refresh();
      notify(translate("repoView.cancelled", { value: opLabel[op] }));
    },
  });
  const op = operation.state;
  const detached = changes.data?.detached;
  const shelve = (input: Partial<ShelveInput>) => operation.run("shelve", () => desktop.shelve(repo.root, input));
  const shelveThen = (paths: string[], name: string, next: () => void) => {
    afterShelve.current = next;
    shelve({ name, paths });
  };
  const localChanges = changes.data ? changes.data.staged.length + changes.data.unstaged.length > 0 : undefined;
  const syncGuard = useSyncGuard(repo.root, localChanges, shelveThen);
  const guarded = (action: SyncAction) => void syncGuard.guard(action);
  const switchTo = (branch: string) =>
    guarded({
      target: { branch },
      verb: translate("repoView.switchingTo", { branch }),
      shelfName: translate("repoView.beforeSwitchingTo", { branch }),
      start: () => {
        tagCheckout.current = "";
        operation.run("switch", () => desktop.switchBranch(repo.root, branch));
      },
    });
  const checkoutTag = (name: string) =>
    guarded({
      target: { tag: name },
      verb: translate("repoView.checkingOutTag", { name }),
      shelfName: translate("repoView.beforeCheckingOut", { name }),
      start: () => {
        tagCheckout.current = name;
        operation.run("switch", () => desktop.checkoutTag(repo.root, name));
      },
    });
  const revert = (mode: "continue" | "skip" | "abort") => operation.run("revert", () => desktop.revert(repo.root, { mode }));
  const merge = (source: string) => operation.run("merge", () => desktop.merge(repo.root, source));
  const abortMerge = () => operation.run("merge", () => desktop.abortMerge(repo.root));
  const finishMerge = () => {
    finishing.current = true;
    const source = conflicts.data?.theirs.label;
    operation.run("push", () => desktop.push(repo.root, source ? translate("repoView.mergeBranchInto", { source, branch: repo.branch }) : translate("repoView.mergeInto", { branch: repo.branch })));
  };
  const hasConflicts = hasOpenConflicts(changes.data);
  const openConflicts = changes.data ? changes.data.conflicts.filter((p) => !changes.data.resolved.includes(p)).length : 0;
  const baseTabs = visibleTabs(hasConflicts, simple);
  const extraTab = simple && pinned && !baseTabs.some((t) => t.id === pinned) ? tabs.find((t) => t.id === pinned) : undefined;
  const shownTabs = extraTab ? [...baseTabs, extraTab] : baseTabs;
  const current = shownTabs.some((t) => t.id === tab) ? tab : "changes";
  const incoming = updated || detached ? 0 : (activity?.incoming ?? 0);
  const incomingLabel = incoming >= 100 ? "100+" : String(incoming);

  const { lock } = useLockActions(repo.root, notify);
  const busyNote = () => notify(translate("repoView.waitUntilFinishes", { toLowerCase: opLabel[op!.op].toLowerCase() }));
  const startUpdate = () => {
    if (op) return busyNote();
    if (detached) return notify(translate("repoView.checkedOutAtTag", { detached }));
    guarded({ target: {}, verb: translate("repoView.updating"), shelfName: translate("repoView.beforeUpdating"), start: () => operation.run("update", () => desktop.update(repo.root)) });
  };
  const writeMessage = () => {
    setTab("changes");
    setFocusMessage((n) => n + 1);
  };
  const lockPath = async (path: string, scope?: string) => {
    try {
      const status = await queryClient.fetchQuery({ queryKey: locksKey(repo.root), queryFn: () => desktop.lockStatus(repo.root) });
      lock.mutate({ branch: scope ?? status.branch, paths: [path] });
    } catch (err) {
      notify(translate("repoView.lockingFailed", { value: errorMessage(err) }));
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
        if (intent.path) void lockPath(intent.path, intent.branch);
        return;
      case "switch":
        if (op) return busyNote();
        return switchTo(target);
      case "checkout-tag":
        if (op) return busyNote();
        return checkoutTag(target);
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
        desktop.openFileAt(root, path, line).catch((err) => notify(translate("repoView.couldNotOpen", { path, value: errorMessage(err) })));
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
              <Badge className="text-warning" title={translate("repoView.checkedOutAtTag2", { detached, branch: repo.branch })}>
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
              <Button variant="ghost" size="sm" className="no-drag text-muted-foreground" onClick={onSearch} aria-label={t("repoView.search")} title={translate("repoView.searchAndRunCommands", { value: shortcutHint("palette") })}>
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
                data-tour="update"
                onClick={startUpdate}
                disabled={!!op || !!detached || syncGuard.checking}
                title={translate("repoView.text", { incoming: incoming > 0 ? translate("repoView.newCommitsOn", { count: incoming, incoming: incomingLabel, branch: repo.branch }) : translate("repoView.getTheLatestVersionOf"), value: shortcutHint("update") })}
              >
                <ArrowDownToLine />
                {labels(simple).update}
                {incoming > 0 && <span className="rounded-full bg-accent px-1.5 text-[10px] text-accent-foreground">{incomingLabel}</span>}
              </Button>
            ))}
          <Button variant="danger" size="sm" onClick={() => onRemove(repo.root)} disabled={removing || !!op} title={t("repoView.removeFromListFilesStay")}>
            <Trash2 />
            {t("repoView.remove")}
          </Button>
        </header>

        {repo.error ? (
          <div className="m-5 flex items-start gap-3 rounded-lg border border-border bg-danger-soft p-4" role="alert">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-danger" />
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("repoView.thisCloneIsUnavailable")}</p>
              <p className="text-xs text-muted-foreground">{repo.error}</p>
              <p className="text-xs text-muted-foreground">{t("repoView.itMayHaveBeenMoved")}</p>
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
                onAbortShelf={() => operation.run("shelve", () => desktop.abortShelfApply(repo.root))}
              />
            )}
            <nav aria-label={t("repoView.views")} role="tablist" className="flex shrink-0 gap-1 border-b border-border px-4">
              {shownTabs.map(({ id, label, Icon }, i) => (
                <button
                  key={id}
                  type="button"
                  role="tab"
                  data-tour={`tab-${id}`}
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
                  onShelve={shelve}
                  onApplyShelf={(shelf, keep) => operation.run("shelve", () => desktop.applyShelf(repo.root, shelf.id, keep))}
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
                  onCheckoutTag={checkoutTag}
                />
              ) : current === "mrs" ? (
                <MergeRequestsView root={repo.root} branch={repo.branch} notify={notify} open={openNumber} create={createMR} />
              ) : current === "locks" ? (
                <LocksView root={repo.root} notify={notify} />
              ) : current === "activity" ? (
                <ActivityView root={repo.root} onOpenCommit={(id) => showHistory("", id)} onOpenMergeRequest={showMergeRequest} onOpenLocks={() => setTab("locks")} />
              ) : (
                <Details repo={repo} busy={!!op} notify={notify} onEditSparse={() => setEditingSparse(true)} />
              )}
            </div>
          </>
        )}
        {syncGuard.dialog}
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
  push: translate("repoView.push"),
  update: translate("repoView.update"),
  sparse: translate("repoView.checkoutChange"),
  clone: translate("repoView.clone"),
  switch: translate("repoView.switch"),
  revert: translate("repoView.revert"),
  merge: translate("repoView.merge"),
  restore: translate("repoView.gettingTheVersion"),
  shelve: translate("repoView.shelving"),
};

export function describeShelve(outcome: OpOutcome): [string, "error" | "success" | undefined, boolean] {
  if (outcome.shelf) {
    const n = outcome.shelf.entries?.length ?? 0;
    return [translate("repoView.shelved", { count: n, name: outcome.shelf.name }), "success", false];
  }
  const a = outcome.shelfApply;
  if (a?.aborted) return [translate("repoView.stoppedApplyingTheShelfThe"), "success", false];
  const conflicts = a?.conflicts ?? [];
  if (conflicts.length > 0) return [translate("repoView.applyStopped", { name: a?.name, count: conflicts.length }), "error", true];
  return [translate("repoView.applied", { value: a?.name ?? translate("repoView.theShelf"), dropped: a?.dropped ? translate("repoView.andDeletedTheShelf") : "" }), "success", false];
}

export function describeMerge(outcome: OpOutcome, branch: string): [string, "error" | "success" | undefined] {
  const m = outcome.merge;
  const conflicts = m?.conflicts ?? [];
  if (conflicts.length > 0) return [translate("repoView.mergeStopped", { count: conflicts.length }), "error"];
  if (m?.aborted) return [translate("repoView.mergeAbortedTheCloneIs"), "success"];
  if (m?.upToDate) return [translate("repoView.alreadyHasThoseChanges", { branch }), "success"];
  if (m?.fastForwarded) return [translate("repoView.fastForwarded", { branch }), "success"];
  return [translate("repoView.mergedAndPushedTo", { branch }), "success"];
}

export function describeRevert(outcome: OpOutcome): [string, "error" | "success" | undefined] {
  const r = outcome.revert;
  const conflicts = r?.conflicts ?? [];
  if (conflicts.length > 0) return [translate("repoView.revertStopped", { count: conflicts.length }), "error"];
  if (r?.aborted) return [translate("repoView.revertAbortedTheCloneIs"), "success"];
  if (r?.skipped) return [translate("repoView.skippedThatCommit"), "success"];
  if (r?.noChange) return [translate("repoView.nothingToRevertTheChanges"), "success"];
  return [translate("repoView.revertedAndPushed"), "success"];
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
  onAbortShelf: () => void;
};

function Banners({ changes, conflicts: set, branch, busy, onSwitch, onRevert, onResolve, onFinishMerge, onAbortMerge, onAbortShelf }: BannersProps) {
  const { t } = useTranslation();
  const open = changes.conflicts.filter((p) => !changes.resolved.includes(p)).length;
  const resolveButton = open > 0 && (
    <Button size="sm" disabled={busy} onClick={onResolve}>
      <GitMerge />
      {t("repoView.resolveConflicts")}
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
            ? translate("repoView.revertWaiting", { count: conflicts })
            : translate("repoView.aRevertIsWaitingTo")}
          {remaining > 1 && translate("repoView.text3", { value: translate("repoView.revertMore", { count: remaining - 1 }) })}
        </span>
        {resolveButton}
        <Button size="sm" variant="primary" disabled={busy || open > 0} title={open > 0 ? translate("repoView.resolveEveryConflictFirst") : undefined} onClick={() => onRevert("continue")}>
          {t("repoView.continue")}
        </Button>
        <Button size="sm" disabled={busy} onClick={() => onRevert("skip")} title={t("repoView.leaveThisCommitAsIt")}>
          {t("repoView.skip")}
        </Button>
        <Button size="sm" disabled={busy} onClick={() => onRevert("abort")} title={t("repoView.undoTheRevertAndRestore")}>
          {t("repoView.abort")}
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
          {source ? translate("repoView.mergingInto", { source, branch }) : translate("repoView.aMergeIsWaiting")}
          {open > 0 ? translate("repoView.conflictsLeft", { count: open }) : translate("repoView.everythingIsResolvedFinishTo")}
        </span>
        {resolveButton}
        <Button size="sm" variant="primary" disabled={busy || open > 0} title={open > 0 ? translate("repoView.resolveEveryConflictFirst") : translate("repoView.pushTheMergeCommit")} onClick={onFinishMerge}>
          {t("repoView.finishMerge")}
        </Button>
        <Button size="sm" disabled={busy} onClick={onAbortMerge} title={t("repoView.dropTheMergeAndRestore")}>
          {t("repoView.abort")}
        </Button>
      </div>
    );
  }
  if (changes.pending === "shelf") {
    const name = set?.theirs.label.replace(/^shelf /, "");
    return (
      <div role="status" className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-danger-soft px-5 py-2 text-[12px]">
        <AlertTriangle className="size-3.5 text-danger" />
        <span className="mr-auto">
          {name ? translate("repoView.applyingShelf", { name }) : translate("repoView.aShelfIsBeingApplied")}
          {translate("repoView.applyLeft", { count: open })}
        </span>
        {resolveButton}
        <Button size="sm" disabled={busy} onClick={onAbortShelf} title={t("repoView.putTheFilesBackAs")}>
          {t("repoView.abort")}
        </Button>
      </div>
    );
  }
  if (changes.detached) {
    return (
      <div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-muted px-5 py-2 text-[12px]">
        <Tag className="size-3.5 text-warning" />
        <span className="mr-auto">
          <Trans i18nKey="repoView.detachedNotice" values={{ tag: changes.detached }} components={[<span className="font-medium" />]} />
        </span>
        <Button size="sm" disabled={busy} onClick={() => onSwitch(branch)}>
          <GitBranch />
          {t("repoView.backTo", { branch })}
        </Button>
      </div>
    );
  }
  return null;
}

function Details({ repo, busy, notify, onEditSparse }: { repo: Repo; busy: boolean; notify: Notify; onEditSparse: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="h-full overflow-y-auto">
      <dl className="m-5 max-w-xl space-y-3 rounded-lg border border-border bg-card p-4 text-xs">
        <Detail label={t("repoView.server")} value={repo.url} />
        <Detail label={t("repoView.branch")} value={repo.branch} />
        <Detail label={t("repoView.location")} value={repo.root} />
        <div>
          <dt className="mb-1 text-muted-foreground">{t("repoView.checkout")}</dt>
          <dd className="flex flex-wrap gap-1">
            {repo.sparse && repo.sparse.length > 0 ? repo.sparse.map((p) => <Badge key={p}>{p}</Badge>) : <span>{t("repoView.fullCheckout")}</span>}
          </dd>
          <Button size="sm" className="mt-2" onClick={onEditSparse} disabled={busy}>
            <FolderTree />
            {t("repoView.changeFolders")}
          </Button>
        </div>
      </dl>
      <section aria-label={t("repoView.storage")} className="m-5 max-w-xl rounded-lg border border-border bg-card p-4 text-xs">
        <StorageCard root={repo.root} busy={busy} notify={notify} onEditSparse={onEditSparse} />
      </section>
      <section aria-label={t("repoView.ignoreRules")} className="m-5 max-w-xl rounded-lg border border-border bg-card p-4 text-xs">
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
