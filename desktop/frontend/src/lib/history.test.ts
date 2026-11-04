import { describe, expect, it } from "vitest";
import type { Commit } from "@/api/desktop";
import { commitBody, commitTitle, filterCommits, isImagePath, shortId, tagsByCommit } from "./history";

const commit = (id: string, author: string, message: string): Commit => ({ id, hash: `h${id}`, parents: [], author, email: `${author.toLowerCase()}@studio.dev`, message, date: "" });

describe("history helpers", () => {
  it("splits commit messages", () => {
    expect(commitTitle("Fix hero\n\nLonger text")).toBe("Fix hero");
    expect(commitTitle("  \nbody")).toBe("(no message)");
    expect(commitBody("Fix hero\n\nLonger text\n")).toBe("Longer text");
    expect(commitBody("Fix hero")).toBe("");
  });

  it("filters by every word", () => {
    const list = [commit("a1", "Ana", "Paint hero"), commit("b2", "Bo", "Fix audio mix")];
    expect(filterCommits(list, "")).toBe(list);
    expect(filterCommits(list, "ana hero").map((c) => c.id)).toEqual(["a1"]);
    expect(filterCommits(list, "bo@studio")).toEqual([list[1]]);
    expect(filterCommits(list, "hb2")).toEqual([list[1]]);
    expect(filterCommits(list, "ana audio")).toEqual([]);
  });

  it("groups tags and recognises images", () => {
    const map = tagsByCommit([
      { name: "v1", commitId: "a", message: "", createdAt: "", createdBy: "" },
      { name: "v1.0.1", commitId: "a", message: "", createdAt: "", createdBy: "" },
      { name: "v2", commitId: "b", message: "", createdAt: "", createdBy: "" },
    ]);
    expect(map.get("a")?.map((t) => t.name)).toEqual(["v1", "v1.0.1"]);
    expect(map.get("c")).toBeUndefined();
    expect(isImagePath("art/hero.PNG")).toBe(true);
    expect(isImagePath("art/hero.fbx")).toBe(false);
    expect(shortId("0123456789abcdef")).toBe("0123456789");
  });
});
