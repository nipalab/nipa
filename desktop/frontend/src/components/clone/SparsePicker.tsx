import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Folder, Loader2 } from "lucide-react";
import type { RemoteListing } from "@/api/desktop";
import { formatBytes } from "@/lib/diff";
import { coveredByAncestor, hasSelectedDescendant, isCovered, toggleFolder } from "@/lib/sparse";
import { cn, errorMessage } from "@/lib/utils";

type Loader = (path: string) => Promise<RemoteListing>;

type SparsePickerProps = {
  load: Loader;
  cacheKey: unknown[];
  value: string[] | null;
  onChange: (value: string[] | null) => void;
};

// SparsePicker chooses between a full checkout (null) and a set of folders,
// browsing the server's tree one directory at a time.
export function SparsePicker({ load, cacheKey, value, onChange }: SparsePickerProps) {
  const selection = value ?? [];
  return (
    <div className="space-y-2 text-[12px]">
      <div className="flex gap-4" role="radiogroup" aria-label="Checkout">
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={value === null} onChange={() => onChange(null)} className="accent-[var(--accent)]" />
          Everything
        </label>
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={value !== null} onChange={() => onChange(value ?? [])} className="accent-[var(--accent)]" />
          Only some folders
        </label>
      </div>
      {value !== null && (
        <div className="max-h-64 overflow-auto rounded-md border border-border bg-background p-1" role="tree" aria-label="Folders">
          <Level path="" depth={0} load={load} cacheKey={cacheKey} selection={selection} onToggle={(p) => onChange(toggleFolder(selection, p))} />
        </div>
      )}
      {value !== null && (
        <p className="text-[11px] text-muted-foreground">
          {selection.length === 0 ? "Tick the folders to download." : `${selection.length} folder${selection.length === 1 ? "" : "s"} selected.`} Other files stay on the server and can be added later.
        </p>
      )}
    </div>
  );
}

type LevelProps = { path: string; depth: number; load: Loader; cacheKey: unknown[]; selection: string[]; onToggle: (path: string) => void };

function Level({ path, depth, load, cacheKey, selection, onToggle }: LevelProps) {
  const listing = useQuery({ queryKey: [...cacheKey, "tree", path], queryFn: () => load(path), staleTime: 60_000 });
  const pad = { paddingLeft: depth * 16 + 6 };
  if (listing.isPending) {
    return (
      <div className="flex items-center gap-1.5 py-1 text-muted-foreground" style={pad}>
        <Loader2 className="size-3 animate-spin" /> Loading…
      </div>
    );
  }
  if (listing.isError) {
    return (
      <p className="py-1 text-danger" style={pad}>
        {errorMessage(listing.error)}
      </p>
    );
  }
  const { dirs, files, fileBytes } = listing.data;
  return (
    <>
      {dirs.map((d) => (
        <FolderRow key={d.path} name={d.name} path={d.path} depth={depth} load={load} cacheKey={cacheKey} selection={selection} onToggle={onToggle} />
      ))}
      {files > 0 && (
        <p className="py-0.5 text-[11px] text-muted-foreground" style={pad}>
          {files} file{files === 1 ? "" : "s"} · {formatBytes(fileBytes)}
          {depth === 0 ? " at the top level (only with Everything)" : ""}
        </p>
      )}
      {dirs.length === 0 && files === 0 && (
        <p className="py-0.5 text-[11px] text-muted-foreground" style={pad}>
          Empty
        </p>
      )}
    </>
  );
}

function FolderRow({ name, path, depth, load, cacheKey, selection, onToggle }: Omit<LevelProps, "path"> & { name: string; path: string }) {
  const [open, setOpen] = useState(false);
  const inherited = coveredByAncestor(path, selection);
  const checked = isCovered(path, selection);
  const partial = !checked && hasSelectedDescendant(path, selection);
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div role="treeitem" aria-expanded={open} aria-selected={checked}>
      <div className="flex items-center gap-1 rounded py-0.5 hover:bg-muted" style={{ paddingLeft: depth * 16 }}>
        <button type="button" onClick={() => setOpen(!open)} aria-label={`${open ? "Collapse" : "Expand"} ${path}`} className="text-muted-foreground hover:text-foreground">
          <Chevron className="size-3.5" />
        </button>
        <label className={cn("flex min-w-0 flex-1 items-center gap-1.5", inherited && "text-muted-foreground")}>
          <input
            type="checkbox"
            checked={checked}
            disabled={inherited}
            ref={(el) => {
              if (el) el.indeterminate = partial;
            }}
            onChange={() => onToggle(path)}
            aria-label={path}
            className="size-3.5 accent-[var(--accent)]"
          />
          <Folder className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{name}</span>
        </label>
      </div>
      {open && <Level path={path} depth={depth + 1} load={load} cacheKey={cacheKey} selection={selection} onToggle={onToggle} />}
    </div>
  );
}
