import type { FeedEvent } from "@/api/desktop";
import { translate , locale } from "@/i18n";

export type FeedRange = "today" | "week" | "month";
export type FeedGroup = "commit" | "request" | "lock";

export const feedRanges: { id: FeedRange; label: string }[] = [
  { id: "today", label: translate("feed.today") },
  { id: "week", label: translate("feed.last7Days") },
  { id: "month", label: translate("feed.last30Days") },
];

export const feedGroups: { id: FeedGroup; label: string }[] = [
  { id: "commit", label: translate("feed.pushes") },
  { id: "request", label: translate("feed.mergeRequests") },
  { id: "lock", label: translate("feed.locks") },
];

export const isFeedRange = (v: unknown): v is FeedRange => feedRanges.some((r) => r.id === v);

// feedSince is the RFC 3339 start of a range; today starts at local midnight.
export function feedSince(range: FeedRange, now: Date = new Date()): string {
  const from = new Date(now);
  if (range === "today") from.setHours(0, 0, 0, 0);
  else from.setDate(from.getDate() - (range === "week" ? 7 : 30));
  return from.toISOString();
}

export const groupOf = (e: FeedEvent): FeedGroup => (e.kind === "commit" ? "commit" : e.kind === "lock" ? "lock" : "request");

export type FeedFilter = { person: string; groups: ReadonlySet<FeedGroup> };

export const filterFeed = (events: FeedEvent[], f: FeedFilter): FeedEvent[] => events.filter((e) => f.groups.has(groupOf(e)) && (!f.person || e.actor === f.person));

// feedPeople lists who appears in the feed, alphabetically.
export const feedPeople = (events: FeedEvent[]): string[] => [...new Set(events.map((e) => e.actor).filter(Boolean))].sort((a, b) => a.localeCompare(b));

const dayKey = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

export type FeedDay = { key: string; label: string; events: FeedEvent[] };

// groupByDay splits newest-first events into local calendar days.
export function groupByDay(events: FeedEvent[], now: Date = new Date()): FeedDay[] {
  const today = dayKey(now);
  const yesterday = dayKey(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1));
  const days: FeedDay[] = [];
  for (const e of events) {
    const at = new Date(e.time);
    if (Number.isNaN(at.getTime())) continue;
    const key = dayKey(at);
    let day = days[days.length - 1];
    if (day?.key !== key) {
      const label = key === today ? translate("feed.today") : key === yesterday ? translate("feed.yesterday") : at.toLocaleDateString(locale(), { weekday: "long", month: "short", day: "numeric" });
      day = { key, label, events: [] };
      days.push(day);
    }
    day.events.push(e);
  }
  return days;
}

export function feedText(e: FeedEvent): string {
  switch (e.kind) {
    case "commit":
      return translate("feed.pushedVerb");
    case "mr_opened":
      return translate("feed.opened", { number: e.number });
    case "mr_merged":
      return translate("feed.merged", { number: e.number });
    case "lock":
      return translate("feed.lockedVerb");
    default:
      return e.kind.replace(/_/g, " ");
  }
}
