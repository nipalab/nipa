import { useCallback, useEffect, useMemo, useRef, type KeyboardEvent } from "react";
import { defaultRangeExtractor, useVirtualizer, type Range } from "@tanstack/react-virtual";
import { Archive, Lock, LockOpen, TriangleAlert, Undo2 } from "lucide-react";
import type { ChangeSet, FileChange, FileLockState } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { Button } from "@/components/ui/button";
import { splitPath } from "@/lib/diff";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

export type Selection = { path: string; staged: boolean };

type Row =
  | { type: "header"; staged: boolean; count: number }
  | { type: "file"; staged: boolean; file: FileChange };

const ROW_HEIGHT = 28;

type FileListProps = {
  changes: ChangeSet;
  selected: Selection | null;
  busy: boolean;
  onSelect: (sel: Selection) => void;
  onToggle?: (sel: Selection) => void;
  onStage: (paths: string[]) => void;
  onUnstage: (paths: string[]) => void;
  onDiscard: (file: FileChange) => void;
  warnings?: Map<string, string>;
  locks?: Map<string, FileLockState>;
  onLock?: (path: string) => void;
  onShelve?: (path: string) => void;
};

export function buildRows(changes: ChangeSet): Row[] {
  const rows: Row[] = [];
  if (changes.staged.length > 0) {
    rows.push({ type: "header", staged: true, count: changes.staged.length });
    for (const file of changes.staged) rows.push({ type: "file", staged: true, file });
  }
  if (changes.unstaged.length > 0) {
    rows.push({ type: "header", staged: false, count: changes.unstaged.length });
    for (const file of changes.unstaged) rows.push({ type: "file", staged: false, file });
  }
  return rows;
}

export function stickyHeader(headers: number[], start: number): number {
  let active = -1;
  for (const i of headers) {
    if (i > start) break;
    active = i;
  }
  return active;
}

