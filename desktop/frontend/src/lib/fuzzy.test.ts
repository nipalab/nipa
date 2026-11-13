import { describe, expect, it } from "vitest";
import { compareMatches, fuzzyMatch, normalizeQuery, rank, segments } from "./fuzzy";

describe("fuzzyMatch", () => {
  it("ranks basename prefix, substring and subsequence before path matches", () => {
    expect(fuzzyMatch("hero", "Assets/hero.png")).toEqual({ tier: 0, span: 4, positions: [7, 8, 9, 10] });
    expect(fuzzyMatch("hero", "Assets/superhero.png")?.tier).toBe(1);
    expect(fuzzyMatch("hro", "Assets/hero.png")).toEqual({ tier: 2, span: 4, positions: [7, 9, 10] });
    expect(fuzzyMatch("hero", "Hero/armor.png")).toEqual({ tier: 3, span: 4, positions: [0, 1, 2, 3] });
    expect(fuzzyMatch("har", "Hero/armor.png")?.tier).toBe(4);
    expect(fuzzyMatch("xyz", "Hero/armor.png")).toBeNull();
    expect(fuzzyMatch("  ", "anything")).toBeNull();
  });

  it("ignores case and spaces and picks the tightest window", () => {
    expect(fuzzyMatch("PL MOV", "Source/Player/Movement.cs")?.tier).toBe(4);
    expect(fuzzyMatch("ab", "a__a_b")).toEqual({ tier: 2, span: 3, positions: [3, 5] });
    expect(fuzzyMatch("ba", "ab")).toBeNull();
    expect(fuzzyMatch("é", "CAFÉ")?.positions).toEqual([3]);
  });

  it("treats backslashes as separators", () => {
    expect(fuzzyMatch("x", "a\\x.txt")?.tier).toBe(0);
  });
});

describe("rank", () => {
  it("orders by tier, span, length then text and applies the limit", () => {
    const items = ["docs/the_mega_entry.md", "Music/theme.wav", "x/THEME.WAV", "a/long_theme_music.wav", "t/h/e/m/e.txt", "zzz"];
    expect(rank(items, "theme", (s) => s).map((r) => r.item)).toEqual(["x/THEME.WAV", "Music/theme.wav", "a/long_theme_music.wav", "docs/the_mega_entry.md", "t/h/e/m/e.txt"]);
    expect(rank(items, "theme", (s) => s, 2)).toHaveLength(2);
    expect(rank(items, "", (s) => s)).toEqual([]);
  });

  it("breaks full ties alphabetically", () => {
    const m = { tier: 0, span: 1, positions: [0] };
    expect(compareMatches(m, m, "b", "a")).toBe(1);
    expect(compareMatches(m, m, "a", "b")).toBe(-1);
    expect(compareMatches(m, m, "a", "a")).toBe(0);
  });
});

describe("helpers", () => {
  it("normalizes queries", () => {
    expect(normalizeQuery(" A b ")).toEqual(["a", "b"]);
  });

  it("splits text into highlighted segments", () => {
    expect(segments("hero.png", [0, 1, 5])).toEqual([
      { text: "he", hit: true },
      { text: "ro.", hit: false },
      { text: "p", hit: true },
      { text: "ng", hit: false },
    ]);
    expect(segments("abc")).toEqual([{ text: "abc", hit: false }]);
  });
});
