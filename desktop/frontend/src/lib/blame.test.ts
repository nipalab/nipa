import { describe, expect, it } from "vitest";
import type { Blame, FileDiff } from "@/api/desktop";
import { blameCells, canBlame } from "./blame";

const now = Date.parse("2026-11-14T12:00:00Z");
const commit = (id: string, author: string, minutes: number) => ({
  id,
  hash: "",
  parents: [],
  author,
  email: `${id}@x`,
  message: `Change ${id}\n\nbody`,
  date: new Date(now - minutes * 60_000).toISOString(),
});

describe("blameCells", () => {
  it("labels the first line of each run and titles every line", () => {
    const blame: Blame = {
      path: "a.cs",
      revision: "",
      truncated: true,
      commits: [commit("c2", "Ana", 120), { ...commit("c1", "", 5), email: "ben@x" }],
      lines: [
        { commitId: "c2", uncommitted: false },
        { commitId: "c2", uncommitted: false },
        { commitId: "c1", uncommitted: false },
        { commitId: "", uncommitted: true },
        { commitId: "", uncommitted: true },
        { commitId: "", uncommitted: false },
        { commitId: "gone", uncommitted: false },
      ],
    };
    const cells = blameCells(blame, now);
    expect(cells.size).toBe(7);
    expect(cells.get(1)).toMatchObject({
      first: true,
      label: "Ana · 2 h ago",
      uncommitted: false,
    });
    expect(cells.get(1)?.commit?.id).toBe("c2");
    expect(cells.get(1)?.title).toContain("Change c2");
    expect(cells.get(2)).toMatchObject({ first: false, label: "" });
    expect(cells.get(3)?.label).toBe("ben@x · 5 min ago");
    expect(cells.get(4)).toMatchObject({
      first: true,
      label: "Not pushed",
      uncommitted: true,
    });
    expect(cells.get(5)).toMatchObject({ first: false, label: "" });
    expect(cells.get(6)).toMatchObject({ first: true, label: "Older" });
    expect(cells.get(7)).toMatchObject({ first: true, label: "Older" });
    expect(cells.get(7)?.commit).toBeUndefined();
  });
});

describe("canBlame", () => {
  const side = { path: "a", mode: 0, size: 1, binary: false, hash: "" };
  const diff = (over: Partial<FileDiff>): FileDiff => ({
    path: "a",
    status: "modified",
    kind: "text",
    old: side,
    new: side,
    additions: 0,
    deletions: 0,
    hunks: [],
    ...over,
  });
  it("accepts text new sides only", () => {
    expect(canBlame(diff({}))).toBe(true);
    expect(canBlame(diff({ new: null }))).toBe(false);
    expect(canBlame(diff({ new: { ...side, binary: true } }))).toBe(false);
    expect(canBlame(diff({ status: "deleted" }))).toBe(false);
    expect(canBlame(diff({ contentUnavailable: true }))).toBe(false);
  });
});
