import { describe, expect, it } from "vitest";
import type { DiffHunk } from "@/api/desktop";
import { formatBytes, hunkHeader, splitPath, splitRows, unifiedRows } from "./diff";

const hunk: DiffHunk = {
  oldStart: 1,
  oldLines: 4,
  newStart: 1,
  newLines: 4,
  lines: [
    { kind: "context", old: 1, new: 1, text: "a" },
    { kind: "removed", old: 2, new: 0, text: "b" },
    { kind: "removed", old: 3, new: 0, text: "c" },
    { kind: "added", old: 0, new: 2, text: "B" },
    { kind: "context", old: 4, new: 3, text: "d" },
    { kind: "added", old: 0, new: 4, text: "e", noNewline: true },
  ],
};

describe("diff rows", () => {
  it("formats hunk headers", () => {
    expect(hunkHeader(hunk)).toBe("@@ -1,4 +1,4 @@");
  });

  it("builds unified rows", () => {
    const rows = unifiedRows([hunk, { ...hunk, lines: null }]);
    expect(rows).toHaveLength(8);
    expect(rows[0]).toEqual({ type: "hunk", header: "@@ -1,4 +1,4 @@" });
    expect(rows[2]).toEqual({ type: "line", kind: "removed", old: 2, new: 0, text: "b", noNewline: false });
    expect(rows[6]).toMatchObject({ kind: "added", noNewline: true });
    expect(rows[7]).toEqual({ type: "hunk", header: "@@ -1,4 +1,4 @@" });
  });

  it("pairs removed and added runs side by side", () => {
    const rows = splitRows([hunk]);
    expect(rows).toEqual([
      { type: "hunk", header: "@@ -1,4 +1,4 @@" },
      { type: "pair", left: { kind: "context", line: 1, text: "a", noNewline: false }, right: { kind: "context", line: 1, text: "a", noNewline: false } },
      { type: "pair", left: { kind: "removed", line: 2, text: "b", noNewline: false }, right: { kind: "added", line: 2, text: "B", noNewline: false } },
      { type: "pair", left: { kind: "removed", line: 3, text: "c", noNewline: false }, right: null },
      { type: "pair", left: { kind: "context", line: 4, text: "d", noNewline: false }, right: { kind: "context", line: 3, text: "d", noNewline: false } },
      { type: "pair", left: null, right: { kind: "added", line: 4, text: "e", noNewline: true } },
    ]);
  });

  it("starts a new pair group when a removal follows additions", () => {
    const rows = splitRows([
      {
        ...hunk,
        lines: [
          { kind: "added", old: 0, new: 1, text: "x" },
          { kind: "removed", old: 1, new: 0, text: "y" },
          { kind: "weird", old: 2, new: 2, text: "z" },
        ],
      },
    ]);
    expect(rows.slice(1)).toEqual([
      { type: "pair", left: null, right: { kind: "added", line: 1, text: "x", noNewline: false } },
      { type: "pair", left: { kind: "removed", line: 1, text: "y", noNewline: false }, right: null },
      { type: "pair", left: { kind: "context", line: 2, text: "z", noNewline: false }, right: { kind: "context", line: 2, text: "z", noNewline: false } },
    ]);
    expect(splitRows([{ ...hunk, lines: null }])).toHaveLength(1);
  });
});

describe("formatting helpers", () => {
  it("formats byte sizes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(50 * 1024 * 1024)).toBe("50 MB");
    expect(formatBytes(3 * 1024 ** 5)).toBe("3072 TB");
  });

  it("splits paths", () => {
    expect(splitPath("art/hero.png")).toEqual({ dir: "art/", name: "hero.png" });
    expect(splitPath("README")).toEqual({ dir: "", name: "README" });
  });
});
