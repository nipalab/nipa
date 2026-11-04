import { useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, ExternalLink, FolderLock, GitPullRequest, Loader2, Lock, LockOpen, RefreshCw, Search } from "lucide-react";
import { desktop, type FileLock } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { describeLockResult, useLockActions, useLockStatus, type Notify } from "@/hooks/useLocks";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage } from "@/lib/utils";

type Filter = "mine" | "team" | "all";

const isDirectory = (path: string) => !/\.[^/]+$/.test(path);

export function LocksView({ root, notify }: { root: string; notify: Notify }) {
  const status = useLockStatus(root);
  const { lock, unlock, refresh } = useLockActions(root, notify);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [releasing, setReleasing] = useState<FileLock | null>(null);
  const [picking, setPicking] = useState(false);

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
          <table className="w-full text-[12px]" aria-label="File locks">
            <thead className="sticky top-0 bg-sidebar text-left text-[11px] text-muted-foreground">
              <tr>
                <th className="px-4 py-1.5 font-medium">Path</th>
                <th className="px-2 py-1.5 font-medium">Scope</th>
                <th className="px-2 py-1.5 font-medium">Held by</th>
                <th className="px-2 py-1.5 font-medium">Since</th>
                <th className="px-4 py-1.5" />
              </tr>
            </thead>
            <tbody>
              {rows.map((l) => (
                <tr key={l.id || `${l.branch}:${l.path}`} className={cn("border-b border-border/60 hover:bg-muted", !l.inScope && "text-muted-foreground")}>
                  <td className="max-w-0 px-4 py-1.5">
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
                  <td className="whitespace-nowrap px-2 py-1.5">{l.global ? "Mainline" : l.branch}</td>
                  <td className={cn("whitespace-nowrap px-2 py-1.5", l.mine && "font-medium text-accent")}>{l.mine ? "You" : l.heldByName || l.heldBy}</td>
                  <td className="whitespace-nowrap px-2 py-1.5 text-muted-foreground" title={l.acquiredAt}>
                    {timeAgo(l.acquiredAt)}
                  </td>
                  <td className="whitespace-nowrap px-4 py-1.5 text-right">
                    {l.mine && !isDirectory(l.path) && (
                      <Button variant="ghost" size="sm" onClick={() => void open(l)} aria-label={`Open ${l.path}`} title="Open in its default app">
                        <ExternalLink />
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

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">{children}</div>;
}
