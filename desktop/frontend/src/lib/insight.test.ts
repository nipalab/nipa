import { describe, expect, it } from "vitest";
import type { ChangeSet } from "@/api/desktop";
import { deletionTargets, emptyOriginForm, recordedDate, referrerSummary, unresolvedRefs, validateOrigin } from "./insight";

describe("insight helpers", () => {
  it("summarizes referrers", () => {
    expect(referrerSummary([])).toBe("");
    expect(referrerSummary(["a"])).toBe("Used by 1 file: a.");
    expect(referrerSummary(["a", "b", "c", "d", "e"])).toBe("Used by 5 files: a, b, c and 2 more.");
    expect(referrerSummary(["a", "b"], 1)).toBe("Used by 2 files: a and 1 more.");
  });

  it("orders unresolved references", () => {
    const list = unresolvedRefs({
      path: "s",
      asset: true,
      uses: [
        { path: "ok", raw: "1", resolved: true },
        { path: "gone", raw: "2", resolved: false },
      ],
      unknown: [{ path: "", raw: "3", resolved: false }],
      usedBy: [],
      scanned: 0,
      truncated: false,
      partial: true,
    });
    expect(list.map((r) => [r.raw, r.outside])).toEqual([
      ["2", false],
      ["3", true],
    ]);
  });

  it("finds staged deletions", () => {
    const c: ChangeSet = {
      branch: "main",
      staged: [{ path: "s", status: "deleted", kind: "" }],
      unstaged: [
        { path: "a", status: "deleted", kind: "" },
        { path: "b", status: "modified", kind: "" },
      ],
      conflicts: [],
      resolved: [],
    };
    expect(deletionTargets(c, ["a", "b", "s"])).toEqual(["a"]);
  });

  it("validates the origin form", () => {
    const form = emptyOriginForm();
    expect(validateOrigin(form)).toMatch(/tool/);
    expect(validateOrigin({ ...form, tool: "x", seed: "1e3" })).toMatch(/seed/);
    expect(validateOrigin({ ...form, tool: "x", seed: " 12 " })).toBe("");
    expect(emptyOriginForm({ tool: "t", model: "m", seed: "1", license: "l", note: "n", humanEdited: true } as never)).toEqual({
      tool: "t",
      model: "m",
      seed: "1",
      license: "l",
      prompt: "",
      note: "n",
      humanEdited: true,
    });
  });

  it("formats record dates", () => {
    expect(recordedDate("")).toBe("");
    expect(recordedDate("nope")).toBe("nope");
    expect(recordedDate("2026-03-04T05:06:07Z")).toMatch(/2026/);
  });
});
