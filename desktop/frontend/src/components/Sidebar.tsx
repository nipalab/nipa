import { AlertTriangle, ArrowDown, CloudDownload, Construction, Eye, FolderGit2, FolderOpen, GitBranch, PanelLeftClose, Settings, WifiOff } from "lucide-react";
import type { DaemonInfo, Repo, RepoActivity } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/Logo";
import { ThemeToggle } from "@/components/ThemeToggle";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useResizableWidth } from "@/hooks/useResizableWidth";
import { isMac } from "@/lib/shortcuts";
import { cn, shortVersion } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

type SidebarProps = {
  repos: Repo[];
  selected: string | null;
  daemon: DaemonInfo;
  adding: boolean;
  onSelect: (root: string) => void;
  onAdd: () => void;
  onClone: () => void;
  onSettings: () => void;
  activity: RepoActivity[];
  onHide?: () => void;
};

export function Sidebar({ repos, selected, daemon, adding, onSelect, onAdd, onClone, onSettings, activity, onHide }: SidebarProps) {
  const { t } = useTranslation();
  const size = useResizableWidth("sidebar", { initial: 256, min: 180, max: 480 });
  return (
    <aside className="relative flex shrink-0 flex-col border-r border-border bg-sidebar" ref={size.ref} style={{ width: size.width }}>
      {isMac() && <div data-testid="mac-titlebar" className="drag-region h-7 shrink-0" />}
      <div className="drag-region flex h-12 items-center gap-2 pl-4 pr-2 pt-1">
        <Logo className="size-5" />
        <span className="flex-1 text-sm font-semibold tracking-tight">{t("sidebar.nipa")}</span>
        {onHide && (
          <Button variant="ghost" size="icon" onClick={onHide} aria-label={t("sidebar.hideSidebar")} title={t("sidebar.hideSidebarCtrlB")}>
            <PanelLeftClose />
          </Button>
        )}
      </div>

      <div className="flex items-center justify-between px-4 pb-1 pt-3">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t("sidebar.repositories")}</span>
        <span className="flex">
          <Button variant="ghost" size="icon" onClick={onClone} aria-label={t("sidebar.cloneARepository")} title={t("sidebar.cloneARepository")}>
            <CloudDownload />
          </Button>
          <Button variant="ghost" size="icon" onClick={onAdd} disabled={adding} aria-label={t("sidebar.openAClone")} title={t("sidebar.openAClone")}>
            <FolderOpen />
          </Button>
        </span>
      </div>

      <nav aria-label={t("sidebar.repositories")} className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
        {repos.length === 0 && <p className="px-2 py-3 text-xs text-muted-foreground">{t("sidebar.noClonesYet")}</p>}
        {repos.map((repo) => (
          <button
            key={repo.root}
            type="button"
            onClick={() => onSelect(repo.root)}
            aria-current={repo.root === selected ? "page" : undefined}
            title={repo.root}
            className={cn(
              "group flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-muted",
              repo.root === selected && "bg-accent-soft hover:bg-accent-soft",
            )}
          >
            {repo.error ? (
              <AlertTriangle className="size-4 shrink-0 text-warning" aria-label={t("sidebar.unavailable")} />
            ) : (
              <FolderGit2 className={cn("size-4 shrink-0 text-muted-foreground", repo.root === selected && "text-accent")} />
            )}
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{repo.name}</span>
              <span className="flex items-center gap-1 truncate text-[11px] text-muted-foreground">
                {repo.error ? (
                  translate("sidebar.unavailable")
                ) : (
                  <>
                    <GitBranch className="size-3" />
                    {repo.branch}
                  </>
                )}
              </span>
            </span>
            <ActivityBadges activity={repo.error ? undefined : activity.find((a) => a.root === repo.root)} />
          </button>
        ))}
      </nav>

      <div className="flex items-center justify-between border-t border-border px-4 py-2.5">
        <span
          className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground"
          title={translate("sidebar.nipaServePid", { version: daemon.version, pid: daemon.pid, port: daemon.port })}
        >
          <span className="size-1.5 shrink-0 rounded-full bg-success" />
          <span className="truncate">{t("sidebar.daemon", { version: shortVersion(daemon.version) })}</span>
        </span>
        <span className="flex items-center gap-1">
          <ThemeToggle />
          <Button variant="ghost" size="icon" onClick={onSettings} aria-label={t("sidebar.settings")} title={t("sidebar.settings")}>
            <Settings />
          </Button>
        </span>
      </div>
      <PaneResizer size={size} label={t("sidebar.resizeSidebar")} />
    </aside>
  );
}

function ActivityBadges({ activity }: { activity?: RepoActivity }) {
  if (!activity) return null;
  const incoming = activity.incoming >= 100 ? "100+" : String(activity.incoming);
  return (
    <span className="flex shrink-0 items-center gap-1 text-[10px]">
      {activity.maintenance?.some((w) => w.active) && <Construction className="size-3 text-warning" aria-label={translate("sidebar.inMaintenance")} />}
      {activity.error && <WifiOff className="size-3 text-warning" aria-label={translate("sidebar.serverUnreachable", { error: activity.error })} />}
      {activity.incoming > 0 && (
        <span className="flex items-center rounded-full bg-accent-soft px-1.5 text-accent" title={translate("sidebar.newCommits", { count: activity.incoming, incoming })}>
          <ArrowDown className="size-3" />
          {incoming}
        </span>
      )}
      {activity.reviewRequests > 0 && (
        <span className="flex items-center gap-0.5 rounded-full bg-warning/20 px-1.5 text-warning" title={translate("sidebar.reviewsWaiting", { count: activity.reviewRequests })}>
          <Eye className="size-3" />
          {activity.reviewRequests}
        </span>
      )}
    </span>
  );
}
