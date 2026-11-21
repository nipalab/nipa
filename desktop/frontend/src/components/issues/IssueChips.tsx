import { useQuery } from "@tanstack/react-query";
import { Ticket } from "lucide-react";
import { useTranslation } from "react-i18next";
import { desktop, type Issue, type IssueKey } from "@/api/desktop";
import { cn } from "@/lib/utils";

const chip = "inline-flex shrink-0 items-center gap-0.5 rounded bg-accent/15 px-1 font-mono text-[10px] text-accent";

// IssueChips shows the issues linked to a commit or merge request. List rows
// get the keys only; with root (detail headers) each chip links to the tracker
// and shows the issue's title and status once the tracker answered.
export function IssueChips({ issues, root, className }: { issues?: IssueKey[] | null; root?: string; className?: string }) {
  const { t } = useTranslation();
  const keys = (issues ?? []).map((i) => i.key);
  const resolved = useQuery({
    queryKey: ["issues-resolve", root, keys.join(",")],
    queryFn: () => desktop.resolveIssues(root ?? "", keys),
    enabled: !!root && keys.length > 0,
    staleTime: 60_000,
    retry: false,
  });
  if (!issues?.length) return null;
  const known = new Map<string, Issue>((resolved.data ?? []).filter((i) => i.state === "ok").map((i) => [i.key, i]));
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-1", className)} aria-label={t("issues.linkedIssues")}>
      {issues.map((issue) => {
        if (!root) {
          return (
            <span key={issue.key} className={chip} title={issue.key}>
              <Ticket className="size-2.5" aria-hidden />
              {issue.key}
            </span>
          );
        }
        const detail = known.get(issue.key);
        return (
          <span key={issue.key} className="inline-flex min-w-0 items-center gap-1">
            <a href={issue.url} target="_blank" rel="noreferrer" title={t("issues.openInTracker", { key: issue.key })} className={cn(chip, "hover:underline")}>
              <Ticket className="size-2.5" aria-hidden />
              {issue.key}
            </a>
            {detail && (
              <>
                <span className="min-w-0 truncate text-foreground" title={detail.title}>
                  {detail.title}
                </span>
                {detail.status && <span className="shrink-0 rounded bg-muted px-1 text-[10px]">{detail.status}</span>}
              </>
            )}
          </span>
        );
      })}
    </span>
  );
}
