import { useMemo, useRef, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ExternalLink, MessageSquarePlus } from "lucide-react";
import type { FileDiff } from "@/api/desktop";
import type { BlameCell, BlameCells } from "@/lib/blame";
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

// LineTools are the per-line extras of a text viewer: the blame gutter on
// the new side and opening the working file at a line.
export type LineTools = { blame?: BlameCells; onSelectCommit?: (id: string) => void; onOpenLine?: (line: number) => void };

function BlameGutter({ cell, onSelect }: { cell?: BlameCell; onSelect?: (id: string) => void }) {
  const base = "w-44 shrink-0 select-none truncate border-r border-border px-2 text-left font-sans text-[11px] leading-5";
  if (!cell) return <span className={base} aria-hidden />;
  const tone = cell.uncommitted ? "text-warning" : "text-muted-foreground";
  const commit = cell.commit;
  if (commit && onSelect) {
    return (
      <button type="button" title={cell.title} aria-label={`Show the commit that last changed this line: ${cell.title.split("\n", 1)[0]}`} onClick={() => onSelect(commit.id)} className={cn(base, tone, "hover:bg-muted hover:text-foreground")}>
        {cell.label}
      </button>
    );
  }
  return (
    <span title={cell.title} className={cn(base, tone)}>
      {cell.label}
    </span>
  );
}

function OpenLine({ line, tools }: { line: number; tools?: LineTools }) {
  const open = tools?.onOpenLine;
  if (!open || !line) return null;
  return (
    <button
      type="button"
      aria-label={`Open in editor at line ${line}`}
      title="Open in editor at this line"
      onClick={() => open(line)}
      className="absolute right-1 top-0.5 hidden rounded bg-muted p-0.5 text-muted-foreground hover:text-foreground group-hover:block focus:block"
    >
      <ExternalLink className="size-3" />
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

function UnifiedLine({ row, language, comments, tools }: { row: Extract<UnifiedRow, { type: "line" }>; language?: string; comments?: LineComments; tools?: LineTools }) {
  const sign = row.kind === "added" ? "+" : row.kind === "removed" ? "−" : " ";
  const anchor = anchorOf(row.kind, row.old, row.new);
  return (
    <>
      <div className={cn("group relative flex min-h-full", lineTone[row.kind])}>
        {tools?.blame && <BlameGutter cell={row.new ? tools.blame.get(row.new) : undefined} onSelect={tools.onSelectCommit} />}
        <AddComment anchor={anchor} comments={comments} />
        <OpenLine line={row.new} tools={tools} />
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

function SplitHalf({ cell, language, side, comments, tools }: { cell: SplitCell; language?: string; side: "old" | "new"; comments?: LineComments; tools?: LineTools }) {
  const blame = side === "new" ? tools?.blame : undefined;
  if (!cell) return <div className={cn("flex w-1/2 min-w-0 bg-muted/50", side === "old" && "border-r border-border")}>{blame && <BlameGutter />}</div>;
  const anchor = splitAnchor(cell, side);
  return (
    <div className={cn("group relative flex w-1/2 min-w-0", lineTone[cell.kind], side === "old" && "border-r border-border")}>
      {blame && <BlameGutter cell={blame.get(cell.line)} onSelect={tools?.onSelectCommit} />}
      {anchor && <AddComment anchor={anchor} comments={comments} />}
      {side === "new" && <OpenLine line={cell.line} tools={tools} />}
      <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[cell.kind])}>{cell.line || ""}</span>
      <Code text={cell.text} language={language} noNewline={cell.noNewline} />
    </div>
  );
}

function SplitLine({ row, language, comments, tools }: { row: Extract<SplitRow, { type: "pair" }>; language?: string; comments?: LineComments; tools?: LineTools }) {
  const left = splitAnchor(row.left, "old");
  const right = splitAnchor(row.right, "new");
  return (
    <>
      <div className="flex min-h-full">
        <SplitHalf cell={row.left} language={language} side="old" comments={comments} />
        <SplitHalf cell={row.right} language={language} side="new" comments={comments} tools={tools} />
      </div>
      {left && comments?.render(left)}
      {right && comments?.render(right)}
    </>
  );
}

export function TextDiff({ diff, layout, comments, tools }: { diff: FileDiff; layout: DiffLayout; comments?: LineComments; tools?: LineTools }) {
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
                <UnifiedLine row={row} language={language} comments={comments} tools={tools} />
              ) : (
                <SplitLine row={row} language={language} comments={comments} tools={tools} />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
