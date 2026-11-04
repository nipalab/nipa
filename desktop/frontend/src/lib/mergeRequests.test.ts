import { describe, expect, it } from "vitest";
import type { Thread } from "@/api/desktop";
import { blockedReason, conversation, eventText, fileThreads, mergeabilityText, threadAt } from "./mergeRequests";

const thread = (over: Partial<Thread>): Thread => ({ id: "t", path: "", oldLine: 0, newLine: 0, outdated: false, resolved: false, resolvedBy: "", createdAt: "", comments: [], ...over });

describe("merge request helpers", () => {
  it("merges timeline, reviews and conversation threads in time order", () => {
    const items = conversation({
      timeline: [
        { id: "e1", kind: "opened", actor: { id: "u1", name: "Ana" }, subject: { id: "", name: "" }, body: "", commitId: "", createdAt: "2026-11-05T09:00:00Z" },
        { id: "e2", kind: "review_submitted", actor: { id: "u2", name: "Bo" }, subject: { id: "", name: "" }, body: "", commitId: "", createdAt: "2026-11-05T11:00:00Z" },
      ],
      reviews: [{ id: "r1", reviewer: { id: "u2", name: "Bo" }, state: "approved", body: "", stale: false, dismissed: false, createdAt: "2026-11-05T11:00:00Z" }],
      threads: [thread({ id: "t1", createdAt: "2026-11-05T10:00:00Z" }), thread({ id: "t2", path: "a.go", createdAt: "2026-11-05T08:00:00Z" })],
      checks: [],
    });
    expect(items.map((i) => i.kind)).toEqual(["event", "thread", "review"]);
  });

  it("anchors threads to lines and splits outdated ones", () => {
    expect(threadAt(thread({ newLine: 4 }), { oldLine: 0, newLine: 4 })).toBe(true);
    expect(threadAt(thread({ oldLine: 4 }), { oldLine: 4, newLine: 0 })).toBe(true);
    expect(threadAt(thread({ oldLine: 4, newLine: 5 }), { oldLine: 4, newLine: 0 })).toBe(false);
    const split = fileThreads([thread({ id: "a", path: "x" }), thread({ id: "b", path: "x", outdated: true }), thread({ id: "c", path: "y" })], "x");
    expect(split.inline.map((t) => t.id)).toEqual(["a"]);
    expect(split.outdated.map((t) => t.id)).toEqual(["b"]);
    const renamed = fileThreads([thread({ id: "a", path: "old" }), thread({ id: "b", path: "new" })], "new", "old");
    expect(renamed.inline.map((t) => t.id)).toEqual(["a", "b"]);
  });

  it("explains blockers, mergeability and events", () => {
    expect(blockedReason("draft")).toContain("draft");
    expect(blockedReason("custom_gate")).toBe("Blocked: custom gate");
    expect(blockedReason("")).toBe("");
    expect(mergeabilityText("mergeable")).toBe("Ready to merge");
    expect(mergeabilityText("odd")).toBe("odd");
    expect(eventText("pushed")).toBe("pushed new commits");
    expect(eventText("new_kind")).toBe("new kind");
  });
});
