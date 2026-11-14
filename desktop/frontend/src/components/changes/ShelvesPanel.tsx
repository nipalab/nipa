import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ChevronDown, ChevronRight, Lock, Trash2 } from "lucide-react";
import { desktop, type Shelf } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { formatBytes, splitPath } from "@/lib/diff";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";

const OPEN_KEY = "nipa.shelves.open";

export type ShelfPick = { id: string; path: string };

export const shelvesKey = (root: string) => ["shelves", root];

type ShelvesPanelProps = {
  root: string;
  busy: boolean;
  picked: ShelfPick | null;
  onPick: (pick: ShelfPick | null) => void;
  onApply: (shelf: Shelf, keep: boolean) => void;
  onError: (message: string) => void;
};

// ShelvesPanel lists the clone's shelves under the changed files: each one
// can be previewed file by file, applied (optionally kept) or deleted.
export function ShelvesPanel({ root, busy, picked, onPick, onApply, onError }: ShelvesPanelProps) {
  const queryClient = useQueryClient();
  const [open, setOpenState] = useState(() => readSetting(OPEN_KEY) !== "closed");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Shelf | null>(null);
  const list = useQuery({ queryKey: shelvesKey(root), queryFn: () => desktop.shelves(root) });
  const drop = useMutation({
    mutationFn: (id: string) => desktop.dropShelf(root, id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: shelvesKey(root) }),
    onError: (err) => onError(`Could not delete the shelf: ${errorMessage(err)}`),
  });
  const shelves = list.data?.shelves ?? [];
  const gone = !!picked && !!list.data && !shelves.some((sh) => sh.id === picked.id);
  useEffect(() => {
    if (gone) onPick(null);
  }, [gone, onPick]);
  if (shelves.length === 0) return null;
  const setOpen = (next: boolean) => {
    writeSetting(OPEN_KEY, next ? "open" : "closed");
    setOpenState(next);
  };
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <section aria-label="Shelves" className="flex max-h-[45%] shrink-0 flex-col border-t border-border">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex shrink-0 items-center gap-2 bg-sidebar px-3 py-1.5 text-left text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        <Chevron className="size-3.5" />
        <Archive className="size-3.5" />
        <span>
          Shelves <span className="font-normal">({shelves.length})</span>
        </span>
      </button>
      {open && (
        <ul className="min-h-0 flex-1 overflow-y-auto">
          {shelves.map((shelf) => (
            <ShelfRow
              key={shelf.id}
              shelf={shelf}
              applying={list.data?.applying === shelf.id}
              expanded={expanded === shelf.id}
              busy={busy || drop.isPending}
              picked={picked}
              onExpand={() => setExpanded(expanded === shelf.id ? null : shelf.id)}
              onPick={onPick}
              onApply={(keep) => onApply(shelf, keep)}
              onDelete={() => setDeleting(shelf)}
            />
          ))}
        </ul>
      )}
      {deleting && (
        <ConfirmDialog
          title="Delete shelf?"
          message={`"${deleting.name}" and the ${deleting.entries.length} file version${deleting.entries.length === 1 ? "" : "s"} in it are deleted. This cannot be undone.`}
          confirmLabel="Delete"
          onCancel={() => setDeleting(null)}
          onConfirm={() => {
            drop.mutate(deleting.id);
            setDeleting(null);
          }}
        />
      )}
    </section>
  );
}

type ShelfRowProps = {
  shelf: Shelf;
  applying: boolean;
  expanded: boolean;
  busy: boolean;
  picked: ShelfPick | null;
  onExpand: () => void;
  onPick: (pick: ShelfPick) => void;
  onApply: (keep: boolean) => void;
  onDelete: () => void;
};

function ShelfRow({ shelf, applying, expanded, busy, picked, onExpand, onPick, onApply, onDelete }: ShelfRowProps) {
  const Chevron = expanded ? ChevronDown : ChevronRight;
  const where = shelf.tag ? `tag ${shelf.tag}` : shelf.branch;
  return (
    <li className="border-b border-border last:border-b-0">
      <div className="flex items-center gap-1.5 px-3 py-1.5 text-[12px]">
        <button type="button" aria-expanded={expanded} onClick={onExpand} className="flex min-w-0 flex-1 items-center gap-1.5 text-left" title={shelf.name}>
          <Chevron className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">{shelf.name}</span>
            <span className="block truncate text-[11px] text-muted-foreground">
              {shelf.entries.length} file{shelf.entries.length === 1 ? "" : "s"} · {where} · {timeAgo(shelf.createdAt)}
              {shelf.size > 0 && ` · ${formatBytes(shelf.size)}`}
            </span>
          </span>
        </button>
        {applying && <span className="shrink-0 rounded-full bg-warning/20 px-1.5 text-[10px] text-warning">Applying</span>}
      </div>
      {expanded && (
        <>
          <ul aria-label={`Files in ${shelf.name}`} className="pb-1">
            {shelf.entries.map((e) => {
              const { dir, name } = splitPath(e.path);
              const active = picked?.id === shelf.id && picked.path === e.path;
              return (
                <li key={e.path}>
                  <button
                    type="button"
                    aria-current={active}
                    onClick={() => onPick({ id: shelf.id, path: e.path })}
                    title={e.path}
                    className={cn("flex w-full items-center gap-2 py-1 pl-8 pr-3 text-left text-[12px] hover:bg-muted", active && "bg-accent-soft hover:bg-accent-soft")}
                  >
                    <FileIcon kind={e.kind} />
                    <span className="min-w-0 flex-1 truncate">
                      {name}
                      {dir && <span className="ml-1.5 text-[11px] text-muted-foreground">{dir.slice(0, -1)}</span>}
                    </span>
                    {e.locked && <Lock className="size-3 shrink-0 text-accent" aria-label="You held a lock on it when shelving" />}
                    {e.staged && <span className="shrink-0 text-[10px] text-muted-foreground">staged</span>}
                    <StatusLetter status={e.untracked ? "untracked" : e.status} />
                  </button>
                </li>
              );
            })}
          </ul>
          <div className="flex flex-wrap gap-1.5 px-3 pb-2">
            <Button size="sm" variant="primary" disabled={busy || applying} onClick={() => onApply(false)} title="Bring the changes back and delete the shelf">
              Apply
            </Button>
            <Button size="sm" disabled={busy || applying} onClick={() => onApply(true)} title="Bring the changes back and keep the shelf">
              Apply and keep
            </Button>
            <Button size="sm" variant="ghost" className="ml-auto text-danger" disabled={busy || applying} onClick={onDelete} aria-label={`Delete ${shelf.name}`}>
              <Trash2 />
              Delete
            </Button>
          </div>
        </>
      )}
    </li>
  );
}