export function FileList({ changes, selected, busy, onSelect, onToggle, onStage, onUnstage, onDiscard, warnings, locks, onLock, onShelve }: FileListProps) {
  const { t } = useTranslation();
  const rows = useMemo(() => buildRows(changes), [changes]);
  const parent = useRef<HTMLDivElement>(null);
  const headers = useMemo(() => rows.flatMap((r, i) => (r.type === "header" ? [i] : [])), [rows]);
  const active = useRef(-1);
  const rangeExtractor = useCallback(
    (range: Range) => {
      active.current = stickyHeader(headers, range.startIndex);
      const indexes = defaultRangeExtractor(range);
      if (active.current < 0 || indexes.includes(active.current)) return indexes;
      return [active.current, ...indexes].sort((a, b) => a - b);
    },
    [headers],
  );
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parent.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    initialRect: { width: 320, height: 640 },
    scrollPaddingStart: ROW_HEIGHT,
    rangeExtractor,
  });

  const fileIndexes = useMemo(() => rows.flatMap((r, i) => (r.type === "file" ? [i] : [])), [rows]);
  const selectedIndex = rows.findIndex((r) => r.type === "file" && r.file.path === selected?.path && r.staged === selected?.staged);

  useEffect(() => {
    if (selectedIndex >= 0) virtualizer.scrollToIndex(selectedIndex);
  }, [selectedIndex, virtualizer]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === " " && e.target === e.currentTarget) {
      e.preventDefault();
      if (selected && !busy) onToggle?.(selected);
      return;
    }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const pos = fileIndexes.indexOf(selectedIndex);
    const next = fileIndexes[Math.max(0, Math.min(fileIndexes.length - 1, pos + (e.key === "ArrowDown" ? 1 : -1)))];
    const row = rows[next];
    if (row?.type !== "file") return;
    onSelect({ path: row.file.path, staged: row.staged });
  };

  return (
    <div ref={parent} role="listbox" aria-label={t("fileList.changedFiles")} tabIndex={0} onKeyDown={onKeyDown} className="min-h-0 flex-1 overflow-y-auto outline-none">
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((item) => {
          const row = rows[item.index];
          const pinned = item.index === active.current;
          return (
            <div
              key={item.key}
              data-pinned={pinned || undefined}
              style={
                pinned
                  ? { position: "sticky", top: 0, zIndex: 1, height: item.size }
                  : { position: "absolute", top: 0, left: 0, right: 0, height: item.size, transform: `translateY(${item.start}px)`, zIndex: row.type === "header" ? 2 : undefined }
              }
            >
              {row.type === "header" ? (
                <SectionHeader
                  staged={row.staged}
                  count={row.count}
                  busy={busy}
                  onAll={() => {
                    const list = row.staged ? changes.staged : changes.unstaged;
                    const paths = list.map((f) => f.path);
                    if (row.staged) onUnstage(paths);
                    else onStage(paths);
                  }}
                />
              ) : (
                <FileRow
                  file={row.file}
                  staged={row.staged}
                  active={item.index === selectedIndex}
                  warning={warnings?.get(row.file.path)}
                  lock={locks?.get(row.file.path)}
                  onLock={onLock ? () => onLock(row.file.path) : undefined}
                  busy={busy}
                  onSelect={() => onSelect({ path: row.file.path, staged: row.staged })}
                  onToggle={() => (row.staged ? onUnstage([row.file.path]) : onStage([row.file.path]))}
                  onDiscard={() => onDiscard(row.file)}
                  onShelve={onShelve ? () => onShelve(row.file.path) : undefined}
                />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function SectionHeader({ staged, count, busy, onAll }: { staged: boolean; count: number; busy: boolean; onAll: () => void }) {
  return (
    <div className="flex h-full items-center justify-between bg-sidebar px-3 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
      <span>
        {staged ? translate("fileList.staged") : translate("fileList.changes")} <span className="font-normal">({count})</span>
      </span>
      <button type="button" disabled={busy} onClick={onAll} className="normal-case tracking-normal text-accent hover:underline disabled:opacity-50">
        {staged ? translate("fileList.unstageAll") : translate("fileList.stageAll")}
      </button>
    </div>
  );
}

type FileRowProps = {
  file: FileChange;
  staged: boolean;
  active: boolean;
  warning?: string;
  lock?: FileLockState;
  onLock?: () => void;
  busy: boolean;
  onSelect: () => void;
  onToggle: () => void;
  onDiscard: () => void;
  onShelve?: () => void;
};

function FileRow({ file, staged, active, warning, lock, onLock, busy, onSelect, onToggle, onDiscard, onShelve }: FileRowProps) {
  const { t } = useTranslation();
  const { dir, name } = splitPath(file.path);
  const discardable = !staged && file.status !== "untracked";
  return (
    <div
      role="option"
      aria-selected={active}
      onClick={onSelect}
      title={file.path}
      className={cn("group flex h-full cursor-default items-center gap-2 px-3 text-[12px] hover:bg-muted", active && "bg-accent-soft hover:bg-accent-soft")}
    >
      <input
        type="checkbox"
        checked={staged}
        disabled={busy}
        onClick={(e) => e.stopPropagation()}
        onChange={onToggle}
        aria-label={translate("fileList.text", { staged: staged ? translate("fileList.unstage") : translate("fileList.stage"), path: file.path })}
        className="size-3.5 shrink-0 accent-[var(--accent)]"
      />
      <FileIcon kind={file.kind} />
      <span className="min-w-0 flex-1 truncate">
        <span className={cn(file.status === "deleted" && "line-through decoration-muted-foreground/60")}>{name}</span>
        {dir && <span className="ml-1.5 text-[11px] text-muted-foreground">{dir.slice(0, -1)}</span>}
      </span>
      {warning && <TriangleAlert className="size-3 shrink-0 text-warning" aria-label={warning} />}
      {lock && <LockBadge lock={lock} busy={busy} onLock={onLock} />}
      {onShelve && (
        <Button
          variant="ghost"
          size="icon"
          disabled={busy}
          className="size-5 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 [&_svg]:size-3"
          aria-label={translate("fileList.shelve", { path: file.path })}
          title={t("fileList.shelveThisFile")}
          onClick={(e) => {
            e.stopPropagation();
            onShelve();
          }}
        >
          <Archive />
        </Button>
      )}
      {discardable && (
        <Button
          variant="ghost"
          size="icon"
          disabled={busy}
          className="size-5 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 [&_svg]:size-3"
          aria-label={translate("fileList.discardChangesTo", { path: file.path })}
          title={t("fileList.discardChanges")}
          onClick={(e) => {
            e.stopPropagation();
            onDiscard();
          }}
        >
          <Undo2 />
        </Button>
      )}
      <StatusLetter status={file.status} />
    </div>
  );
}

function LockBadge({ lock, busy, onLock }: { lock: FileLockState; busy: boolean; onLock?: () => void }) {
  const { t } = useTranslation();
  if (lock.state === "mine") return <Lock className="size-3 shrink-0 text-accent" aria-label={t("fileList.lockedByYou")} />;
  if (lock.state === "theirs") {
    return <Lock className="size-3 shrink-0 text-danger" aria-label={translate("fileList.lockedBy", { value: lock.heldByName || translate("fileList.someoneElse"), lockPath: lock.lockPath !== lock.path ? translate("fileList.folder", { lockPath: lock.lockPath }) : "" })} />;
  }
  const needed = lock.state === "needed";
  if (!onLock) return needed ? <LockOpen className="size-3 shrink-0 text-warning" aria-label={t("fileList.needsALockToPush")} /> : null;
  return (
    <button
      type="button"
      disabled={busy}
      onClick={(e) => {
        e.stopPropagation();
        onLock();
      }}
      aria-label={needed ? translate("fileList.needsALockToPush2") : translate("fileList.lockThisFile")}
      title={needed ? translate("fileList.needsALockToPush3") : translate("fileList.lockThisFile")}
      className={cn("shrink-0 disabled:opacity-50", needed ? "text-warning" : "text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100")}
    >
      <LockOpen className="size-3" />
    </button>
  );
}
