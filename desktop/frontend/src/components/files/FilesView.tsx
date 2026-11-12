import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ChevronRight, CloudDownload, Copy, ExternalLink, FolderOpen, FolderSearch, History as HistoryIcon, LayoutGrid, Link2, List, Loader2, Lock, LockOpen, RefreshCw } from "lucide-react";
import { desktop, type Commit, type Repo } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useLockActions, useLockStatus, type Notify } from "@/hooks/useLocks";
import { useResizableWidth } from "@/hooks/useResizableWidth";
import { ancestors, dirsToRefresh, lockFor, nextSort, parentOf, readViewMode, sortEntries, writeViewMode, type Row, type Sort, type ViewMode } from "@/lib/files";
import { cn, errorMessage } from "@/lib/utils";
import { FileDetail } from "./FileDetail";
import { FileMenu, type FileAction } from "./FileMenu";
import { FileGrid, FileList } from "./FileTable";
import { FolderTree } from "./FolderTree";
import { filesKey, useDirectory } from "./useDirectory";

type FilesViewProps = {
  repo: Repo;
  busy: boolean;
  notify: Notify;
  open?: { path: string; seq: number };
  onHistory: (path: string, commit?: string) => void;
  onRestore: (path: string, commit: Commit) => void;
  onAddSparse: (path: string) => void;
};

type Menu = { row: Row; x: number; y: number } | null;

