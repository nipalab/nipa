import { describe, expect, it } from "vitest";
import { codepoint, fontRows, formatRange, rangeSize, sampleChars } from "./font";

describe("font helpers", () => {
  it("formats code points and ranges", () => {
    expect(codepoint(0x41)).toBe("U+0041");
    expect(codepoint(0x1f600)).toBe("U+1F600");
    expect(formatRange({ first: 0x41, last: 0x41 })).toBe("U+0041");
    expect(formatRange({ first: 0x41, last: 0x5a })).toBe("U+0041–U+005A");
    expect(rangeSize({ first: 0x41, last: 0x5a })).toBe(26);
  });

  it("samples printable characters", () => {
    expect(sampleChars({ first: 0x41, last: 0x5a }, 5)).toBe("ABCDE");
    expect(sampleChars({ first: 0x7f, last: 0xa1 })).toBe("¡");
    expect(sampleChars({ first: 0x1f600, last: 0x1f602 })).toBe("😀😁😂");
  });

  it("lists the detail rows", () => {
    expect(fontRows.map((r) => r.label)).toContain("x-height");
    const empty = { family: "", style: "", version: "" } as never;
    expect(fontRows[0].value(empty)).toBe("—");
    expect(fontRows[1].value(empty)).toBe("—");
    expect(fontRows[2].value(empty)).toBe("—");
  });
});
