import { describe, expect, it } from "vitest";
import type { Commit, FileHit } from "@/api/desktop";
import { commitMatches, looksLikeCommitId, mergeFileHits, mrNumber } from "./palette";

const hit = (path: string): FileHit => ({ path, name: path.slice(path.lastIndexOf("/") + 1), size: 1, binary: false, kind: "text" });
const commit = (id: string, message: string, hash = "f".repeat(64)): Commit => ({ id, hash, parents: [], author: "Ann", email: "", message, date: "" });

describe("mergeFileHits", () => {
  it("ranks every clone's hits together and keeps clone order on ties", () => {
    const merged = mergeFileHits(
      [
        { root: "/a", hits: [hit("Assets/Hero/armor.png"), hit("docs/hero.md")] },
        { root: "/b", hits: [hit("Art/hero.png"), hit("docs/hero.md"), hit("nothing.txt")] },
      ],
      "hero",
      10,
    );
    expect(merged.map((m) => `${m.root}:${m.hit.path}`)).toEqual(["/b:Art/hero.png", "/a:docs/hero.md", "/b:docs/hero.md", "/a:Assets/Hero/armor.png"]);
    expect(merged[0].match.positions).toEqual([4, 5, 6, 7]);
    expect(mergeFileHits([{ root: "/a", hits: [hit("x/hero.png"), hit("y/hero.png")] }], "hero", 1)).toHaveLength(1);
  });
});

describe("mrNumber", () => {
  it("reads plain, # and ! numbers", () => {
    expect(mrNumber("12")).toBe(12);
    expect(mrNumber(" #7 ")).toBe(7);
    expect(mrNumber("!3")).toBe(3);
    expect(mrNumber("#")).toBeNull();
    expect(mrNumber("fix 12")).toBeNull();
  });
});

describe("commits", () => {
  it("recognises commit ids", () => {
    expect(looksLikeCommitId("1abc2def")).toBe(true);
    expect(looksLikeCommitId("abc")).toBe(false);
    expect(looksLikeCommitId("ABCD")).toBe(false);
    expect(looksLikeCommitId("with space")).toBe(false);
  });

  it("puts id and hash prefixes before message matches", () => {
    const commits = [commit("aaa1", "Fix hero armor"), commit("bbb2", "Armor tweaks for the hero", "abcdef01"), commit("ccc3", "Unrelated")];
    expect(commitMatches(commits, "hero armor", 5).map((c) => c.id)).toEqual(["aaa1", "bbb2"]);
    expect(commitMatches(commits, "abcd", 5).map((c) => c.id)).toEqual(["bbb2"]);
    expect(commitMatches(commits, "abc", 5)).toEqual([]);
    expect(commitMatches(commits, "ccc3", 5).map((c) => c.id)).toEqual(["ccc3"]);
    expect(commitMatches(commits, "armor", 1)).toHaveLength(1);
    expect(commitMatches(commits, "  ", 5)).toEqual([]);
  });
});
