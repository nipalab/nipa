import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ExternalLink, FolderLock, GitPullRequest, Bell, BellOff, HandHelping, Loader2, Lock, LockOpen, RefreshCw, Search } from "lucide-react";
import { desktop, type FileLock } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { Dialog } from "@/components/ui/dialog";
import { ColumnResizer } from "@/components/ui/resize-handle";
import { useColumnWidths } from "@/hooks/useColumnWidths";
import { usePersistedState } from "@/hooks/usePersistedState";
import { repoSetting } from "@/lib/settings";
import { describeLockResult, useLockActions, useLockStatus, type Notify } from "@/hooks/useLocks";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage } from "@/lib/utils";
import { Trans, useTranslation } from "react-i18next";
import { translate } from "@/i18n";

type Filter = "mine" | "team" | "all";

const isFilter = (v: unknown): v is Filter => v === "mine" || v === "team" || v === "all";

const isDirectory = (path: string) => !/\.[^/]+$/.test(path);

const COLUMNS = [
  { id: "path", label: translate("locksView.path"), width: 420 },
  { id: "scope", label: translate("locksView.scope"), width: 120 },
  { id: "holder", label: translate("locksView.heldBy"), width: 160 },
  { id: "since", label: translate("locksView.since"), width: 110 },
];
const COLUMN_DEFAULTS = Object.fromEntries(COLUMNS.map((c) => [c.id, c.width]));
const ACTIONS_WIDTH = 180;

