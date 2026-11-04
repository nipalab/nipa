import { useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownUp, FileText, Loader2, Table2 } from "lucide-react";
import { desktop, type FileDiff, type TableColumn, type TableCompare, type TableRow } from "@/api/desktop";
import { imageRefs } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { TextDiff, type DiffLayout } from "@/components/changes/TextDiff";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { cn } from "@/lib/utils";

type Mode = "table" | "text";

export function TableDiff({ root, diff, layout }: { root: string; diff: FileDiff; layout: DiffLayout }) {
  const hasText = (diff.hunks ?? []).length > 0;
  const [mode, setMode] = useState<Mode>("table");
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: ["diff", root, diff.path, "table"],
    queryFn: () => desktop.tableDiff(root, old, next),
    staleTime: 30_000,
    enabled: mode === "table",
  });
  const switcher = hasText && (
    <Group label="Table view">
      <ToolButton active={mode === "table"} label="Table" onClick={() => setMode("table")}>
        <Table2 className="size-3.5" />
      </ToolButton>
      <ToolButton active={mode === "text"} label="Text" onClick={() => setMode("text")}>
        <FileText className="size-3.5" />
      </ToolButton>
    </Group>
  );
  if (mode === "text") {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex shrink-0 items-center border-b border-border px-3 py-1.5">{switcher}</div>
        <div className="min-h-0 flex-1">
          <TextDiff diff={diff} layout={layout} />
        </div>
      </div>
    );
  }
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Comparing table…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The table could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data) return <MetaDiff diff={diff} detail="There is no table preview for this file." />;
  return <TableCompareView compare={query.data} toolbar={switcher || null} />;
}

const ROW = 26;
const CHANGED_ROW = 42;
const COL_WIDTH = 160;
const GUTTER = 88;

const hasChanges = (c: TableCompare) => c.rowsAdded + c.rowsRemoved + c.rowsChanged + c.rowsMoved + c.columnsAdded + c.columnsRemoved > 0;

