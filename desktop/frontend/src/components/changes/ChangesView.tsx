import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, ChevronDown, ChevronRight, FileDiff as FileDiffIcon, Loader2, Tag } from "lucide-react";
import { desktop, type ChangeSet, type FileChange, type FileLockState, type MetaWarning } from "@/api/desktop";
import { CommitBox, type LockIssue } from "@/components/changes/CommitBox";
import { DiffViewer } from "@/components/changes/DiffViewer";
import { FileList, type Selection } from "@/components/changes/FileList";
import type { DiffLayout } from "@/components/changes/TextDiff";
import { ConfirmDialog } from "@/components/ui/confirm";
import { lockStateMap, useLockActions, useLockStatus, type Notify } from "@/hooks/useLocks";
import type { OpState } from "@/hooks/useRepoOperation";
import { errorMessage, readSetting, writeSetting } from "@/lib/utils";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useResizableWidth } from "@/hooks/useResizableWidth";

const LAYOUT_KEY = "nipa.diffLayout";
const POLL_MS = 30_000;

type ChangesViewProps = {
  root: string;
  op: OpState | null;
  message: string;
  onMessage: (message: string) => void;
  onPush: (message: string) => void;
  onCancelOp: () => void;
  onError: (message: string) => void;
  notify: Notify;
};

function firstFile(c: ChangeSet): Selection | null {
  if (c.staged.length > 0) return { path: c.staged[0].path, staged: true };
  if (c.unstaged.length > 0) return { path: c.unstaged[0].path, staged: false };
  return null;
}

function stillListed(c: ChangeSet, sel: Selection | null): Selection | null {
  if (!sel) return null;
  const list = sel.staged ? c.staged : c.unstaged;
  if (list.some((f) => f.path === sel.path)) return sel;
  const other = sel.staged ? c.unstaged : c.staged;
  return other.some((f) => f.path === sel.path) ? { path: sel.path, staged: !sel.staged } : null;
}

