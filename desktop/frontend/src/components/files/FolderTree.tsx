import type { MouseEvent } from "react";
import { ChevronDown, ChevronRight, CloudDownload, Folder, FolderOpen, Loader2 } from "lucide-react";
import type { Repo } from "@/api/desktop";
import type { Row } from "@/lib/files";
import { cn } from "@/lib/utils";
import { useDirectory } from "./useDirectory";

type TreeProps = {
  repo: Repo;
  current: string;
  expanded: Set<string>;
  onToggle: (path: string) => void;
  onSelect: (path: string) => void;
  onMenu: (row: Row, x: number, y: number) => void;
};

// FolderTree lists folders only; a folder's children are fetched the first
// time it is expanded.
export function FolderTree(props: TreeProps) {
  const { repo, current, onSelect } = props;
  return (
    <nav aria-label="Folders" className="h-full overflow-auto py-1">
      <ul role="tree">
        <li role="treeitem" aria-expanded aria-selected={current === ""}>
          <button
            type="button"
            onClick={() => onSelect("")}
            className={cn("flex w-full items-center gap-1.5 px-2 py-1 text-left text-[12px] font-medium hover:bg-muted", current === "" && "bg-muted")}
          >
            <FolderOpen className="size-3.5 text-accent" aria-hidden />
            <span className="truncate">{repo.name}</span>
          </button>
          <Children {...props} path="" depth={1} />
        </li>
      </ul>
    </nav>
  );
}

function Children({ path, depth, ...props }: TreeProps & { path: string; depth: number }) {
  const dir = useDirectory(props.repo, path);
  const folders = dir.rows.filter((r) => r.isDir);
  if (dir.loading) {
    return (
      <div className="flex items-center gap-1.5 py-1 text-[11px] text-muted-foreground" style={{ paddingLeft: depth * 12 + 8 }}>
        <Loader2 className="size-3 animate-spin" /> Loading…
      </div>
    );
  }
  if (folders.length === 0) return null;
  return (
    <ul role="group">
      {folders.map((f) => (
        <Node key={f.path} row={f} depth={depth} {...props} />
      ))}
    </ul>
  );
}

function Node({ row, depth, ...props }: TreeProps & { row: Row; depth: number }) {
  const { current, expanded, onToggle, onSelect, onMenu } = props;
  const open = expanded.has(row.path);
  const onContextMenu = (e: MouseEvent) => {
    e.preventDefault();
    onMenu(row, e.clientX, e.clientY);
  };
  if (row.remote) {
    return (
      <li role="treeitem" aria-selected={false} aria-disabled>
        <button
          type="button"
          onContextMenu={onContextMenu}
          onClick={(e) => onMenu(row, e.clientX, e.clientY)}
          title={`${row.path} is not in this clone's checkout`}
          className="flex w-full items-center gap-1.5 py-1 pr-2 text-left text-[12px] text-muted-foreground/60 hover:bg-muted"
          style={{ paddingLeft: depth * 12 + 20 }}
        >
          <CloudDownload className="size-3.5 shrink-0" aria-hidden />
          <span className="truncate">{row.name}</span>
        </button>
      </li>
    );
  }
  return (
    <li role="treeitem" aria-expanded={open} aria-selected={current === row.path}>
      <div className={cn("flex items-center hover:bg-muted", current === row.path && "bg-muted font-medium")} style={{ paddingLeft: depth * 12 }}>
        <button type="button" aria-label={open ? `Collapse ${row.name}` : `Expand ${row.name}`} onClick={() => onToggle(row.path)} className="p-1 text-muted-foreground hover:text-foreground">
          {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        </button>
        <button type="button" onClick={() => onSelect(row.path)} onContextMenu={onContextMenu} className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2 text-left text-[12px]">
          <Folder className={cn("size-3.5 shrink-0 text-accent", row.status && "text-success")} aria-hidden />
          <span className="truncate">{row.name}</span>
          {row.childrenChanged && <span className="size-1.5 shrink-0 rounded-full bg-warning" aria-label="Changes inside" />}
        </button>
      </div>
      {open && <Children {...props} path={row.path} depth={depth + 1} />}
    </li>
  );
}
