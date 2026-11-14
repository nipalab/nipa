import { describe, expect, it } from "vitest";
import type { ConflictFile } from "@/api/desktop";
import { choices, compose, conflictCount, editsText, hasMarkers, kindLabel, mergesScene, parseConflicts, pickText, segmentsFor, unresolved } from "./conflicts";

const marked = "top\n<<<<<<< ours\nmine\n=======\ntheirs\n>>>>>>> theirs\nmiddle\n<<<<<<< ours\n=======\nadded\n>>>>>>> theirs\n";
const file = (over: Partial<ConflictFile> = {}): ConflictFile => ({ path: "a.txt", kind: "text", binary: false, resolved: false, viewer: "text", ...over });
const text = (t: string, missing = false) => ({ text: t, missing });

describe("conflicts", () => {
  it("parses marker regions between shared text", () => {
    expect(parseConflicts(marked)).toEqual([
      { kind: "same", text: "top\n" },
      { kind: "conflict", ours: "mine\n", theirs: "theirs\n" },
      { kind: "same", text: "middle\n" },
      { kind: "conflict", ours: "", theirs: "added\n" },
    ]);
    expect(parseConflicts("")).toEqual([]);
    expect(parseConflicts("a\n<<<<<<< ours\nb\n")).toEqual([{ kind: "same", text: "a\n<<<<<<< ours\nb\n" }]);
    expect(parseConflicts("<<<<<<< ours\nb\n=======\nc\n")).toEqual([{ kind: "same", text: "<<<<<<< ours\nb\n=======\nc\n" }]);
  });

  it("detects markers only at line starts", () => {
    expect(hasMarkers(marked)).toBe(true);
    expect(hasMarkers(">>>>>>> theirs\n")).toBe(true);
    expect(hasMarkers("x <<<<<<< ours\n")).toBe(false);
  });

  it("combines sides", () => {
    expect(pickText("a\n", "b\n", "ours")).toBe("a\n");
    expect(pickText("a\n", "b\n", "theirs")).toBe("b\n");
    expect(pickText("a", "b\n", "ours-theirs")).toBe("a\nb\n");
    expect(pickText("a\n", "b", "theirs-ours")).toBe("b\na\n");
    expect(pickText("", "b\n", "ours-theirs")).toBe("b\n");
  });

  it("composes picks and keeps markers for unpicked blocks", () => {
    const segments = parseConflicts(marked);
    expect(conflictCount(segments)).toBe(2);
    expect(compose(segments, ["theirs", "ours-theirs"])).toBe("top\ntheirs\nmiddle\nadded\n");
    expect(compose(segments, ["ours", undefined])).toBe("top\nmine\nmiddle\n<<<<<<< ours\n=======\nadded\n>>>>>>> theirs\n");
  });

  it("builds a whole-file block only for files both sides added", () => {
    const texts = { base: text("", true), ours: text("mine\n"), theirs: text("theirs\n"), working: text("mine\n") };
    expect(segmentsFor(texts, "add_add")).toEqual([{ kind: "conflict", ours: "mine\n", theirs: "theirs\n" }]);
    expect(segmentsFor(texts, "text")).toEqual([{ kind: "same", text: "mine\n" }]);
    expect(segmentsFor({ ...texts, working: text("fixed\n") }, "add_add")).toEqual([{ kind: "same", text: "fixed\n" }]);
    expect(segmentsFor({ ...texts, working: text(marked) }, "add_add")).toHaveLength(4);
  });

  it("knows which files the editor handles", () => {
    expect(editsText(file())).toBe(true);
    expect(editsText(file({ kind: "add_add" }))).toBe(true);
    expect(editsText(file({ kind: "add_add", binary: true }))).toBe(false);
    expect(editsText(file({ kind: "modify_delete" }))).toBe(false);
    expect(mergesScene(file({ path: "levels/Arena.TSCN" }))).toBe(true);
    expect(mergesScene(file({ path: "items/sword.tres", kind: "add_add" }))).toBe(true);
    expect(mergesScene(file({ path: "levels/arena.tscn", binary: true }))).toBe(false);
    expect(mergesScene(file({ path: "player.gd" }))).toBe(false);
  });

  it("labels kinds and choices", () => {
    expect(["text", "binary", "add_add", "modify_delete", "delete_modify", "other"].map(kindLabel)).toEqual([
      "Both changed",
      "Both changed (binary)",
      "Both added",
      "Deleted on their side",
      "Deleted on your side",
      "other",
    ]);
    const set = { op: "merge" as const, ours: { id: "o", label: "main" }, theirs: { id: "t", label: "feature" } };
    expect(choices(file(), set).map((c) => c.label)).toEqual(["Take mine", "Take theirs"]);
    expect(choices(file({ kind: "modify_delete" }), set).map((c) => c.label)).toEqual(["Keep the file", "Delete it"]);
    expect(choices(file({ kind: "delete_modify" }), set).map((c) => c.hint)).toEqual(["main deleted it", "Take the version from feature"]);
    expect(choices(file(), { ...set, ours: { id: "", label: "" }, theirs: { id: "", label: "" } })[1].hint).toBe("Use the version from theirs");
    expect(unresolved({ files: [file(), file({ resolved: true })] })).toBe(1);
  });
});
