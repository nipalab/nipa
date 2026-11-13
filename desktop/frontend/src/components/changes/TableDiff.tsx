import { useMemo, useRef, useState, type ReactNode } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownUp, ChevronDown, ChevronUp, FileText, Loader2, Search, Table2 } from "lucide-react";
import { desktop, type FileDiff, type TableColumn, type TableCompare, type TableRow, type TableSheet } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { TextDiff, type DiffLayout, type LineComments } from "@/components/changes/TextDiff";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { ColumnResizer } from "@/components/ui/resize-handle";
import { useColumnWidths } from "@/hooks/useColumnWidths";
import { changedColumns, columnChanges, filterRows, isChange, nextChange, visibleColumns } from "@/lib/table";
import { cn } from "@/lib/utils";

type Mode = "table" | "text";

export function TableDiff({ root, diff, layout, comments }: { root: string; diff: FileDiff; layout: DiffLayout; comments?: LineComments }) {
  const hasText = (diff.hunks ?? []).length > 0;
  const [mode, setMode] = useState<Mode>(comments && hasText ? "text" : "table");
  const [sheet, setSheet] = useState("");
  const { old, next } = imageRefs(diff);
  const base = viewerKey(root, diff, "table");
  const query = useQuery({
    queryKey: [...base, sheet],
    queryFn: () => desktop.tableDiff(root, old, next, sheet),
    staleTime: 30_000,
    enabled: mode === "table",
    placeholderData: (prev, prevQuery) => (base.every((v, i) => prevQuery?.queryKey[i] === v) ? keepPreviousData(prev) : undefined),
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
          <TextDiff diff={diff} layout={layout} comments={comments} />
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
  return <TableCompareView compare={query.data} toolbar={switcher || null} storageKey={`table:${diff.path}`} onSheet={setSheet} />;
}

const ROW = 26;
const CHANGED_ROW = 42;
const COL_WIDTH = 160;
const NO_DEFAULTS: Record<string, number> = {};
const GUTTER = 88;

const hasChanges = (c: TableCompare) => c.rowsAdded + c.rowsRemoved + c.rowsChanged + c.rowsMoved + c.columnsAdded + c.columnsRemoved > 0;

type ViewProps = { compare: TableCompare; toolbar?: ReactNode; storageKey?: string; onSheet?: (name: string) => void };

export function TableCompareView({ compare, toolbar = null, storageKey = "table", onSheet }: ViewProps) {
  const columns = compare.columns ?? [];
  const sizes = useColumnWidths(compare.sheet ? `${storageKey}#${compare.sheet}` : storageKey, NO_DEFAULTS, 48);
  const ids = useMemo(() => columnIds(columns), [columns]);
  const widths = ids.map((id) => sizes.width(id, COL_WIDTH));
  const all = compare.rows ?? [];
  const [onlyChanges, setOnlyChanges] = useState(() => hasChanges(compare) && all.length > 50);
  const [text, setText] = useState("");
  const [column, setColumn] = useState(-1);
  const [onlyChangedColumns, setOnlyChangedColumns] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const counts = useMemo(() => columnChanges(columns, all), [columns, all]);
  const touched = useMemo(() => changedColumns(columns, all), [columns, all]);
  const order = useMemo(() => visibleColumns(columns, touched, compare.keyColumn, onlyChangedColumns), [columns, touched, compare.keyColumn, onlyChangedColumns]);
  const filterColumn = column < columns.length ? column : -1;
  const rows = useMemo(() => filterRows(all, { onlyChanges, text, column: filterColumn }), [all, onlyChanges, text, filterColumn]);
  const changeRows = useMemo(() => rows.flatMap((r, i) => (isChange(r) ? [i] : [])), [rows]);
  const [seenRows, setSeenRows] = useState(rows);
  if (seenRows !== rows) {
    setSeenRows(rows);
    setCursor(-1);
  }
  const parent = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: (i) => (rows[i].status === "changed" ? CHANGED_ROW : ROW),
    overscan: 16,
    initialRect: { width: 800, height: 600 },
  });
  const jump = (dir: 1 | -1) => {
    const i = nextChange(rows, cursor, dir);
    if (i < 0) return;
    setCursor(i);
    virtualizer.scrollToIndex(i, { align: "center" });
  };
  const shownWidths = order.map((i) => widths[i]);
  const width = shownWidths.reduce((sum, w) => sum + w, GUTTER);
  const keyIndex = compare.keyColumn >= 0 && order[0] === compare.keyColumn ? compare.keyColumn : -1;
  const key = compare.keyColumn >= 0 ? columns[compare.keyColumn] : null;
  const position = changeRows.indexOf(cursor) + 1;
  const filtered = text.trim() !== "" || onlyChanges;

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
      {(compare.sheets ?? []).length > 0 && <SheetTabs compare={compare} onSheet={onSheet} />}
      {compare.error ? (
        <div className="p-4 text-[11px]">
          <Notice>{`This file cannot be compared as a table: ${compare.error}.`}</Notice>
        </div>
      ) : (
        <>
          <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-1 text-[11px]">
            <div className="flex items-center gap-1 rounded-md border border-border bg-card px-1.5">
              <Search className="size-3 text-muted-foreground" />
              <input
                type="search"
                value={text}
                onChange={(e) => setText(e.target.value)}
                placeholder="Filter rows"
                aria-label="Filter rows"
                className="h-6 w-40 bg-transparent outline-none placeholder:text-muted-foreground"
              />
            </div>
            <select
              value={filterColumn}
              onChange={(e) => setColumn(Number(e.target.value))}
              aria-label="Filter column"
              className="h-6 max-w-40 rounded-md border border-border bg-card px-1"
            >
              <option value={-1}>All columns</option>
              {columns.map((c, i) => (
                <option key={i} value={i}>
                  {columnLabel(c, i)}
                </option>
              ))}
            </select>
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={onlyChangedColumns} onChange={(e) => setOnlyChangedColumns(e.target.checked)} className="size-3.5 accent-[var(--accent)]" />
              Changed columns only
            </label>
            {filtered && <span className="text-muted-foreground">{`${rows.length.toLocaleString()} of ${count(all.length, "row")}`}</span>}
            <div className="ml-auto flex items-center gap-1.5">
              <span className="text-muted-foreground" aria-live="polite">
                {changeRows.length === 0 ? "No changes" : position > 0 ? `${position} of ${count(changeRows.length, "change")}` : count(changeRows.length, "change")}
              </span>
              <Group label="Changes">
                <ToolButton label="Previous change" onClick={() => jump(-1)}>
                  <ChevronUp className="size-3.5" />
                </ToolButton>
                <ToolButton label="Next change" onClick={() => jump(1)}>
                  <ChevronDown className="size-3.5" />
                </ToolButton>
              </Group>
            </div>
          </div>
          <div ref={parent} className="min-h-0 flex-1 overflow-auto bg-card text-[12px]" role="table" aria-label="Table changes" aria-rowcount={rows.length + 1}>
            <div style={{ width }} className="sticky top-0 z-10 flex border-b border-border bg-sidebar font-medium" role="row">
              <div className="sticky left-0 z-[1] shrink-0 bg-sidebar px-2 py-1 text-[11px] text-muted-foreground" style={{ width: GUTTER }} role="columnheader">
                Row
              </div>
              {order.map((i) => (
                <ColumnHeader key={i} column={columns[i]} index={i} id={ids[i]} width={widths[i]} sizes={sizes} changes={counts[i]} sticky={i === keyIndex} />
              ))}
            </div>
            {rows.length === 0 ? (
              <p className="p-4 text-[11px] text-muted-foreground">{text.trim() ? "No rows match the filter." : onlyChanges ? "No rows changed." : "The table is empty."}</p>
            ) : (
              <div style={{ height: virtualizer.getTotalSize(), width, position: "relative" }}>
                {virtualizer.getVirtualItems().map((item) => (
                  <Row
                    key={item.key}
                    row={rows[item.index]}
                    columns={columns}
                    order={order}
                    widths={widths}
                    total={width}
                    top={item.start}
                    height={item.size}
                    stickyColumn={keyIndex}
                    current={item.index === cursor}
                  />
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

const sheetTone: Record<string, string> = { added: "bg-success", removed: "bg-danger", changed: "bg-warning" };

function sheetTitle(s: TableSheet): string {
  if (s.status === "added" || s.status === "removed") return `${s.name}: sheet ${s.status}`;
  if (s.status === "same") return `${s.name}: no changes`;
  const parts = [s.rowsAdded && `+${count(s.rowsAdded, "row")}`, s.rowsRemoved && `−${count(s.rowsRemoved, "row")}`, s.rowsChanged && `${count(s.rowsChanged, "row")} changed (${count(s.cellsChanged, "cell")})`];
  return `${s.name}: ${parts.filter(Boolean).join(", ") || "columns changed"}`;
}

function SheetTabs({ compare, onSheet }: { compare: TableCompare; onSheet?: (name: string) => void }) {
  return (
    <div className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-border px-2 pt-1 text-[11px]" role="tablist" aria-label="Sheets">
      {(compare.sheets ?? []).map((s) => {
        const selected = s.name === compare.sheet;
        return (
          <button
            key={s.name}
            type="button"
            role="tab"
            aria-selected={selected}
            title={sheetTitle(s)}
            onClick={() => onSheet?.(s.name)}
            className={cn(
              "flex shrink-0 items-center gap-1.5 rounded-t-md border border-b-0 px-2.5 py-1",
              selected ? "border-border bg-card text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
              s.status === "removed" && "line-through",
            )}
          >
            {s.name || "Sheet"}
            {s.status !== "same" && <span className={cn("size-1.5 rounded-full", sheetTone[s.status])} aria-label={`Sheet ${s.status}`} />}
          </button>
        );
      })}
    </div>
  );
}

const letters = (i: number): string => (i < 26 ? String.fromCharCode(65 + i) : letters(Math.floor(i / 26) - 1) + letters(i % 26));
const columnLabel = (c: TableColumn, i: number) => c.name || `column ${letters(i)}`;

export function columnIds(columns: TableColumn[]): string[] {
  const seen = new Map<string, number>();
  return columns.map((c, i) => {
    const base = c.name || letters(i);
    const n = (seen.get(base) ?? 0) + 1;
    seen.set(base, n);
    return n === 1 ? base : `${base} #${n}`;
  });
}

type HeaderProps = { column: TableColumn; index: number; id: string; width: number; sizes: ReturnType<typeof useColumnWidths>; changes: number; sticky: boolean };

function ColumnHeader({ column, index, id, width, sizes, changes, sticky }: HeaderProps) {
  const added = column.old < 0;
  const removed = column.new < 0;
  const renamed = !added && !removed && column.oldName !== column.name && column.oldName !== "";
  return (
    <div className={cn("relative shrink-0", sticky && "sticky z-[1] bg-sidebar")} style={{ width, left: sticky ? GUTTER : undefined }}>
      <div
        role="columnheader"
        title={renamed ? `${column.oldName} → ${column.name}` : column.name}
        className={cn("flex items-center gap-1 border-l border-border px-2 py-1", added && "bg-success/15 text-success", removed && "bg-danger/15 text-danger line-through")}
      >
        <span className="truncate">
          {column.name || <span className="text-muted-foreground">{letters(index)}</span>}
          {renamed && <span className="ml-1 font-normal text-warning">(was {column.oldName})</span>}
        </span>
        {changes > 0 && (
          <span className="ml-auto shrink-0 rounded bg-warning/15 px-1 text-[10px] font-normal text-warning" title={`${count(changes, "changed cell")} in this column`}>
            {changes.toLocaleString()}
          </span>
        )}
      </div>
      <ColumnResizer sizes={sizes} id={id} label={column.name ? id : columnLabel(column, index)} />
    </div>
  );
}

const rowTone: Record<string, string> = {
  added: "bg-success/10",
  removed: "bg-danger/10 text-muted-foreground line-through",
  changed: "",
  same: "",
};

const stickyTone: Record<string, string> = { added: "bg-success/10", removed: "bg-danger/10" };

type RowProps = {
  row: TableRow;
  columns: TableColumn[];
  order: number[];
  widths: number[];
  total: number;
  top: number;
  height: number;
  stickyColumn: number;
  current: boolean;
};

function Row({ row, columns, order, widths, total, top, height, stickyColumn, current }: RowProps) {
  const changed = new Set(row.changed ?? []);
  const overlay = <span className={cn("pointer-events-none absolute inset-0", stickyTone[row.status])} />;
  return (
    <div
      role="row"
      data-status={row.status}
      aria-current={current || undefined}
      className={cn("absolute left-0 flex border-b border-border/60", rowTone[row.status], current && "outline outline-1 -outline-offset-1 outline-accent")}
      style={{ top, height, width: total }}
    >
      <div className="sticky left-0 z-[1] flex shrink-0 items-center gap-1 bg-card px-2 font-mono text-[10px] text-muted-foreground no-underline" style={{ width: GUTTER }} role="rowheader">
        {overlay}
        <span>{row.oldLine || "·"}</span>
        <span>→</span>
        <span>{row.newLine || "·"}</span>
        {row.moved && <ArrowDownUp className="size-3 text-accent" aria-label="Moved" />}
      </div>
      {order.map((i) => {
        const c = columns[i];
        const value = (row.cells ?? [])[i] ?? "";
        const isChanged = changed.has(i);
        const sticky = i === stickyColumn;
        return (
          <div
            key={i}
            role="cell"
            title={isChanged ? `${row.old?.[i] ?? ""} → ${value}` : value}
            className={cn(
              "flex shrink-0 flex-col justify-center overflow-hidden border-l border-border/60 px-2",
              sticky && "sticky z-[1] bg-card",
              isChanged && "bg-warning/15",
              c.old < 0 && row.status !== "added" && "text-success",
              c.new < 0 && row.status !== "removed" && "text-muted-foreground line-through",
            )}
            style={{ width: widths[i], left: sticky ? GUTTER : undefined }}
          >
            {sticky && overlay}
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