export function ChangesView({ root, op, message, onMessage, onPush, onCancelOp, onError, notify }: ChangesViewProps) {
  const listSize = useResizableWidth("changes", { initial: 320, min: 220, max: 640 });
  const queryClient = useQueryClient();
  const [picked, setPicked] = useState<Selection | null>(null);
  const [discarding, setDiscarding] = useState<FileChange | null>(null);
  const [layout, setLayoutState] = useState<DiffLayout>(() => (readSetting(LAYOUT_KEY) === "split" ? "split" : "unified"));

  const changes = useQuery({ queryKey: ["changes", root], queryFn: () => desktop.changes(root), refetchInterval: POLL_MS });

  useEffect(
    () =>
      desktop.onRepoChanged((change) => {
        if (change.root !== root) return;
        void queryClient.invalidateQueries({ queryKey: ["changes", root] });
        void queryClient.invalidateQueries({ queryKey: ["locks", root] });
        const paths = change.paths ?? [];
        if (paths.length === 0) void queryClient.invalidateQueries({ queryKey: ["diff", root] });
        for (const path of paths) void queryClient.invalidateQueries({ queryKey: ["diff", root, path] });
      }),
    [root, queryClient],
  );

  const applyChanges = {
    onSuccess: (next: ChangeSet) => {
      queryClient.setQueryData(["changes", root], next);
      void queryClient.invalidateQueries({ queryKey: ["diff", root] });
      void queryClient.invalidateQueries({ queryKey: ["locks", root] });
    },
    onError: (err: unknown) => onError(errorMessage(err)),
  };
  const stage = useMutation({ mutationFn: (paths: string[]) => desktop.stage(root, paths), ...applyChanges });
  const unstage = useMutation({ mutationFn: (paths: string[]) => desktop.unstage(root, paths), ...applyChanges });
  const discard = useMutation({ mutationFn: (paths: string[]) => desktop.discard(root, paths), ...applyChanges });

  const data = changes.data;
  const meta = useQuery({
    queryKey: ["meta", root, changes.dataUpdatedAt],
    queryFn: () => desktop.metaWarnings(root),
    enabled: !!data && data.staged.length + data.unstaged.length > 0,
    placeholderData: (prev) => prev,
  });
  const warnings = useMemo(() => {
    const map = new Map<string, string>();
    for (const w of meta.data ?? []) {
      for (const p of [w.path, w.related]) if (!map.has(p)) map.set(p, `${w.path}: ${w.message}`);
    }
    return map;
  }, [meta.data]);
  const lockStatus = useLockStatus(root);
  const lockStates = useMemo(() => lockStateMap(lockStatus.data), [lockStatus.data]);
  const { lock } = useLockActions(root, notify);
  const lockBranch = lockStatus.data?.branch ?? "";
  const lockPaths = (paths: string[]) => lock.mutate({ branch: lockBranch, paths });
  const selected = data ? (stillListed(data, picked) ?? firstFile(data)) : null;

  const diff = useQuery({
    queryKey: ["diff", root, selected?.path],
    queryFn: () => desktop.fileDiff(root, selected!.path),
    enabled: !!selected,
  });

  const setLayout = (next: DiffLayout) => {
    writeSetting(LAYOUT_KEY, next);
    setLayoutState(next);
  };

  if (changes.isPending) return <Centered icon={<Loader2 className="size-5 animate-spin text-muted-foreground" />} title="Reading working copy…" />;
  if (changes.isError) {
    return <Centered icon={<AlertTriangle className="size-5 text-danger" />} title="Could not read changes" detail={errorMessage(changes.error)} />;
  }

  const c = changes.data;
  const busy = stage.isPending || unstage.isPending || discard.isPending || !!op;
  const empty = c.staged.length === 0 && c.unstaged.length === 0;
  const blocked = c.detached ? `HEAD is at tag ${c.detached}. Switch to a branch to push.` : undefined;
  const openConflicts = c.conflicts.filter((p) => !c.resolved.includes(p)).length;
  const lockIssue = stagedLockIssue(c, lockStates, lockPaths, lock.isPending);

  return (
    <div className="flex h-full min-h-0">
      <div className="relative flex shrink-0 flex-col border-r border-border" style={{ width: listSize.width }}>
        {c.detached && (
          <Banner icon={<Tag className="size-3.5 text-accent" />} text={`Viewing tag ${c.detached}`} />
        )}
        {openConflicts > 0 && (
          <Banner
            icon={<AlertTriangle className="size-3.5 text-warning" />}
            text={`${openConflicts} conflicted file${openConflicts === 1 ? "" : "s"}: resolve them in the Conflicts tab.`}
          />
        )}
        {!empty && (meta.data ?? []).length > 0 && (
          <MetaWarnings
            warnings={meta.data!}
            onSelect={(w) => {
              for (const path of [w.path, w.related]) {
                const staged = c.staged.some((f) => f.path === path);
                if (staged || c.unstaged.some((f) => f.path === path)) return setPicked({ path, staged });
              }
            }}
          />
        )}
        {empty ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
            <CheckCircle2 className="size-6 text-success" />
            <p className="text-sm font-medium">No local changes</p>
            <p className="text-xs text-muted-foreground">Edits you make in this clone show up here as you save them.</p>
          </div>
        ) : (
          <FileList
            changes={c}
            selected={selected}
            busy={busy}
            onSelect={setPicked}
            onStage={(paths) => stage.mutate(paths)}
            onUnstage={(paths) => unstage.mutate(paths)}
            onDiscard={setDiscarding}
            warnings={warnings}
            locks={lockStates}
            onLock={lockStatus.data ? (path) => lockPaths([path]) : undefined}
          />
        )}
        <CommitBox
          stagedCount={c.staged.length}
          blockedReason={blocked}
          lockIssue={blocked ? undefined : lockIssue}
          op={op?.op === "push" ? op : null}
          busy={busy}
          message={message}
          onMessage={onMessage}
          onPush={onPush}
          onCancel={onCancelOp}
        />
        <PaneResizer size={listSize} label="Resize file list" />
      </div>

      <div className="min-w-0 flex-1">
        {!selected ? (
          <Centered icon={<FileDiffIcon className="size-6 text-muted-foreground" />} title="Nothing to compare" detail="Select a changed file to see what changed." />
        ) : diff.isPending ? (
          <Centered icon={<Loader2 className="size-5 animate-spin text-muted-foreground" />} title="Loading diff…" />
        ) : diff.isError ? (
          <Centered icon={<AlertTriangle className="size-5 text-danger" />} title="Could not load the diff" detail={errorMessage(diff.error)} />
        ) : diff.data ? (
          <DiffViewer root={root} diff={diff.data} layout={layout} onLayout={setLayout} />
        ) : (
          <Centered icon={<CheckCircle2 className="size-6 text-success" />} title="No changes" detail="This file matches the last synced version." />
        )}
      </div>

      {discarding && (
        <ConfirmDialog
          title="Discard changes?"
          message={`${discarding.path} goes back to the last synced version. Your edits are lost.`}
          confirmLabel="Discard"
          onCancel={() => setDiscarding(null)}
          onConfirm={() => {
            discard.mutate([discarding.path]);
            setDiscarding(null);
          }}
        />
      )}
    </div>
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

function stagedLockIssue(c: ChangeSet, states: Map<string, FileLockState>, lockPaths: (paths: string[]) => void, busy: boolean): LockIssue | undefined {
  const staged = c.staged.flatMap((f) => states.get(f.path) ?? []);
  const theirs = staged.filter((s) => s.state === "theirs");
  if (theirs.length > 0) {
    const [first] = theirs;
    const more = theirs.length > 1 ? ` (+${theirs.length - 1} more)` : "";
    return { message: `${first.path} is locked by ${first.heldByName || "someone else"}${more}. Unstage it or ask them to release the lock.` };
  }
  const needed = staged.filter((s) => s.state === "needed");
  if (needed.length === 0) return undefined;
  return {
    message: `${plural(needed.length, "staged file")} ${needed.length === 1 ? "needs" : "need"} a lock before you can push.`,
    action: { label: `Lock ${plural(needed.length, "file")}`, onClick: () => lockPaths(needed.map((s) => s.path)), busy },
  };
}

const metaKinds: Record<string, string> = {
  "missing-meta": "Missing .meta",
  "orphan-meta": "Orphan .meta",
  "meta-not-staged": ".meta not staged",
  "guid-changed": "GUID changed",
};

function MetaWarnings({ warnings, onSelect }: { warnings: MetaWarning[]; onSelect: (warning: MetaWarning) => void }) {
  const [open, setOpen] = useState(warnings.length <= 3);
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div className="border-b border-border bg-muted text-[11px]">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center gap-2 px-3 py-2 text-left">
        <AlertTriangle className="size-3.5 shrink-0 text-warning" />
        <span className="flex-1">
          {warnings.length} Unity .meta warning{warnings.length === 1 ? "" : "s"}
        </span>
        <Chevron className="size-3.5 text-muted-foreground" />
      </button>
      {open && (
        <ul className="max-h-40 overflow-y-auto px-3 pb-2" aria-label="Unity .meta warnings">
          {warnings.map((w) => (
            <li key={`${w.kind}:${w.path}`}>
              <button type="button" onClick={() => onSelect(w)} className="w-full rounded px-1 py-0.5 text-left hover:bg-background" title={w.path}>
                <span className="font-medium text-warning">{metaKinds[w.kind] ?? w.kind}</span> <span className="font-mono">{w.path}</span>
                <span className="block text-muted-foreground">{w.message}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Banner({ icon, text }: { icon: ReactNode; text: string }) {
  return (
    <div className="flex items-center gap-2 border-b border-border bg-muted px-3 py-2 text-[11px]">
      {icon}
      <span>{text}</span>
    </div>
  );
}

function Centered({ icon, title, detail }: { icon: ReactNode; title: string; detail?: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
      {icon}
      <p className="text-sm font-medium">{title}</p>
      {detail && <p className="max-w-sm select-text text-xs text-muted-foreground">{detail}</p>}
    </div>
  );
}
