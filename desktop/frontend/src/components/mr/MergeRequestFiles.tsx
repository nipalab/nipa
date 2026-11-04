import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Loader2, MessageSquare } from "lucide-react";
import { desktop, type CommentInput, type FileDiff, type Thread } from "@/api/desktop";
import { DiffViewer } from "@/components/changes/DiffViewer";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import type { DiffLayout, LineAnchor, LineComments } from "@/components/changes/TextDiff";
import { CommentBox, ThreadCard } from "@/components/mr/ThreadCard";
import { fileThreads, threadAt } from "@/lib/mergeRequests";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";

const LAYOUT_KEY = "nipa.diffLayout";

type MergeRequestFilesProps = {
  root: string;
  base: string;
  head: string;
  threads: Thread[];
  busy: boolean;
  canComment: boolean;
  onComment: (input: Partial<CommentInput> & { body: string }) => Promise<unknown>;
  onReply: (threadId: string, body: string) => Promise<unknown>;
  onResolve: (threadId: string, resolved: boolean) => void;
};

export function MergeRequestFiles({ root, base, head, threads, busy, canComment, onComment, onReply, onResolve }: MergeRequestFilesProps) {
  const files = useQuery({ queryKey: ["commit", root, head, "vs", base], queryFn: () => desktop.revisionFiles(root, base, head), enabled: !!base && !!head, staleTime: Infinity });
  const [picked, setPicked] = useState("");
  const [drafting, setDrafting] = useState<LineAnchor | null>(null);
  const [layout, setLayoutState] = useState<DiffLayout>(() => (readSetting(LAYOUT_KEY) === "split" ? "split" : "unified"));
  const setLayout = (next: DiffLayout) => {
    setLayoutState(next);
    writeSetting(LAYOUT_KEY, next);
  };

  const list = files.data ?? [];
  const selected: FileDiff | undefined = list.find((f) => f.path === picked) ?? list[0];
  const counts = useMemo(() => {
    const out = new Map<string, number>();
    for (const t of threads) if (t.path) out.set(t.path, (out.get(t.path) ?? 0) + 1);
    return out;
  }, [threads]);

  if (!base || !head) return <Centered>The branches of this request are gone, so its changes cannot be shown.</Centered>;
  if (files.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> Loading changes…
      </Centered>
    );
  }
  if (files.isError) {
    return (
      <Centered>
        <AlertTriangle className="size-4 text-danger" /> {errorMessage(files.error)}
      </Centered>
    );
  }
  if (list.length === 0) return <Centered>No file changes.</Centered>;

  const { inline, outdated } = selected ? fileThreads(threads, selected.path) : { inline: [], outdated: [] };
  const comments: LineComments | undefined = selected && {
    render: (anchor) => {
      const here = inline.filter((t) => threadAt(t, anchor));
      const writing = drafting && drafting.oldLine === anchor.oldLine && drafting.newLine === anchor.newLine;
      if (here.length === 0 && !writing) return null;
      return (
        <div className="space-y-1 border-y border-border bg-muted/40 py-1">
          {here.map((t) => (
            <ThreadCard key={t.id} thread={t} busy={busy} compact onReply={(body) => onReply(t.id, body)} onResolve={(r) => onResolve(t.id, r)} />
          ))}
          {writing && (
            <div className="mx-2 my-1 rounded-md border border-border bg-card p-2">
              <CommentBox
                label={`Comment on line ${anchor.newLine || anchor.oldLine}`}
                placeholder="Leave a comment on this line"
                busy={busy}
                autoFocus
                onCancel={() => setDrafting(null)}
                onSubmit={async (body) => {
                  await onComment({ path: selected.path, oldLine: anchor.oldLine, newLine: anchor.newLine, body });
                  setDrafting(null);
                }}
              />
            </div>
          )}
        </div>
      );
    },
    onAdd: canComment ? setDrafting : undefined,
  };

  return (
    <div className="flex h-full min-h-0">
      <ul className="w-64 shrink-0 overflow-auto border-r border-border py-1" aria-label="Changed files">
        {list.map((f) => (
          <li key={f.path}>
            <button
              type="button"
              aria-current={f === selected}
              onClick={() => {
                setPicked(f.path);
                setDrafting(null);
              }}
              title={f.path}
              className={cn("flex w-full items-center gap-1.5 px-3 py-1 text-left text-[12px] hover:bg-muted", f === selected && "bg-muted font-medium")}
            >
              <FileIcon kind={f.kind} />
              <span className="min-w-0 flex-1 truncate">{f.path}</span>
              {counts.get(f.path) ? (
                <span className="flex items-center gap-0.5 text-[10px] text-muted-foreground" aria-label={`${counts.get(f.path)} threads`}>
                  <MessageSquare className="size-3" />
                  {counts.get(f.path)}
                </span>
              ) : null}
              <StatusLetter status={f.status} />
            </button>
          </li>
        ))}
      </ul>
      {selected && (
        <div className="flex min-w-0 flex-1 flex-col">
          {outdated.length > 0 && (
            <details className="shrink-0 border-b border-border px-4 py-2 text-[12px]">
              <summary className="cursor-pointer text-muted-foreground">
                {outdated.length} outdated thread{outdated.length === 1 ? "" : "s"} on lines that changed since
              </summary>
              <div className="mt-2 max-h-60 space-y-2 overflow-auto">
                {outdated.map((t) => (
                  <ThreadCard key={t.id} thread={t} busy={busy} onReply={(body) => onReply(t.id, body)} onResolve={(r) => onResolve(t.id, r)} />
                ))}
              </div>
            </details>
          )}
          {selected.old?.binary || selected.new?.binary ? (
            <p className="shrink-0 border-b border-border px-4 py-1.5 text-[11px] text-muted-foreground">Binary files take comments in the conversation, not on lines.</p>
          ) : null}
          <div className="min-h-0 flex-1">
            <DiffViewer root={root} diff={selected} layout={layout} onLayout={setLayout} comments={comments} />
          </div>
        </div>
      )}
    </div>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