export function FilesView({ repo, busy, notify, open, onHistory, onRestore, onAddSparse }: FilesViewProps) {
  const root = repo.root;
  const queryClient = useQueryClient();
  const treeSize = useResizableWidth("files-tree", { initial: 220, min: 140, max: 480 });
  const listSize = useResizableWidth("files-list", { initial: 460, min: 260, max: 1200 });
  const [dir, setDir] = useState("");
  const [selected, setSelected] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [mode, setModeState] = useState<ViewMode>(readViewMode);
  const [sort, setSort] = useState<Sort>({ key: "name", desc: false });
  const [menu, setMenu] = useState<Menu>(null);
  const pendingOpen = useRef("");

  const lockStatus = useLockStatus(root);
  const locks = useMemo(() => lockStatus.data?.locks ?? [], [lockStatus.data]);
  const lockActions = useLockActions(root, notify);
  const listing = useDirectory(repo, dir);
  const rows = useMemo(() => sortEntries(listing.rows, sort, locks), [listing.rows, sort, locks]);
  const row = rows.find((r) => r.path === selected);

  const reveal = (path: string) => {
    setDir(parentOf(path));
    setSelected(path);
    setExpanded((prev) => new Set([...prev, ...ancestors(path).filter(Boolean)]));
  };

  useEffect(() => {
    if (!open?.path) return;
    pendingOpen.current = open.path;
    reveal(open.path);
  }, [open?.path, open?.seq]);

  useEffect(() => {
    const target = pendingOpen.current;
    if (!target) return;
    const hit = listing.rows.find((r) => r.path === target);
    if (!hit) return;
    pendingOpen.current = "";
    if (hit.isDir && !hit.remote) browse(hit.path);
  }, [listing.rows, open?.seq]);

  useEffect(
    () =>
      desktop.onRepoChanged((change) => {
        if (change.root !== root) return;
        const paths = change.paths ?? [];
        if (paths.length === 0) {
          for (const key of ["files", "file", "diff"]) void queryClient.invalidateQueries({ queryKey: [key, root] });
          return;
        }
        for (const d of dirsToRefresh(paths)) void queryClient.invalidateQueries({ queryKey: filesKey(root, d), exact: true });
        for (const p of paths) {
          void queryClient.invalidateQueries({ queryKey: ["file", root, p] });
          void queryClient.invalidateQueries({ queryKey: ["diff", root, p] });
        }
      }),
    [root, queryClient],
  );

  const setMode = (next: ViewMode) => {
    setModeState(next);
    writeViewMode(next);
  };

  const browse = (path: string) => {
    setDir(path);
    setSelected("");
    setExpanded((prev) => new Set([...prev, ...ancestors(path).filter(Boolean), path].filter(Boolean)));
  };

  const toggle = (path: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const copy = (text: string, done: string) => {
    if (!navigator.clipboard) {
      notify("The clipboard is not available");
      return;
    }
    navigator.clipboard.writeText(text).then(
      () => notify(done, "success"),
      (err: unknown) => notify(`Could not copy: ${errorMessage(err)}`),
    );
  };

  const openRow = (r: Row) => {
    if (r.remote) onAddSparse(r.path);
    else if (r.isDir) browse(r.path);
    else desktop.openFile(root, r.path).catch((err: unknown) => notify(`Could not open ${r.path}: ${errorMessage(err)}`));
  };

  const actionsFor = (r: Row): FileAction[] => {
    if (r.remote) {
      return [{ id: "download", label: "Download this folder", Icon: CloudDownload, run: () => onAddSparse(r.path), disabled: busy, title: `Add ${r.path} to this clone's checkout` }];
    }
    const gone = r.status === "missing" || r.status === "deleted";
    const actions: FileAction[] = [
      r.isDir
        ? { id: "browse", label: "Open folder", Icon: FolderOpen, run: () => browse(r.path) }
        : { id: "open", label: "Open", Icon: ExternalLink, run: () => openRow(r), disabled: gone },
      {
        id: "reveal",
        label: "Reveal in file manager",
        Icon: FolderSearch,
        disabled: gone,
        run: () => desktop.revealFile(root, r.path).catch((err: unknown) => notify(`Could not show ${r.path}: ${errorMessage(err)}`)),
      },
    ];
    if (!r.isDir) actions.push(lockAction(r));
    actions.push(
      { id: "history", label: "History", Icon: HistoryIcon, run: () => onHistory(r.path), disabled: !r.tracked },
      { id: "copy-path", label: "Copy path", Icon: Copy, run: () => copy(r.path, "Path copied") },
      {
        id: "copy-link",
        label: "Copy nipa:// link",
        Icon: Link2,
        run: () =>
          desktop.fileLink(repo.url, r.path).then(
            (link) => copy(link, "Link copied"),
            (err: unknown) => notify(`Could not make a link: ${errorMessage(err)}`),
          ),
      },
    );
    return actions;
  };

  const lockAction = (r: Row): FileAction => {
    const lock = lockFor(r.path, locks);
    if (!lock) {
      return {
        id: "lock",
        label: "Lock",
        Icon: Lock,
        disabled: !lockStatus.data || lockActions.lock.isPending,
        run: () => lockStatus.data && lockActions.lock.mutate({ branch: lockStatus.data.branch, paths: [r.path] }),
      };
    }
    if (lock.mine) {
      return {
        id: "unlock",
        label: "Unlock",
        Icon: LockOpen,
        disabled: lockActions.unlock.isPending,
        title: lock.path === r.path ? "Release your lock" : `Releases your lock on ${lock.path}`,
        run: () => lockActions.unlock.mutate(lock),
      };
    }
    return { id: "locked", label: `Locked by ${lock.heldByName || lock.heldBy}`, Icon: Lock, disabled: true, run: () => {} };
  };

  const crumbs = dir ? dir.split("/") : [];

  return (
    <div className="flex h-full min-h-0">
      <aside className="relative shrink-0 border-r border-border" ref={treeSize.ref} style={{ width: treeSize.width }}>
        <FolderTree repo={repo} current={dir} expanded={expanded} onToggle={toggle} onSelect={browse} onMenu={(r, x, y) => setMenu({ row: r, x, y })} />
        <PaneResizer size={treeSize} label="Resize folder tree" />
      </aside>
      <section className="relative flex shrink-0 flex-col border-r border-border" ref={listSize.ref} style={{ width: listSize.width }}>
        <div className="flex h-9 shrink-0 items-center gap-1 border-b border-border px-2">
          <nav aria-label="Folder path" className="flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden text-[12px]">
            <Crumb onClick={() => browse("")} current={dir === ""}>
              {repo.name}
            </Crumb>
            {crumbs.map((name, i) => {
              const path = crumbs.slice(0, i + 1).join("/");
              return (
                <span key={path} className="flex min-w-0 items-center gap-0.5">
                  <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
                  <Crumb onClick={() => browse(path)} current={path === dir}>
                    {name}
                  </Crumb>
                </span>
              );
            })}
          </nav>
          <span className="shrink-0 text-[11px] text-muted-foreground">{rows.length} items</span>
          <div className="ml-1 flex rounded-md border border-border p-0.5" role="group" aria-label="View">
            <ModeButton active={mode === "list"} label="List" onClick={() => setMode("list")} Icon={List} />
            <ModeButton active={mode === "grid"} label="Thumbnails" onClick={() => setMode("grid")} Icon={LayoutGrid} />
          </div>
        </div>
        <div className="min-h-0 flex-1">
          {listing.loading ? (
            <Centered>
              <Loader2 className="size-4 animate-spin" /> Loading files…
            </Centered>
          ) : listing.error ? (
            <Centered>
              <AlertTriangle className="size-4 text-danger" />
              <span>{errorMessage(listing.error)}</span>
              <Button size="sm" onClick={() => void listing.refetch()}>
                <RefreshCw />
                Try again
              </Button>
            </Centered>
          ) : rows.length === 0 ? (
            <Centered>This folder is empty.</Centered>
          ) : mode === "grid" ? (
            <FileGrid root={root} rows={rows} locks={locks} selected={selected} onSelect={(r) => setSelected(r.path)} onOpen={openRow} onMenu={(r, x, y) => setMenu({ row: r, x, y })} />
          ) : (
            <FileList
              root={root}
              rows={rows}
              locks={locks}
              selected={selected}
              onSelect={(r) => setSelected(r.path)}
              onOpen={openRow}
              onMenu={(r, x, y) => setMenu({ row: r, x, y })}
              sort={sort}
              onSort={(key) => setSort((s) => nextSort(s, key))}
            />
          )}
        </div>
        <PaneResizer size={listSize} label="Resize file list" />
      </section>
      <section className="min-w-0 flex-1" aria-label="File details">
        {row && !row.isDir && !row.remote ? (
          <FileDetail
            key={row.path}
            root={root}
            branch={repo.branch}
            row={row}
            locks={locks}
            actions={actionsFor(row)}
            busy={busy}
            onRestore={(commit) => onRestore(row.path, commit)}
            onShowCommit={(id) => onHistory(row.path, id)}
          />
        ) : (
          <Centered>{row?.remote ? "This folder is not in your checkout." : "Select a file to see it here."}</Centered>
        )}
      </section>
      {menu && <FileMenu x={menu.x} y={menu.y} actions={actionsFor(menu.row)} onClose={() => setMenu(null)} />}
    </div>
  );
}

function Crumb({ children, onClick, current }: { children: ReactNode; onClick: () => void; current: boolean }) {
  return (
    <button type="button" onClick={onClick} aria-current={current ? "location" : undefined} className={cn("truncate rounded px-1 hover:bg-muted", current ? "font-medium" : "text-muted-foreground")}>
      {children}
    </button>
  );
}

function ModeButton({ active, label, onClick, Icon }: { active: boolean; label: string; onClick: () => void; Icon: typeof List }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      title={label}
      onClick={onClick}
      className={cn("flex size-6 items-center justify-center rounded text-muted-foreground hover:text-foreground", active && "bg-muted text-foreground")}
    >
      <Icon className="size-3.5" />
      <span className="sr-only">{label}</span>
    </button>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
