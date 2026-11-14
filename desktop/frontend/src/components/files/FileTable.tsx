import { useEffect, useRef, useState, type MouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp, CloudDownload, Folder, Lock } from "lucide-react";
import { desktop, type FileLock } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { ColumnResizer } from "@/components/ui/resize-handle";
import { useColumnWidths } from "@/hooks/useColumnWidths";
import { formatBytes } from "@/lib/diff";
import { canPreview, kindName, lockFor, lockState, thumbKey, type Row, type Sort, type SortKey } from "@/lib/files";
import { cn } from "@/lib/utils";

const ROW_HEIGHT = 28;
const HEADER_HEIGHT = 32;
const CELL_WIDTH = 132;
const CELL_HEIGHT = 150;

type TableProps = {
  root: string;
  rows: Row[];
  locks: FileLock[];
  selected: string;
  onSelect: (row: Row) => void;
  onOpen: (row: Row) => void;
  onMenu: (row: Row, x: number, y: number) => void;
};

const columns: { key: SortKey; label: string; width: number; className: string }[] = [
  { key: "name", label: "Name", width: 280, className: "" },
  { key: "kind", label: "Kind", width: 96, className: "" },
  { key: "size", label: "Size", width: 80, className: "justify-end" },
  { key: "status", label: "Status", width: 64, className: "justify-center" },
  { key: "lock", label: "Lock", width: 112, className: "" },
];
const COLUMN_DEFAULTS = Object.fromEntries(columns.map((c) => [c.key, c.width]));
const ROW_PADDING = 24;

function menuHandler(row: Row, onSelect: (r: Row) => void, onMenu: TableProps["onMenu"]) {
  return (e: MouseEvent) => {
    e.preventDefault();
    onSelect(row);
    onMenu(row, e.clientX, e.clientY);
  };
}

function LockCell({ lock }: { lock?: FileLock }) {
  const state = lockState(lock);
  if (!state) return null;
  const who = state === "mine" ? "You" : lock?.heldByName || lock?.heldBy || "Someone";
  return (
    <span className={cn("flex min-w-0 items-center gap-1 truncate", state === "mine" ? "text-success" : "text-warning")} title={`Locked by ${who}${lock && lock.path ? ` (${lock.path})` : ""}`}>
      <Lock className="size-3 shrink-0" />
      <span className="truncate">{who}</span>
    </span>
  );
}

