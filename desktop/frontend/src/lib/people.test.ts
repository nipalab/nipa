import { describe, expect, it } from "vitest";
import { hueFor, initials } from "./people";

describe("initials", () => {
  it("uses the first and last word", () => {
    expect(initials("Alex Rivera")).toBe("AR");
    expect(initials("  mina  van  okafor ")).toBe("MO");
  });
  it("handles one word, empty and wide characters", () => {
    expect(initials("jon")).toBe("J");
    expect(initials("")).toBe("?");
    expect(initials("田中 太郎")).toBe("田太");
  });
});

describe("hueFor", () => {
  it("is stable and within a colour wheel", () => {
    expect(hueFor("u-alex")).toBe(hueFor("u-alex"));
    expect(hueFor("u-alex")).not.toBe(hueFor("u-mina"));
    expect(hueFor("anything at all")).toBeLessThan(360);
    expect(hueFor("")).toBe(0);
  });
});
