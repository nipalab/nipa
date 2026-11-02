import { afterEach, describe, expect, it, vi } from "vitest";
import { cn, errorMessage, readSetting, shortVersion, writeSetting } from "./utils";

afterEach(() => vi.restoreAllMocks());

describe("utils", () => {
  it("merges class names", () => {
    expect(cn("px-2", undefined, "px-4")).toBe("px-4");
  });

  it("shortens Go pseudo-versions", () => {
    expect(shortVersion("v0.0.0-20261102214500-3b410f8954ee+dirty")).toBe("dev-3b410f8+dirty");
    expect(shortVersion("v1.2.4-0.20261102214500-3b410f8954ee")).toBe("dev-3b410f8");
    expect(shortVersion("v1.4.0")).toBe("v1.4.0");
  });

  it("extracts error messages", () => {
    expect(errorMessage(new Error("boom"))).toBe("boom");
    expect(errorMessage("plain")).toBe("plain");
    expect(errorMessage({})).toBe("Something went wrong");
  });

  it("reads and writes settings", () => {
    writeSetting("k", "v");
    expect(readSetting("k")).toBe("v");
    writeSetting("k", null);
    expect(readSetting("k")).toBeNull();
  });

  it("tolerates unavailable storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(readSetting("k")).toBeNull();
    expect(() => writeSetting("k", "v")).not.toThrow();
  });
});
