import { useState, type ReactNode } from "react";
import { useSimple } from "@/lib/mode";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, GitBranchPlus, Loader2, Tag as TagIcon, Undo2 } from "lucide-react";
import { desktop, type Commit, type FileDiff, type Tag } from "@/api/desktop";
import { DiffViewer } from "@/components/changes/DiffViewer";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import type { DiffLayout } from "@/components/changes/TextDiff";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { commitBody, commitTitle, shortId } from "@/lib/history";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useResizableWidth } from "@/hooks/useResizableWidth";

const LAYOUT_KEY = "nipa.diffLayout";

export type CommitActions = {
  onRevert: (commit: Commit) => void;
  onTag: (commit: Commit) => void;
  onBranch: (commit: Commit) => void;
  onSelectCommit: (id: string) => void;
  revertBlocked?: string;
};

type CommitDetailProps = CommitActions & { root: string; commit: Commit; tags: Tag[]; focusPath: string; busy: boolean };

export function CommitDetail({ root, commit, tags, focusPath, busy, revertBlocked, onRevert, onTag, onBranch, onSelectCommit }: CommitDetailProps) {
  const filesSize = useResizableWidth("commit-files", { initial: 256, min: 160, max: 560 });
  const files = useQuery({
    queryKey: ["commit", root, commit.id, "files"],
    queryFn: () => desktop.commitFiles(root, commit.id, commit.parents[0] ?? ""),
    staleTime: Infinity,
  });
  const [picked, setPicked] = useState<{ commit: string; path: string } | null>(null);
  const [layout, setLayoutState] = useState<DiffLayout>(() => (readSetting(LAYOUT_KEY) === "split" ? "split" : "unified"));
  const setLayout = (next: DiffLayout) => {
    setLayoutState(next);
    writeSetting(LAYOUT_KEY, next);
  };

  const list = files.data ?? [];
  const wanted = picked?.commit === commit.id ? picked.path : focusPath;
  const selected: FileDiff | undefined = list.find((f) => f.path === wanted || f.oldPath === wanted) ?? list[0];
  const body = commitBody(commit.message);
  const simple = useSimple();
  const merge = commit.parents.length > 1;
  const blocked = revertBlocked ?? (merge ? "Reverting a merge commit is only available from the command line (nipa revert --mainline)" : undefined);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="shrink-0 space-y-2 border-b border-border px-4 py-3">
        <div className="flex items-start gap-2">
          <h2 className="min-w-0 flex-1 select-text break-words text-sm font-semibold">{commitTitle(commit.message)}</h2>
          {!simple && (
            <>
              <Button size="sm" onClick={() => onBranch(commit)} disabled={busy} title="Create a branch starting at this commit">
                <GitBranchPlus />
                Branch…
              </Button>
              <Button size="sm" onClick={() => onTag(commit)} disabled={busy} title="Tag this commit as a release or milestone">
                <TagIcon />
                Tag…
              </Button>
              <Button size="sm" onClick={() => onRevert(commit)} disabled={busy || !!blocked} title={blocked ?? "Undo this commit with a new commit"}>
                <Undo2 />
                Revert…
              </Button>
            </>
          )}
        </div>
        {body && <p className="max-h-24 select-text overflow-auto whitespace-pre-wrap text-[12px] text-muted-foreground">{body}</p>}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          <span className="font-medium text-foreground" title={commit.email}>
            {commit.author || commit.email || "Unknown author"}
          </span>
          <span title={commit.date}>{timeAgo(commit.date)}</span>
          <span className="select-text font-mono" title={commit.hash}>
            {shortId(commit.id)}
          </span>
          {commit.parents.map((p, i) => (
            <button key={p} type="button" className="font-mono hover:text-foreground hover:underline" onClick={() => onSelectCommit(p)} title={i === 0 ? "First parent" : "Merged parent"}>
              {i === 0 ? "parent" : "merged"} {shortId(p)}
            </button>
          ))}
          {tags.map((t) => (
            <Badge key={t.name} title={t.message || undefined}>
              <TagIcon />
              {t.name}
            </Badge>
          ))}
        </div>
      </header>
      {files.isPending ? (
        <Centered>
          <Loader2 className="size-4 animate-spin" /> Loading changed files…
        </Centered>
      ) : files.isError ? (
        <Centered>
          <AlertTriangle className="size-4 text-danger" /> {errorMessage(files.error)}
        </Centered>
      ) : list.length === 0 ? (
        <Centered>This commit changed no files you can see.</Centered>
      ) : (
        <div className="flex min-h-0 flex-1">
          <div className="relative shrink-0" ref={filesSize.ref} style={{ width: filesSize.width }}>
            <ul className="h-full overflow-auto border-r border-border py-1" aria-label="Changed files">
              {list.map((f) => (
                <li key={f.path}>
                  <button
                    type="button"
                    aria-current={f === selected}
                    onClick={() => setPicked({ commit: commit.id, path: f.path })}
                    title={f.path}
                    className={cn("flex w-full items-center gap-1.5 px-3 py-1 text-left text-[12px] hover:bg-muted", f === selected && "bg-muted font-medium")}
                  >
                    <FileIcon kind={f.kind} />
                    <span className="min-w-0 flex-1 truncate">{f.path}</span>
                    <StatusLetter status={f.status} />
                  </button>
                </li>
              ))}
            </ul>
            <PaneResizer size={filesSize} label="Resize file list" />
          </div>
          <div className="min-w-0 flex-1">{selected && <DiffViewer root={root} diff={selected} layout={layout} onLayout={setLayout} />}</div>
        </div>
      )}
    </div>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex flex-1 items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
