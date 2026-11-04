import { describe, expect, it } from "vitest";
import { coveredByAncestor, hasSelectedDescendant, isCovered, joinPath, normalizeSelection, parseRepoUrl, toggleFolder } from "./sparse";

describe("sparse selection", () => {
  it("normalizes prefixes", () => {
    expect(normalizeSelection(["/art/", "art/props", "audio", "", "art", "artwork"])).toEqual(["art", "artwork", "audio"]);
  });

  it("answers coverage questions", () => {
    const sel = ["art", "maps/level1"];
    expect(isCovered("art/props", sel)).toBe(true);
    expect(isCovered("artwork", sel)).toBe(false);
    expect(coveredByAncestor("art", sel)).toBe(false);
    expect(coveredByAncestor("art/props", sel)).toBe(true);
    expect(hasSelectedDescendant("maps", sel)).toBe(true);
    expect(hasSelectedDescendant("art", sel)).toBe(false);
  });

  it("toggles folders", () => {
    expect(toggleFolder([], "art")).toEqual(["art"]);
    expect(toggleFolder(["art"], "art")).toEqual([]);
    expect(toggleFolder(["art"], "art/props")).toEqual(["art"]);
    expect(toggleFolder(["maps/a", "maps/b", "audio"], "maps")).toEqual(["audio", "maps"]);
  });

  it("parses clone URLs and joins paths", () => {
    expect(parseRepoUrl("https://nipa.studio.test/studio/game/art")).toEqual({ server: "https://nipa.studio.test", org: "studio", project: "game" });
    expect(parseRepoUrl("http://127.0.0.1:6799/o/p")).toEqual({ server: "http://127.0.0.1:6799", org: "o", project: "p" });
    expect(parseRepoUrl("nope")).toBeNull();
    expect(joinPath("/home/ann/work/", "game")).toBe("/home/ann/work/game");
    expect(joinPath("C:\\Projects\\", "game")).toBe("C:\\Projects\\game");
  });
});
