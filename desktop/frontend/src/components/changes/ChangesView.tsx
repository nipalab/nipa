import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, FileDiff as FileDiffIcon, Loader2, Tag } from "lucide-react";
import { desktop, type ChangeSet, type FileChange } from "@/api/desktop";
import { CommitBox } from "@/components/changes/CommitBox";
import { DiffViewer } from "@/components/changes/DiffViewer";
import { FileList, type Selection } from "@/components/changes/FileList";
import type { DiffLayout } from "@/components/changes/TextDiff";
import { ConfirmDialog } from "@/components/ui/confirm";
import type { OpState } from "@/hooks/useRepoOperation";
import { errorMessage, readSetting, writeSetting } from "@/lib/utils";

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

export function ChangesView({ root, op, message, onMessage, onPush, onCancelOp, onError }: ChangesViewProps) {
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
    },
    onError: (err: unknown) => onError(errorMessage(err)),
  };
  const stage = useMutation({ mutationFn: (paths: string[]) => desktop.stage(root, paths), ...applyChanges });
  const unstage = useMutation({ mutationFn: (paths: string[]) => desktop.unstage(root, paths), ...applyChanges });
  const discard = useMutation({ mutationFn: (paths: string[]) => desktop.discard(root, paths), ...applyChanges });

  const data = changes.data;
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

  return (
    <div className="flex h-full min-h-0">
      <div className="flex w-80 shrink-0 flex-col border-r border-border">
        {c.detached && (
          <Banner icon={<Tag className="size-3.5 text-accent" />} text={`Viewing tag ${c.detached}`} />
        )}
        {c.conflicts.length > 0 && (
          <Banner
            icon={<AlertTriangle className="size-3.5 text-warning" />}
            text={`${c.conflicts.length} conflicted file${c.conflicts.length === 1 ? "" : "s"}: resolve, stage, then push.`}
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
          />
        )}
        <CommitBox
          stagedCount={c.staged.length}
          blockedReason={blocked}
          op={op?.op === "push" ? op : null}
          busy={busy}
          message={message}
          onMessage={onMessage}
          onPush={onPush}
          onCancel={onCancelOp}
        />
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
