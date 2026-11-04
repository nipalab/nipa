import { useMemo, useRef, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { MessageSquarePlus } from "lucide-react";
import type { FileDiff } from "@/api/desktop";
import { splitRows, unifiedRows, type SplitCell, type SplitRow, type UnifiedRow } from "@/lib/diff";
import { highlightLine, languageFor } from "@/lib/highlight";
import { cn } from "@/lib/utils";

export type DiffLayout = "unified" | "split";

// LineAnchor names a diff line the way the server anchors review comments:
// removed lines by their old number, every other line by its new number.
export type LineAnchor = { oldLine: number; newLine: number };

// LineComments lets a caller show threads under diff lines and start new
// ones; render returns null for lines without anything to show.
export type LineComments = { render: (anchor: LineAnchor) => ReactNode; onAdd?: (anchor: LineAnchor) => void };

const anchorOf = (kind: string, oldLine: number, newLine: number): LineAnchor => (kind === "removed" ? { oldLine, newLine: 0 } : { oldLine: 0, newLine });

function AddComment({ anchor, comments }: { anchor: LineAnchor; comments?: LineComments }) {
  if (!comments?.onAdd) return null;
  return (
    <button
      type="button"
      aria-label={anchor.newLine ? `Comment on line ${anchor.newLine}` : `Comment on removed line ${anchor.oldLine}`}
      title="Comment on this line"
      onClick={() => comments.onAdd?.(anchor)}
      className="absolute left-0.5 top-0.5 hidden rounded bg-accent p-0.5 text-white group-hover:block focus:block"
    >
      <MessageSquarePlus className="size-3" />
    </button>
  );
}

const ROW_HEIGHT = 20;

const lineTone = {
  added: "bg-success/12",
  removed: "bg-danger/12",
  context: "",
};

const gutterTone = {
  added: "bg-success/20 text-success",
  removed: "bg-danger/20 text-danger",
  context: "text-muted-foreground/70",
};

function Code({ text, language, noNewline }: { text: string; language?: string; noNewline: boolean }) {
  return (
    <span className="min-w-0 flex-1 whitespace-pre-wrap break-all px-2">
      <span dangerouslySetInnerHTML={{ __html: highlightLine(text, language) }} />
      {noNewline && (
        <span className="ml-2 select-none text-[10px] text-muted-foreground" title="No newline at end of file">
          ⏎̸
        </span>
      )}
    </span>
  );
}

function HunkRow({ header }: { header: string }) {
  return <div className="flex h-full items-center bg-accent-soft/60 px-3 font-mono text-[11px] text-muted-foreground">{header}</div>;
}

function UnifiedLine({ row, language, comments }: { row: Extract<UnifiedRow, { type: "line" }>; language?: string; comments?: LineComments }) {
  const sign = row.kind === "added" ? "+" : row.kind === "removed" ? "−" : " ";
  const anchor = anchorOf(row.kind, row.old, row.new);
  return (
    <>
      <div className={cn("group relative flex min-h-full", lineTone[row.kind])}>
        <AddComment anchor={anchor} comments={comments} />
        <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[row.kind])}>{row.old || ""}</span>
        <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[row.kind])}>{row.new || ""}</span>
        <span className={cn("w-4 shrink-0 select-none text-center", gutterTone[row.kind])}>{sign}</span>
        <Code text={row.text} language={language} noNewline={row.noNewline} />
      </div>
      {comments?.render(anchor)}
    </>
  );
}

const splitAnchor = (cell: SplitCell, side: "old" | "new"): LineAnchor | null => {
  if (!cell) return null;
  if (side === "old") return cell.kind === "removed" ? { oldLine: cell.line, newLine: 0 } : null;
  return { oldLine: 0, newLine: cell.line };
};

function SplitHalf({ cell, language, side, comments }: { cell: SplitCell; language?: string; side: "old" | "new"; comments?: LineComments }) {
  if (!cell) return <div className={cn("flex w-1/2 min-w-0 bg-muted/50", side === "old" && "border-r border-border")} />;
  const anchor = splitAnchor(cell, side);
  return (
    <div className={cn("group relative flex w-1/2 min-w-0", lineTone[cell.kind], side === "old" && "border-r border-border")}>
      {anchor && <AddComment anchor={anchor} comments={comments} />}
      <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[cell.kind])}>{cell.line || ""}</span>
      <Code text={cell.text} language={language} noNewline={cell.noNewline} />
    </div>
  );
}

function SplitLine({ row, language, comments }: { row: Extract<SplitRow, { type: "pair" }>; language?: string; comments?: LineComments }) {
  const left = splitAnchor(row.left, "old");
  const right = splitAnchor(row.right, "new");
  return (
    <>
      <div className="flex min-h-full">
        <SplitHalf cell={row.left} language={language} side="old" comments={comments} />
        <SplitHalf cell={row.right} language={language} side="new" comments={comments} />
      </div>
      {left && comments?.render(left)}
      {right && comments?.render(right)}
    </>
  );
}

export function TextDiff({ diff, layout, comments }: { diff: FileDiff; layout: DiffLayout; comments?: LineComments }) {
  const hunks = diff.hunks ?? [];
  const rows = useMemo(() => (layout === "split" ? splitRows(hunks) : unifiedRows(hunks)), [hunks, layout]);
  const language = languageFor(diff.path);
  const parent = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 30,
    initialRect: { width: 800, height: 800 },
  });

  return (
    <div ref={parent} className="hljs-scope h-full overflow-y-auto select-text font-mono text-[12px] leading-5" role="table" aria-label={`Diff of ${diff.path}`}>
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index];
          return (
            <div
              key={item.key}
              ref={virtualizer.measureElement}
              data-index={item.index}
              role="row"
              style={{ position: "absolute", top: 0, left: 0, right: 0, minHeight: ROW_HEIGHT, transform: `translateY(${item.start}px)` }}
            >
              {row.type === "hunk" ? (
                <HunkRow header={row.header} />
              ) : row.type === "line" ? (
                <UnifiedLine row={row} language={language} comments={comments} />
              ) : (
                <SplitLine row={row} language={language} comments={comments} />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
