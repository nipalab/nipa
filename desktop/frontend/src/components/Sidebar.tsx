import { AlertTriangle, ArrowDown, CloudDownload, Eye, FolderGit2, FolderOpen, GitBranch, Settings, WifiOff } from "lucide-react";
import type { DaemonInfo, Repo, RepoActivity } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/Logo";
import { ThemeToggle } from "@/components/ThemeToggle";
import { cn, shortVersion } from "@/lib/utils";

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
};

export function Sidebar({ repos, selected, daemon, adding, onSelect, onAdd, onClone, onSettings, activity }: SidebarProps) {
  return (
    <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-sidebar">
      <div className="drag-region flex h-12 items-center gap-2 px-4 pt-1">
        <Logo className="size-5" />
        <span className="text-sm font-semibold tracking-tight">Nipa</span>
      </div>

      <div className="flex items-center justify-between px-4 pb-1 pt-3">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Repositories</span>
        <span className="flex">
          <Button variant="ghost" size="icon" onClick={onClone} aria-label="Clone a repository" title="Clone a repository">
            <CloudDownload />
          </Button>
          <Button variant="ghost" size="icon" onClick={onAdd} disabled={adding} aria-label="Open a clone" title="Open a clone">
            <FolderOpen />
          </Button>
        </span>
      </div>

      <nav aria-label="Repositories" className="flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
        {repos.length === 0 && <p className="px-2 py-3 text-xs text-muted-foreground">No clones yet.</p>}
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
              <AlertTriangle className="size-4 shrink-0 text-warning" aria-label="Unavailable" />
            ) : (
              <FolderGit2 className={cn("size-4 shrink-0 text-muted-foreground", repo.root === selected && "text-accent")} />
            )}
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{repo.name}</span>
              <span className="flex items-center gap-1 truncate text-[11px] text-muted-foreground">
                {repo.error ? (
                  "Unavailable"
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
          title={`nipa serve ${daemon.version} · pid ${daemon.pid} · port ${daemon.port}`}
        >
          <span className="size-1.5 shrink-0 rounded-full bg-success" />
          <span className="truncate">Daemon {shortVersion(daemon.version)}</span>
        </span>
        <span className="flex items-center gap-1">
          <ThemeToggle />
          <Button variant="ghost" size="icon" onClick={onSettings} aria-label="Settings" title="Settings">
            <Settings />
          </Button>
        </span>
      </div>
    </aside>
  );
}

function ActivityBadges({ activity }: { activity?: RepoActivity }) {
  if (!activity) return null;
  const incoming = activity.incoming >= 100 ? "100+" : String(activity.incoming);
  return (
    <span className="flex shrink-0 items-center gap-1 text-[10px]">
      {activity.error && <WifiOff className="size-3 text-warning" aria-label={`Server unreachable: ${activity.error}`} />}
      {activity.incoming > 0 && (
        <span className="flex items-center rounded-full bg-accent-soft px-1.5 text-accent" title={`${incoming} new commit${activity.incoming === 1 ? "" : "s"} to get`}>
          <ArrowDown className="size-3" />
          {incoming}
        </span>
      )}
      {activity.reviewRequests > 0 && (
        <span className="flex items-center gap-0.5 rounded-full bg-warning/20 px-1.5 text-warning" title={`${activity.reviewRequests} merge request${activity.reviewRequests === 1 ? "" : "s"} waiting for your review`}>
          <Eye className="size-3" />
          {activity.reviewRequests}
        </span>
      )}
    </span>
  );
}
