import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, GitCommit, GitMerge, GitPullRequest, Loader2, Lock, RefreshCw } from "lucide-react";
import { desktop, type FeedEvent } from "@/api/desktop";
import { Avatar } from "@/components/people/Avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { usePersistedState } from "@/hooks/usePersistedState";
import { feedGroups, feedPeople, feedRanges, feedSince, feedText, filterFeed, groupByDay, isFeedRange, type FeedGroup, type FeedRange } from "@/lib/feed";
import { repoSetting } from "@/lib/settings";
import { shortId } from "@/lib/history";
import { cn, errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate , locale } from "@/i18n";

export const activityKey = (root: string, range: FeedRange) => ["activity", root, range] as const;

type Props = {
  root: string;
  onOpenCommit: (id: string) => void;
  onOpenMergeRequest: (number: number) => void;
  onOpenLocks: () => void;
};

const ACTIVITY_POLL_MS = 60_000;

// ActivityView is the project timeline: pushes of recently updated branches,
// merge requests opened or merged, and locks taken, grouped by day.
export function ActivityView({ root, onOpenCommit, onOpenMergeRequest, onOpenLocks }: Props) {
  const { t } = useTranslation();
  const [range, setRange] = usePersistedState<FeedRange>(repoSetting(root, "activity.range"), "week", { valid: isFeedRange });
  const [person, setPerson] = useState("");
  const [groups, setGroups] = useState<ReadonlySet<FeedGroup>>(() => new Set(feedGroups.map((g) => g.id)));
  const feed = useQuery({ queryKey: activityKey(root, range), queryFn: () => desktop.projectActivity(root, feedSince(range)), refetchInterval: ACTIVITY_POLL_MS });

  const events = feed.data?.events ?? [];
  const people = useMemo(() => feedPeople(events), [events]);
  const days = useMemo(() => groupByDay(filterFeed(events, { person, groups })), [events, person, groups]);
  const toggle = (id: FeedGroup) =>
    setGroups((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const open = (e: FeedEvent) => (e.kind === "commit" ? onOpenCommit(e.commitId) : e.kind === "lock" ? onOpenLocks() : onOpenMergeRequest(e.number));

  if (feed.isPending) {
    return (
      <Centered>
        <Loader2 className="size-5 animate-spin text-muted-foreground" />
        <p className="text-sm font-medium">{t("activityView.loadingActivity")}</p>
      </Centered>
    );
  }
  if (!feed.data) {
    return (
      <Centered>
        <AlertTriangle className="size-5 text-danger" />
        <p className="text-sm font-medium">{t("activityView.couldNotLoadActivity")}</p>
        <p className="max-w-sm text-xs text-muted-foreground">{errorMessage(feed.error)}</p>
        <Button size="sm" onClick={() => void feed.refetch()}>
          <RefreshCw />
          {t("activityView.tryAgain")}
        </Button>
      </Centered>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-[12px]">
        <select value={range} onChange={(e) => setRange(e.target.value as FeedRange)} aria-label={t("activityView.timeRange")} className="rounded-md border border-border bg-background px-2 py-1">
          {feedRanges.map((r) => (
            <option key={r.id} value={r.id}>
              {r.label}
            </option>
          ))}
        </select>
        <select value={person} onChange={(e) => setPerson(e.target.value)} aria-label={t("activityView.person")} className="rounded-md border border-border bg-background px-2 py-1">
          <option value="">{t("activityView.everyone")}</option>
          {people.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
        <div className="flex rounded-md border border-border p-0.5" role="group" aria-label={t("activityView.show")}>
          {feedGroups.map((g) => (
            <button
              key={g.id}
              type="button"
              aria-pressed={groups.has(g.id)}
              onClick={() => toggle(g.id)}
              className={cn("rounded px-2 py-0.5 text-muted-foreground hover:text-foreground", groups.has(g.id) && "bg-muted text-foreground")}
            >
              {g.label}
            </button>
          ))}
        </div>
        <Button size="sm" className="ml-auto" onClick={() => void feed.refetch()} title={t("activityView.refreshActivity")} aria-label={t("activityView.refreshActivity")} disabled={feed.isFetching}>
          <RefreshCw className={cn(feed.isFetching && "animate-spin")} />
        </Button>
      </div>
      {(feed.data.truncated || feed.data.partial) && (
        <p role="status" className="shrink-0 border-b border-border bg-muted px-4 py-1.5 text-[11px] text-muted-foreground">
          {feed.data.partial ? translate("activityView.someActivityCouldNotBe") : ""}
          {feed.data.truncated ? translate("activityView.thisIsTheMostRecent") : ""}
        </p>
      )}
      {days.length === 0 ? (
        <Centered>
          <p className="text-sm font-medium">{events.length === 0 ? translate("activityView.nothingHasHappenedInThis") : translate("activityView.nothingMatchesTheseFilters")}</p>
          <p className="max-w-sm text-xs text-muted-foreground">{events.length === 0 ? translate("activityView.tryALongerRangeOr") : translate("activityView.chooseAnotherPersonOrShow")}</p>
        </Centered>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="mx-auto max-w-3xl space-y-4 p-4">
            {days.map((day) => (
              <section key={day.key} aria-label={day.label}>
                <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{day.label}</h3>
                <ul className="divide-y divide-border rounded-md border border-border bg-card">
                  {day.events.map((e) => (
                    <li key={e.id}>
                      <button type="button" onClick={() => open(e)} className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-[12px] hover:bg-muted">
                        <Avatar name={e.actor} />
                        <KindIcon kind={e.kind} />
                        <span className="min-w-0 flex-1 truncate">
                          <span className="font-medium">{e.actor}</span> <span className="text-muted-foreground">{feedText(e)}</span> <span>{e.title}</span>
                        </span>
                        {e.kind === "commit" && <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{shortId(e.commitId)}</span>}
                        {e.branch && <Badge className="shrink-0 font-mono">{e.branch}</Badge>}
                        <time dateTime={e.time} title={new Date(e.time).toLocaleString(locale())} className="w-12 shrink-0 text-right text-[11px] text-muted-foreground">
                          {new Date(e.time).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" })}
                        </time>
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function KindIcon({ kind }: { kind: string }) {
  const { t } = useTranslation();
  const cls = "size-3.5 shrink-0 text-muted-foreground";
  return kind === "commit" ? <GitCommit className={cls} aria-label={t("activityView.push")} /> : kind === "lock" ? <Lock className={cls} aria-label={t("activityView.lock")} /> : kind === "mr_merged" ? <GitMerge className={cls} aria-label={t("activityView.merged")} /> : <GitPullRequest className={cls} aria-label={t("activityView.opened")} />;
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">{children}</div>;
}