export function FileList({ rows, locks, selected, onSelect, onOpen, onMenu, sort, onSort }: TableProps & { sort: Sort; onSort: (key: SortKey) => void }) {
  const parent = useRef<HTMLDivElement>(null);
  const sizes = useColumnWidths("files", COLUMN_DEFAULTS, 48);
  const w = (key: SortKey) => ({ width: sizes.width(key), flex: "none" as const });
  const total = columns.reduce((sum, c) => sum + sizes.width(c.key), ROW_PADDING);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 20,
    scrollMargin: HEADER_HEIGHT,
    initialRect: { width: 800, height: 800 },
  });
  return (
    <div ref={parent} className="h-full min-h-0 overflow-auto" role="table" aria-label="Files" aria-rowcount={rows.length}>
      <div style={{ minWidth: total }}>
        <div role="row" className="sticky top-0 z-10 flex h-8 items-center border-b border-border bg-background px-3 text-[11px] text-muted-foreground">
          {columns.map((c) => (
            <span
              key={c.key}
              role="columnheader"
              aria-label={c.label}
              aria-sort={sort.key === c.key ? (sort.desc ? "descending" : "ascending") : "none"}
              className={cn("relative flex h-full items-center pr-2", c.className)}
              style={w(c.key)}
            >
              <button type="button" onClick={() => onSort(c.key)} className="flex min-w-0 items-center gap-0.5 truncate hover:text-foreground">
                {c.label}
                {sort.key === c.key && (sort.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
              </button>
              <ColumnResizer sizes={sizes} id={c.key} label={c.label} />
            </span>
          ))}
        </div>
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            const lock = row.remote ? undefined : lockFor(row.path, locks);
            return (
              <div
                key={row.path}
                role="row"
                aria-selected={row.path === selected}
                data-index={item.index}
                tabIndex={0}
                onClick={() => onSelect(row)}
                onDoubleClick={() => onOpen(row)}
                onKeyDown={(e) => e.key === "Enter" && onOpen(row)}
                onContextMenu={menuHandler(row, onSelect, onMenu)}
                title={row.remote ? `${row.path} is not in this clone's checkout` : row.path}
                className={cn(
                  "absolute left-0 right-0 flex cursor-default items-center px-3 text-[12px] outline-none hover:bg-muted focus-visible:bg-muted",
                  row.path === selected && "bg-muted font-medium",
                  row.remote && "text-muted-foreground/70",
                )}
                style={{ top: 0, height: ROW_HEIGHT, transform: `translateY(${item.start - HEADER_HEIGHT}px)` }}
              >
                <span role="cell" className="flex min-w-0 items-center gap-1.5 pr-2" style={w("name")}>
                  {row.remote ? (
                    <CloudDownload className="size-3.5 shrink-0" aria-hidden />
                  ) : row.isDir ? (
                    <Folder className={cn("size-3.5 shrink-0 text-accent", row.status && "text-success")} aria-hidden />
                  ) : (
                    <FileIcon kind={row.kind} />
                  )}
                  <span className="truncate">{row.name}</span>
                  {row.childrenChanged && <span className="size-1.5 shrink-0 rounded-full bg-warning" title="Something inside changed" aria-label="Changes inside" />}
                </span>
                <span role="cell" className="truncate pr-2 text-muted-foreground" style={w("kind")}>
                  {row.remote ? "Not downloaded" : kindName(row)}
                </span>
                <span role="cell" className="truncate pr-2 text-right font-mono text-[11px] text-muted-foreground" style={w("size")}>
                  {row.isDir ? "" : formatBytes(row.size)}
                </span>
                <span role="cell" className="flex justify-center pr-2" style={w("status")}>
                  {row.status && <StatusLetter status={row.status} />}
                </span>
                <span role="cell" className="min-w-0 pr-2 text-[11px]" style={w("lock")}>
                  <LockCell lock={lock} />
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function useColumnsFor(el: HTMLDivElement | null): number {
  const [width, setWidth] = useState(800);
  useEffect(() => {
    if (!el) return;
    const measure = () => setWidth(el.clientWidth || 800);
    measure();
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
    observer?.observe(el);
    return () => observer?.disconnect();
  }, [el]);
  return Math.max(1, Math.floor(width / CELL_WIDTH));
}

export function FileGrid({ root, rows, locks, selected, onSelect, onOpen, onMenu }: TableProps) {
  const [el, setEl] = useState<HTMLDivElement | null>(null);
  const perRow = useColumnsFor(el);
  const lines = Math.ceil(rows.length / perRow);
  const virtualizer = useVirtualizer({
    count: lines,
    getScrollElement: () => el,
    estimateSize: () => CELL_HEIGHT,
    overscan: 3,
    initialRect: { width: 800, height: 800 },
  });
  return (
    <div ref={setEl} className="h-full overflow-y-auto" role="grid" aria-label="Files" aria-rowcount={lines}>
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((item) => (
          <div key={item.key} role="row" className="absolute left-0 right-0 flex gap-1 px-2" style={{ top: 0, height: CELL_HEIGHT, transform: `translateY(${item.start}px)` }}>
            {rows.slice(item.index * perRow, item.index * perRow + perRow).map((row) => {
              const lock = row.remote ? undefined : lockFor(row.path, locks);
              return (
                <button
                  key={row.path}
                  type="button"
                  role="gridcell"
                  aria-selected={row.path === selected}
                  onClick={() => onSelect(row)}
                  onDoubleClick={() => onOpen(row)}
                  onContextMenu={menuHandler(row, onSelect, onMenu)}
                  title={row.path}
                  style={{ width: CELL_WIDTH - 4 }}
                  className={cn(
                    "mt-1 flex h-[142px] flex-col items-center gap-1 rounded-md border border-transparent p-1.5 text-[11px] hover:bg-muted",
                    row.path === selected && "border-accent bg-muted",
                    row.remote && "opacity-60",
                  )}
                >
                  <span className="relative flex size-[104px] items-center justify-center overflow-hidden rounded">
                    {canPreview(row) ? <Thumbnail root={root} row={row} /> : <BigIcon row={row} />}
                    {row.status && (
                      <span className="absolute right-0.5 top-0.5 rounded bg-card/90 px-0.5">
                        <StatusLetter status={row.status} />
                      </span>
                    )}
                    {lockState(lock) && (
                      <span className={cn("absolute bottom-0.5 right-0.5 rounded bg-card/90 p-0.5", lockState(lock) === "mine" ? "text-success" : "text-warning")}>
                        <Lock className="size-3" aria-label={lockState(lock) === "mine" ? "Locked by you" : `Locked by ${lock?.heldByName || "someone"}`} />
                      </span>
                    )}
                  </span>
                  <span className="w-full truncate text-center">{row.name}</span>
                </button>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

function BigIcon({ row }: { row: Row }) {
  if (row.remote) return <CloudDownload className="size-10 text-muted-foreground" aria-hidden />;
  if (row.isDir) return <Folder className={cn("size-12 text-accent", row.status && "text-success")} aria-hidden />;
  return <FileIcon kind={row.kind} className="size-10" />;
}

// Thumbnail is fetched only while its cell is rendered (the grid is
// virtualized) and cached by the file version.
export function Thumbnail({ root, row }: { root: string; row: Row }) {
  const thumb = useQuery({
    queryKey: ["thumb", root, row.path, thumbKey(row)],
    queryFn: () => desktop.imageDiff(root, null, { source: "working", path: row.path }, { maxSize: 128 }),
    staleTime: Infinity,
    gcTime: 10 * 60_000,
    retry: false,
  });
  const info = thumb.data?.new;
  if (info && !info.hdr && info.src) return <img src={info.src} alt={row.name} className="checkerboard max-h-full max-w-full object-contain" />;
  return <FileIcon kind={row.kind} className="size-10" />;
}
