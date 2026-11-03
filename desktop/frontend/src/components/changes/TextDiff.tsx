import { useMemo, useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { FileDiff } from "@/api/desktop";
import { splitRows, unifiedRows, type SplitCell, type SplitRow, type UnifiedRow } from "@/lib/diff";
import { highlightLine, languageFor } from "@/lib/highlight";
import { cn } from "@/lib/utils";

export type DiffLayout = "unified" | "split";

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

function UnifiedLine({ row, language }: { row: Extract<UnifiedRow, { type: "line" }>; language?: string }) {
  const sign = row.kind === "added" ? "+" : row.kind === "removed" ? "−" : " ";
  return (
    <div className={cn("flex min-h-full", lineTone[row.kind])}>
      <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[row.kind])}>{row.old || ""}</span>
      <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[row.kind])}>{row.new || ""}</span>
      <span className={cn("w-4 shrink-0 select-none text-center", gutterTone[row.kind])}>{sign}</span>
      <Code text={row.text} language={language} noNewline={row.noNewline} />
    </div>
  );
}

function SplitHalf({ cell, language, side }: { cell: SplitCell; language?: string; side: "old" | "new" }) {
  if (!cell) return <div className={cn("flex w-1/2 min-w-0 bg-muted/50", side === "old" && "border-r border-border")} />;
  return (
    <div className={cn("flex w-1/2 min-w-0", lineTone[cell.kind], side === "old" && "border-r border-border")}>
      <span className={cn("w-12 shrink-0 select-none pr-2 text-right", gutterTone[cell.kind])}>{cell.line || ""}</span>
      <Code text={cell.text} language={language} noNewline={cell.noNewline} />
    </div>
  );
}

function SplitLine({ row, language }: { row: Extract<SplitRow, { type: "pair" }>; language?: string }) {
  return (
    <div className="flex min-h-full">
      <SplitHalf cell={row.left} language={language} side="old" />
      <SplitHalf cell={row.right} language={language} side="new" />
    </div>
  );
}

export function TextDiff({ diff, layout }: { diff: FileDiff; layout: DiffLayout }) {
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
                <UnifiedLine row={row} language={language} />
              ) : (
                <SplitLine row={row} language={language} />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
