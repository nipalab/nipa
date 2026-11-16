import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { describe, expect, it } from "vitest";

import { groupStatus, hasSnapshot } from "../src/scm/changes";
import { findRepoRoot, relativeTo } from "../src/scm/discovery";
import { status } from "./fakes/daemon";

describe("groupStatus", () => {
  it("groups like the desktop Changes screen", () => {
    const changes = groupStatus(
      status({
        staged: ["b.txt", "a.png"],
        added: ["a.png"],
        deleted: ["gone.txt"],
        modified: ["m.txt"],
        missing: ["lost.txt"],
        untracked: ["new.txt"],
        conflicts: ["c2.txt", "c1.txt"],
        resolved: ["c1.txt"],
        pending: "merge",
        pending_remaining: 0,
      }) as never,
    );
    expect(changes.staged).toEqual([
      { path: "a.png", kind: "added" },
      { path: "b.txt", kind: "modified" },
      { path: "gone.txt", kind: "deleted" },
    ]);
    expect(changes.unstaged).toEqual([
      { path: "lost.txt", kind: "deleted" },
      { path: "m.txt", kind: "modified" },
      { path: "new.txt", kind: "untracked" },
    ]);
    expect(changes.conflicts).toEqual([
      { path: "c1.txt", kind: "resolved" },
      { path: "c2.txt", kind: "conflict" },
    ]);
    expect(changes.pending).toBe("merge");
    expect(changes.detachedTag).toBeUndefined();
  });

  it("tolerates missing lists and reports a detached tag", () => {
    const changes = groupStatus({ branch: "main", head: { kind: "tag", name: "v1" } } as never);
    expect(changes).toMatchObject({ detachedTag: "v1", pending: undefined, pendingRemaining: 0, staged: [], unstaged: [], conflicts: [] });
    expect(groupStatus({ branch: "main", head: { kind: "branch", name: "x" } } as never).detachedTag).toBeUndefined();
  });

  it("knows which kinds have a synced version", () => {
    expect(hasSnapshot("modified")).toBe(true);
    expect(hasSnapshot("untracked")).toBe(false);
    expect(hasSnapshot("added")).toBe(false);
  });
});

describe("discovery", () => {
  it("finds the clone root from a nested folder", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "nipa-root-"));
    try {
      await fs.mkdir(path.join(dir, ".nipa"));
      await fs.writeFile(path.join(dir, ".nipa", "config"), "{}");
      await fs.mkdir(path.join(dir, "a", "b"), { recursive: true });
      expect(await findRepoRoot(path.join(dir, "a", "b"))).toBe(dir);
      expect(await findRepoRoot(os.tmpdir())).toBeUndefined();
    } finally {
      await fs.rm(dir, { recursive: true, force: true });
    }
  });

  it("stops after 32 parents", async () => {
    const deep = path.join("/", ...Array.from({ length: 40 }, (_, i) => `d${i}`));
    const seen: string[] = [];
    expect(
      await findRepoRoot(deep, async (f) => {
        seen.push(f);
        return false;
      }),
    ).toBeUndefined();
    expect(seen).toHaveLength(33);
  });

  it("computes repository-relative paths", () => {
    expect(relativeTo("/r", "/r/a/b.txt")).toBe("a/b.txt");
    expect(relativeTo("/r", "/r")).toBeUndefined();
    expect(relativeTo("/r", "/other/x")).toBeUndefined();
  });
});
