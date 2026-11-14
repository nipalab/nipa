import { describe, expect, it } from "vitest";
import type { FeedEvent } from "@/api/desktop";
import { feedPeople, feedSince, feedText, filterFeed, groupByDay, groupOf, isFeedRange, type FeedGroup } from "./feed";

const event = (over: Partial<FeedEvent>): FeedEvent => ({ id: "x", kind: "commit", time: "2026-11-14T09:00:00", actor: "Ana", title: "t", branch: "main", commitId: "c1", number: 0, path: "", ...over });
const all = new Set<FeedGroup>(["commit", "request", "lock"]);

describe("feedSince", () => {
  const now = new Date(2026, 10, 14, 15, 30);
  it("starts today at local midnight", () => {
    expect(new Date(feedSince("today", now))).toEqual(new Date(2026, 10, 14, 0, 0));
  });
  it("goes back whole days for a week and a month", () => {
    expect(new Date(feedSince("week", now))).toEqual(new Date(2026, 10, 7, 15, 30));
    expect(new Date(feedSince("month", now))).toEqual(new Date(2026, 9, 15, 15, 30));
  });
  it("validates a stored range", () => {
    expect(isFeedRange("week")).toBe(true);
    expect(isFeedRange("year")).toBe(false);
  });
});

describe("filterFeed", () => {
  const events = [event({ id: "1" }), event({ id: "2", kind: "mr_merged", actor: "Bo" }), event({ id: "3", kind: "lock", actor: "Ana" }), event({ id: "4", kind: "mr_opened", actor: "Bo" })];
  it("keeps the chosen kinds and person", () => {
    expect(filterFeed(events, { person: "", groups: all }).map((e) => e.id)).toEqual(["1", "2", "3", "4"]);
    expect(filterFeed(events, { person: "Bo", groups: all }).map((e) => e.id)).toEqual(["2", "4"]);
    expect(filterFeed(events, { person: "", groups: new Set<FeedGroup>(["lock", "commit"]) }).map((e) => e.id)).toEqual(["1", "3"]);
    expect(filterFeed(events, { person: "Ana", groups: new Set<FeedGroup>(["request"]) })).toEqual([]);
  });
  it("maps kinds to groups and lists people once, sorted", () => {
    expect(events.map(groupOf)).toEqual(["commit", "request", "lock", "request"]);
    expect(feedPeople([...events, event({ actor: "" })])).toEqual(["Ana", "Bo"]);
  });
});

describe("groupByDay", () => {
  const now = new Date(2026, 10, 14, 15, 0);
  it("groups newest-first events into local days with friendly labels", () => {
    const days = groupByDay(
      [
        event({ id: "a", time: new Date(2026, 10, 14, 14).toISOString() }),
        event({ id: "b", time: new Date(2026, 10, 14, 9).toISOString() }),
        event({ id: "c", time: new Date(2026, 10, 13, 23).toISOString() }),
        event({ id: "d", time: new Date(2026, 10, 10, 8).toISOString() }),
        event({ id: "bad", time: "nonsense" }),
      ],
      now,
    );
    expect(days.map((d) => [d.label, d.events.map((e) => e.id)])).toEqual([
      ["Today", ["a", "b"]],
      ["Yesterday", ["c"]],
      [new Date(2026, 10, 10).toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" }), ["d"]],
    ]);
  });
  it("is empty without events", () => {
    expect(groupByDay([], now)).toEqual([]);
  });
});

describe("feedText", () => {
  it("describes each kind", () => {
    expect(feedText(event({}))).toBe("pushed");
    expect(feedText(event({ kind: "mr_opened", number: 7 }))).toBe("opened !7");
    expect(feedText(event({ kind: "mr_merged", number: 7 }))).toBe("merged !7");
    expect(feedText(event({ kind: "lock" }))).toBe("locked");
    expect(feedText(event({ kind: "something_new" }))).toBe("something new");
  });
});
