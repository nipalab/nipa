import { useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ExternalLink, FolderLock, GitPullRequest, Bell, BellOff, Loader2, Lock, LockOpen, RefreshCw, Search } from "lucide-react";
import { desktop, type FileLock } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { ColumnResizer } from "@/components/ui/resize-handle";
import { useColumnWidths } from "@/hooks/useColumnWidths";
import { usePersistedState } from "@/hooks/usePersistedState";
import { repoSetting } from "@/lib/settings";
import { describeLockResult, useLockActions, useLockStatus, type Notify } from "@/hooks/useLocks";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage } from "@/lib/utils";

type Filter = "mine" | "team" | "all";

const isFilter = (v: unknown): v is Filter => v === "mine" || v === "team" || v === "all";

const isDirectory = (path: string) => !/\.[^/]+$/.test(path);

const COLUMNS = [
  { id: "path", label: "Path", width: 420 },
  { id: "scope", label: "Scope", width: 120 },
  { id: "holder", label: "Held by", width: 160 },
  { id: "since", label: "Since", width: 110 },
];
const COLUMN_DEFAULTS = Object.fromEntries(COLUMNS.map((c) => [c.id, c.width]));
const ACTIONS_WIDTH = 180;

export function LocksView({ root, notify }: { root: string; notify: Notify }) {
  const status = useLockStatus(root);
  const columns = useColumnWidths("locks", COLUMN_DEFAULTS, 64);
  const { lock, unlock, refresh } = useLockActions(root, notify);
  const [filter, setFilter] = usePersistedState<Filter>(repoSetting(root, "locks.filter"), "all", { valid: isFilter });
  const [query, setQuery] = useState("");
  const [releasing, setReleasing] = useState<FileLock | null>(null);
  const [picking, setPicking] = useState(false);
  const watches = useLockWatches(root, notify);

  const data = status.data;
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (data?.locks ?? [])
      .filter((l) => (filter === "mine" ? l.mine : filter === "team" ? !l.mine : true))
      .filter((l) => !q || l.path.toLowerCase().includes(q) || (l.heldByName || l.heldBy).toLowerCase().includes(q))
      .sort((a, b) => Number(b.inScope) - Number(a.inScope) || Number(b.mine) - Number(a.mine) || a.path.localeCompare(b.path));
  }, [data, filter, query]);

  const lockAndEdit = async () => {
    if (!data) return;
    setPicking(true);
    try {
      const path = await desktop.pickFile(root);
      if (!path) return;
      const result = await lock.mutateAsync({ branch: data.branch, paths: [path] });
      if (describeLockResult(result) === null) await desktop.openFile(root, path);
    } catch (err) {
      notify(errorMessage(err));
    } finally {
      setPicking(false);
    }
  };

  const open = (l: FileLock) => desktop.openFile(root, l.path).catch((err: unknown) => notify(`Could not open ${l.path}: ${errorMessage(err)}`));

  if (status.isPending) {
    return (
      <Centered>
        <Loader2 className="size-5 animate-spin text-muted-foreground" />
        <p className="text-sm font-medium">Loading locks…</p>
      </Centered>
    );
  }
  if (!data) {
    return (
      <Centered>
        <AlertTriangle className="size-5 text-danger" />
        <p className="text-sm font-medium">Could not load locks</p>
        <p className="max-w-sm text-xs text-muted-foreground">{errorMessage(status.error)}</p>
        <Button size="sm" onClick={() => void refresh()}>
          <RefreshCw />
          Try again
        </Button>
      </Centered>
    );
  }

  const mine = data.locks.filter((l) => l.mine).length;
  const scope = data.defaultBranch ? `mainline (${data.branch})` : `branch ${data.branch}`;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-[12px]">
        <p className="mr-auto text-muted-foreground">
          Pushes to <span className="font-medium text-foreground">{data.branch || "this clone"}</span> check locks on the {scope}.
        </p>
        <Button size="sm" onClick={() => void refresh()} title="Refresh locks" aria-label="Refresh locks" disabled={status.isFetching}>
          <RefreshCw className={cn(status.isFetching && "animate-spin")} />
        </Button>
        <Button size="sm" variant="primary" onClick={() => void lockAndEdit()} disabled={picking || lock.isPending}>
          <Lock />
          Lock &amp; edit a file…
        </Button>
      </div>
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2">
        <div className="flex rounded-md border border-border p-0.5 text-[12px]" role="group" aria-label="Show">
          {(
            [
              ["all", `All (${data.locks.length})`],
              ["mine", `Mine (${mine})`],
              ["team", `Team (${data.locks.length - mine})`],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              aria-pressed={filter === id}
              onClick={() => setFilter(id)}
              className={cn("rounded px-2 py-0.5 text-muted-foreground hover:text-foreground", filter === id && "bg-muted text-foreground")}
            >
              {label}
            </button>
          ))}
        </div>
        <label className="ml-auto flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1 text-[12px]">
          <Search className="size-3.5 text-muted-foreground" />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Filter by path or person" aria-label="Filter locks" className="w-56 bg-transparent outline-none" />
        </label>
      </div>
      {rows.length === 0 ? (
        <Centered>
          <LockOpen className="size-6 text-muted-foreground" />
          <p className="text-sm font-medium">{data.locks.length === 0 ? "Nothing is locked" : "No locks match"}</p>
          <p className="max-w-sm text-xs text-muted-foreground">
            {data.locks.length === 0 ? "Lock a binary file before you edit it so nobody else changes it at the same time." : "Try another filter."}
          </p>
        </Centered>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <table
            className="w-full table-fixed text-[12px]"
            style={{ minWidth: COLUMNS.reduce((sum, c) => sum + columns.width(c.id), ACTIONS_WIDTH) }}
            aria-label="File locks"
          >
            <colgroup>
              {COLUMNS.map((c) => (
                <col key={c.id} style={{ width: columns.width(c.id) }} />
              ))}
              <col />
            </colgroup>
            <thead className="sticky top-0 z-10 bg-sidebar text-left text-[11px] text-muted-foreground">
              <tr>
                {COLUMNS.map((c, i) => (
                  <th key={c.id} aria-label={c.label} className={cn("relative truncate py-1.5 font-medium", i === 0 ? "px-4" : "px-2")}>
                    {c.label}
                    <ColumnResizer sizes={columns} id={c.id} label={c.label} />
                  </th>
                ))}
                <th className="px-4 py-1.5" />
              </tr>
            </thead>
            <tbody>
              {rows.map((l) => (
                <tr key={l.id || `${l.branch}:${l.path}`} className={cn("border-b border-border/60 hover:bg-muted", !l.inScope && "text-muted-foreground")}>
                  <td className="px-4 py-1.5">
                    <span className="flex items-center gap-1.5" title={l.path}>
                      {isDirectory(l.path) ? <FolderLock className="size-3.5 shrink-0" aria-label="Folder lock" /> : <Lock className="size-3.5 shrink-0" />}
                      <span className="truncate font-mono text-[11px]">{l.path}</span>
                      {l.mergeRequest > 0 && (
                        <Badge title={`Held for merge request !${l.mergeRequest}`}>
                          <GitPullRequest />!{l.mergeRequest}
                        </Badge>
                      )}
                    </span>
                  </td>
                  <td className="truncate px-2 py-1.5">{l.global ? "Mainline" : l.branch}</td>
                  <td className={cn("truncate px-2 py-1.5", l.mine && "font-medium text-accent")}>{l.mine ? "You" : l.heldByName || l.heldBy}</td>
                  <td className="truncate px-2 py-1.5 text-muted-foreground" title={l.acquiredAt}>
                    {timeAgo(l.acquiredAt)}
                  </td>
                  <td className="whitespace-nowrap px-4 py-1.5 text-right">
                    {l.mine && !isDirectory(l.path) && (
                      <Button variant="ghost" size="sm" onClick={() => void open(l)} aria-label={`Open ${l.path}`} title="Open in its default app">
                        <ExternalLink />
                      </Button>
                    )}
                    {!l.mine && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={watches.toggle.isPending}
                        aria-pressed={watches.has(l)}
                        aria-label={watches.has(l) ? `Stop watching ${l.path}` : `Notify me when ${l.path} is free`}
                        title={watches.has(l) ? "Stop watching this lock" : "Notify me when free"}
                        onClick={() => watches.toggle.mutate(l)}
                      >
                        {watches.has(l) ? <BellOff /> : <Bell />}
                      </Button>
                    )}
                    {(l.mine || data.canReleaseAny) && (
                      <Button size="sm" disabled={unlock.isPending} onClick={() => (l.mine ? unlock.mutate(l) : setReleasing(l))} aria-label={`Release ${l.path}`}>
                        <LockOpen />
                        Release
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {releasing && (
        <ConfirmDialog
          title="Release someone else's lock?"
          message={`${releasing.heldByName || releasing.heldBy} holds ${releasing.path}. If they are still editing it, their work can no longer be pushed without merging by hand.`}
          confirmLabel="Release"
          onCancel={() => setReleasing(null)}
          onConfirm={() => {
            unlock.mutate(releasing);
            setReleasing(null);
          }}
        />
      )}
    </div>
  );
}

function useLockWatches(root: string, notify: Notify) {
  const queryClient = useQueryClient();
  const key = ["lock-watches", root];
  const list = useQuery({ queryKey: key, queryFn: () => desktop.lockWatches(root), retry: false });
  const watches = list.data ?? [];
  const has = (l: { branch: string; path: string }) => watches.some((w) => w.branch === l.branch && w.path === l.path);
  const toggle = useMutation({
    mutationFn: async (l: { branch: string; path: string }) => {
      const watching = has(l);
      await (watching ? desktop.unwatchLock(root, l.branch, l.path) : desktop.watchLock(root, l.branch, l.path));
      return { path: l.path, watching: !watching };
    },
    onSuccess: ({ path, watching }) => {
      void queryClient.invalidateQueries({ queryKey: key });
      notify(watching ? `You will be told when ${path} is free` : `Stopped watching ${path}`, "success");
    },
    onError: (err) => notify(errorMessage(err)),
  });
  return { has, toggle };
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">{children}</div>;
}