export function LocksView({ root, notify }: { root: string; notify: Notify }) {
  const { t } = useTranslation();
  const status = useLockStatus(root);
  const columns = useColumnWidths("locks", COLUMN_DEFAULTS, 64);
  const { lock, unlock, refresh } = useLockActions(root, notify);
  const [filter, setFilter] = usePersistedState<Filter>(repoSetting(root, "locks.filter"), "all", { valid: isFilter });
  const [query, setQuery] = useState("");
  const [releasing, setReleasing] = useState<FileLock | null>(null);
  const [picking, setPicking] = useState(false);
  const watches = useLockWatches(root, notify);
  const [asking, setAsking] = useState<FileLock | null>(null);
  const ask = useMutation({
    mutationFn: ({ lock: l, message }: { lock: FileLock; message: string }) => desktop.requestRelease(root, l.id, message),
    onSuccess: (_, { lock: l }) => {
      setAsking(null);
      void refresh();
      notify(translate("locksView.askedToRelease", { heldBy: l.heldByName || l.heldBy, path: l.path }), "success");
    },
    onError: (err) => notify(errorMessage(err)),
  });

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

  const open = (l: FileLock) => desktop.openFile(root, l.path).catch((err: unknown) => notify(translate("locksView.couldNotOpen", { path: l.path, value: errorMessage(err) })));

  if (status.isPending) {
    return (
      <Centered>
        <Loader2 className="size-5 animate-spin text-muted-foreground" />
        <p className="text-sm font-medium">{t("locksView.loadingLocks")}</p>
      </Centered>
    );
  }
  if (!data) {
    return (
      <Centered>
        <AlertTriangle className="size-5 text-danger" />
        <p className="text-sm font-medium">{t("locksView.couldNotLoadLocks")}</p>
        <p className="max-w-sm text-xs text-muted-foreground">{errorMessage(status.error)}</p>
        <Button size="sm" onClick={() => void refresh()}>
          <RefreshCw />
          {t("locksView.tryAgain")}
        </Button>
      </Centered>
    );
  }

  const mine = data.locks.filter((l) => l.mine).length;
  const scope = data.defaultBranch ? translate("locksView.mainline", { branch: data.branch }) : translate("locksView.branch", { branch: data.branch });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-[12px]">
        <p className="mr-auto text-muted-foreground">
          <Trans i18nKey="locksView.scopeHint" values={{ branch: data.branch || t("locksView.thisClone"), scope }} components={[<span key="b" className="font-medium text-foreground">{null}</span>]} />
        </p>
        <Button size="sm" onClick={() => void refresh()} title={t("locksView.refreshLocks")} aria-label={t("locksView.refreshLocks")} disabled={status.isFetching}>
          <RefreshCw className={cn(status.isFetching && "animate-spin")} />
        </Button>
        <Button size="sm" variant="primary" onClick={() => void lockAndEdit()} disabled={picking || lock.isPending}>
          <Lock />
          {t("locksView.lockAndEdit")}
        </Button>
      </div>
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2">
        <div className="flex rounded-md border border-border p-0.5 text-[12px]" role="group" aria-label={t("locksView.show")}>
          {(
            [
              ["all", translate("locksView.all", { count: data.locks.length })],
              ["mine", translate("locksView.mine", { mine })],
              ["team", translate("locksView.team", { mine: data.locks.length - mine })],
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
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("locksView.filterByPathOrPerson")} aria-label={t("locksView.filterLocks")} className="w-56 bg-transparent outline-none" />
        </label>
      </div>
      {rows.length === 0 ? (
        <Centered>
          <LockOpen className="size-6 text-muted-foreground" />
          <p className="text-sm font-medium">{data.locks.length === 0 ? translate("locksView.nothingIsLocked") : translate("locksView.noLocksMatch")}</p>
          <p className="max-w-sm text-xs text-muted-foreground">
            {data.locks.length === 0 ? translate("locksView.lockAnArtAudioOr") : translate("locksView.tryAnotherFilter")}
          </p>
        </Centered>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <table
            className="w-full table-fixed text-[12px]"
            style={{ minWidth: COLUMNS.reduce((sum, c) => sum + columns.width(c.id), ACTIONS_WIDTH) }}
            aria-label={t("locksView.fileLocks")}
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
                      {isDirectory(l.path) ? <FolderLock className="size-3.5 shrink-0" aria-label={t("locksView.folderLock")} /> : <Lock className="size-3.5 shrink-0" />}
                      <span className="truncate font-mono text-[11px]">{l.path}</span>
                      {l.mine && l.requests && l.requests.length > 0 && (
                        <Badge title={l.requests.map((r) => `${r.requesterName || r.requesterId}${r.message ? `: ${r.message}` : ""}`).join("\n")}>
                          <HandHelping />
                          {l.requests.length === 1 ? translate("locksView.1Request") : translate("locksView.requests", { count: l.requests.length })}
                        </Badge>
                      )}
                      {l.mergeRequest > 0 && (
                        <Badge title={translate("locksView.heldForMergeRequest", { mergeRequest: l.mergeRequest })}>
                          <GitPullRequest />!{l.mergeRequest}
                        </Badge>
                      )}
                    </span>
                  </td>
                  <td className="truncate px-2 py-1.5">{l.global ? translate("locksView.mainline2") : l.branch}</td>
                  <td className={cn("truncate px-2 py-1.5", l.mine && "font-medium text-accent")}>{l.mine ? translate("locksView.you") : l.heldByName || l.heldBy}</td>
                  <td className="truncate px-2 py-1.5 text-muted-foreground" title={l.acquiredAt}>
                    {timeAgo(l.acquiredAt)}
                  </td>
                  <td className="whitespace-nowrap px-4 py-1.5 text-right">
                    {l.mine && !isDirectory(l.path) && (
                      <Button variant="ghost" size="sm" onClick={() => void open(l)} aria-label={translate("locksView.open", { path: l.path })} title={t("locksView.openInItsDefaultApp")}>
                        <ExternalLink />
                      </Button>
                    )}
                    {!l.mine && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={ask.isPending}
                        aria-label={translate("locksView.askToRelease2", { path: l.path })}
                        title={askedBefore(l, data.userId) ? translate("locksView.youAlreadyAskedAskAgain") : translate("locksView.askTheHolderToRelease")}
                        onClick={() => setAsking(l)}
                      >
                        <HandHelping />
                      </Button>
                    )}
                    {!l.mine && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={watches.toggle.isPending}
                        aria-pressed={watches.has(l)}
                        aria-label={watches.has(l) ? translate("locksView.stopWatching", { path: l.path }) : translate("locksView.notifyMeWhenIsFree", { path: l.path })}
                        title={watches.has(l) ? translate("locksView.stopWatchingThisLock") : translate("locksView.notifyMeWhenFree")}
                        onClick={() => watches.toggle.mutate(l)}
                      >
                        {watches.has(l) ? <BellOff /> : <Bell />}
                      </Button>
                    )}
                    {(l.mine || data.canReleaseAny) && (
                      <Button size="sm" disabled={unlock.isPending} onClick={() => (l.mine ? unlock.mutate(l) : setReleasing(l))} aria-label={translate("locksView.release2", { path: l.path })}>
                        <LockOpen />
                        {t("locksView.release")}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {asking && (
        <AskDialog
          lock={asking}
          busy={ask.isPending}
          onCancel={() => setAsking(null)}
          onConfirm={(message) => ask.mutate({ lock: asking, message })}
        />
      )}
      {releasing && (
        <ConfirmDialog
          title={t("locksView.releaseSomeoneElseSLock")}
          message={translate("locksView.holdsIfThey", { heldBy: releasing.heldByName || releasing.heldBy, path: releasing.path })}
          confirmLabel={translate("locksView.release")}
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

const askedBefore = (l: FileLock, userId: string) => (l.requests ?? []).some((r) => r.requesterId === userId);

function AskDialog({ lock, busy, onConfirm, onCancel }: { lock: FileLock; busy: boolean; onConfirm: (message: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [message, setMessage] = useState("");
  const field = useRef<HTMLTextAreaElement>(null);
  useEffect(() => field.current?.focus(), []);
  const holder = lock.heldByName || lock.heldBy;
  return (
    <Dialog title={t("locksView.askToRelease")} onClose={onCancel}>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          onConfirm(message.trim());
        }}
      >
        <p className="break-words text-xs text-muted-foreground">
          <Trans i18nKey="locksView.holderNotice" values={{ holder, path: lock.path }} components={[<span key="p" className="font-mono">{null}</span>]} />
        </p>
        <label className="block space-y-1 text-[12px]">
          <span className="text-muted-foreground">{t("locksView.messageOptional")}</span>
          <textarea
            ref={field}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            maxLength={500}
            rows={3}
            className="w-full resize-none rounded-md border border-border bg-background px-2 py-1.5 text-[12px] outline-none focus:border-accent"
          />
        </label>
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onCancel}>
            {t("locksView.cancel")}
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={busy}>
            {t("locksView.sendRequest")}
          </Button>
        </div>
      </form>
    </Dialog>
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
      notify(watching ? translate("locksView.youWillBeToldWhen", { path }) : translate("locksView.stoppedWatching", { path }), "success");
    },
    onError: (err) => notify(errorMessage(err)),
  });
  return { has, toggle };
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">{children}</div>;
}