export function TableCompareView({ compare, toolbar = null }: { compare: TableCompare; toolbar?: ReactNode }) {
  const columns = compare.columns ?? [];
  const all = compare.rows ?? [];
  const [onlyChanges, setOnlyChanges] = useState(() => hasChanges(compare) && all.length > 50);
  const rows = useMemo(() => (onlyChanges ? all.filter((r) => r.status !== "same" || r.moved) : all), [all, onlyChanges]);
  const parent = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: (i) => (rows[i].status === "changed" ? CHANGED_ROW : ROW),
    overscan: 16,
    initialRect: { width: 800, height: 600 },
  });
  const width = GUTTER + columns.length * COL_WIDTH;
  const key = compare.keyColumn >= 0 ? columns[compare.keyColumn] : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-1.5 text-[11px]">
        {toolbar}
        <Summary compare={compare} />
        {!compare.error && (
          <span className="text-muted-foreground">{key ? `Rows matched by ${columnLabel(key, compare.keyColumn)}` : "Rows matched by content"}</span>
        )}
        <label className="ml-auto flex items-center gap-1.5">
          <input type="checkbox" checked={onlyChanges} onChange={(e) => setOnlyChanges(e.target.checked)} className="size-3.5 accent-[var(--accent)]" />
          Only changed rows
        </label>
      </div>
      {compare.error ? (
        <div className="p-4 text-[11px]">
          <Notice>{`This file cannot be compared as a table: ${compare.error}.`}</Notice>
        </div>
      ) : (
        <div ref={parent} className="min-h-0 flex-1 overflow-auto bg-card text-[12px]" role="table" aria-label="Table changes" aria-rowcount={rows.length + 1}>
          <div style={{ width }} className="sticky top-0 z-10 flex border-b border-border bg-sidebar font-medium" role="row">
            <div className="shrink-0 px-2 py-1 text-[11px] text-muted-foreground" style={{ width: GUTTER }} role="columnheader">
              Row
            </div>
            {columns.map((c, i) => (
              <ColumnHeader key={i} column={c} index={i} />
            ))}
          </div>
          {rows.length === 0 ? (
            <p className="p-4 text-[11px] text-muted-foreground">{onlyChanges ? "No rows changed." : "The table is empty."}</p>
          ) : (
            <div style={{ height: virtualizer.getTotalSize(), width, position: "relative" }}>
              {virtualizer.getVirtualItems().map((item) => (
                <Row key={item.key} row={rows[item.index]} columns={columns} top={item.start} height={item.size} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

const letters = (i: number): string => (i < 26 ? String.fromCharCode(65 + i) : letters(Math.floor(i / 26) - 1) + letters(i % 26));
const columnLabel = (c: TableColumn, i: number) => c.name || `column ${letters(i)}`;

function ColumnHeader({ column, index }: { column: TableColumn; index: number }) {
  const added = column.old < 0;
  const removed = column.new < 0;
  const renamed = !added && !removed && column.oldName !== column.name && column.oldName !== "";
  return (
    <div
      role="columnheader"
      title={renamed ? `${column.oldName} → ${column.name}` : column.name}
      className={cn("shrink-0 truncate border-l border-border px-2 py-1", added && "bg-success/15 text-success", removed && "bg-danger/15 text-danger line-through")}
      style={{ width: COL_WIDTH }}
    >
      {column.name || <span className="text-muted-foreground">{letters(index)}</span>}
      {renamed && <span className="ml-1 font-normal text-warning">(was {column.oldName})</span>}
    </div>
  );
}

const rowTone: Record<string, string> = {
  added: "bg-success/10",
  removed: "bg-danger/10 text-muted-foreground line-through",
  changed: "",
  same: "",
};

function Row({ row, columns, top, height }: { row: TableRow; columns: TableColumn[]; top: number; height: number }) {
  const changed = new Set(row.changed ?? []);
  return (
    <div
      role="row"
      data-status={row.status}
      className={cn("absolute left-0 flex border-b border-border/60", rowTone[row.status])}
      style={{ top, height, width: GUTTER + columns.length * COL_WIDTH }}
    >
      <div className="flex shrink-0 items-center gap-1 px-2 font-mono text-[10px] text-muted-foreground no-underline" style={{ width: GUTTER }} role="rowheader">
        <span>{row.oldLine || "·"}</span>
        <span>→</span>
        <span>{row.newLine || "·"}</span>
        {row.moved && <ArrowDownUp className="size-3 text-accent" aria-label="Moved" />}
      </div>
      {columns.map((c, i) => {
        const value = (row.cells ?? [])[i] ?? "";
        const isChanged = changed.has(i);
        return (
          <div
            key={i}
            role="cell"
            title={isChanged ? `${row.old?.[i] ?? ""} → ${value}` : value}
            className={cn(
              "flex shrink-0 flex-col justify-center overflow-hidden border-l border-border/60 px-2",
              isChanged && "bg-warning/15",
              c.old < 0 && row.status !== "added" && "text-success",
              c.new < 0 && row.status !== "removed" && "text-muted-foreground line-through",
            )}
            style={{ width: COL_WIDTH }}
          >
            {isChanged && <span className="truncate text-[11px] text-danger line-through">{row.old?.[i] ?? ""}</span>}
            <span className="truncate">{value}</span>
          </div>
        );
      })}
    </div>
  );
}

const count = (n: number, word: string) => `${n.toLocaleString()} ${word}${n === 1 ? "" : "s"}`;

function Summary({ compare: c }: { compare: TableCompare }) {
  const parts: { text: string; tone: string }[] = [];
  if (c.rowsAdded) parts.push({ text: `+${count(c.rowsAdded, "row")}`, tone: "text-success" });
  if (c.rowsRemoved) parts.push({ text: `−${count(c.rowsRemoved, "row")}`, tone: "text-danger" });
  if (c.rowsChanged) parts.push({ text: `${c.rowsChanged.toLocaleString()} changed (${count(c.cellsChanged, "cell")})`, tone: "text-warning" });
  if (c.rowsMoved) parts.push({ text: `${c.rowsMoved.toLocaleString()} moved`, tone: "text-accent" });
  if (c.columnsAdded) parts.push({ text: `+${count(c.columnsAdded, "column")}`, tone: "text-success" });
  if (c.columnsRemoved) parts.push({ text: `−${count(c.columnsRemoved, "column")}`, tone: "text-danger" });
  if (parts.length === 0 && !c.error) parts.push({ text: "No cell changes", tone: "text-muted-foreground" });
  return (
    <span className="flex gap-3 font-medium" aria-label="Table summary">
      {parts.map((p) => (
        <span key={p.text} className={p.tone}>
          {p.text}
        </span>
      ))}
    </span>
  );
}
