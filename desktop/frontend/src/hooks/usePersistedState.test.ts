import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { usePersistedState } from "./usePersistedState";

const isString = (v: unknown): v is string => typeof v === "string";

describe("usePersistedState", () => {
  it("keeps the value across remounts", () => {
    const first = renderHook(() => usePersistedState("nipa.k", "a"));
    expect(first.result.current[0]).toBe("a");
    act(() => first.result.current[1]("b"));
    act(() => first.result.current[1]((prev) => `${prev}c`));
    expect(first.result.current[0]).toBe("bc");
    expect(localStorage.getItem("nipa.k")).toBe('"bc"');
    first.unmount();
    expect(renderHook(() => usePersistedState("nipa.k", "a")).result.current[0]).toBe("bc");
  });

  it("falls back on invalid or corrupt values", () => {
    localStorage.setItem("nipa.k", "42");
    expect(renderHook(() => usePersistedState("nipa.k", "a", { valid: isString })).result.current[0]).toBe("a");
    localStorage.setItem("nipa.k", "{");
    expect(renderHook(() => usePersistedState("nipa.k", "a")).result.current[0]).toBe("a");
  });

  it("prefers an explicit value and stores it", () => {
    localStorage.setItem("nipa.k", '"old"');
    const { result } = renderHook(() => usePersistedState("nipa.k", "a", { prefer: "deep" }));
    expect(result.current[0]).toBe("deep");
    expect(localStorage.getItem("nipa.k")).toBe('"deep"');
  });

  it("forgets an undefined value", () => {
    localStorage.setItem("nipa.k", "1");
    const { result } = renderHook(() => usePersistedState<number | undefined>("nipa.k", undefined));
    act(() => result.current[1](undefined));
    expect(localStorage.getItem("nipa.k")).toBeNull();
  });
});
