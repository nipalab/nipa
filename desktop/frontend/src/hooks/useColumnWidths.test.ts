import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { COLUMN_MAX, useColumnWidths } from "./useColumnWidths";

const defaults = { path: 400 };

describe("useColumnWidths", () => {
  it("falls back to defaults and then to the given width", () => {
    const { result } = renderHook(() => useColumnWidths("t", defaults));
    expect(result.current.width("path")).toBe(400);
    expect(result.current.width("other")).toBe(160);
    expect(result.current.width("other", 90)).toBe(90);
  });

  it("restores stored widths and ignores invalid entries", () => {
    localStorage.setItem("nipa.layout.cols.t", JSON.stringify({ path: 250, bad: "x", huge: 99999, tiny: 1 }));
    const { result } = renderHook(() => useColumnWidths("t", defaults));
    expect(result.current.width("path")).toBe(250);
    expect(result.current.width("bad")).toBe(160);
    expect(result.current.width("huge")).toBe(COLUMN_MAX);
    expect(result.current.width("tiny")).toBe(48);
  });

  it.each(["not json", "[1,2]", "null"])("ignores a malformed store %s", (raw) => {
    localStorage.setItem("nipa.layout.cols.t", raw);
    expect(renderHook(() => useColumnWidths("t", defaults)).result.current.width("path")).toBe(400);
  });

  it("previews, commits and resets one column", () => {
    const { result } = renderHook(() => useColumnWidths("t", defaults, 60));
    act(() => result.current.preview("path", 10));
    expect(result.current.width("path")).toBe(60);
    expect(localStorage.getItem("nipa.layout.cols.t")).toBeNull();

    act(() => result.current.commit("path", 512));
    act(() => result.current.commit("size", 90));
    expect(JSON.parse(localStorage.getItem("nipa.layout.cols.t")!)).toEqual({ path: 512, size: 90 });

    act(() => result.current.reset("path"));
    expect(result.current.width("path")).toBe(400);
    expect(JSON.parse(localStorage.getItem("nipa.layout.cols.t")!)).toEqual({ size: 90 });
    act(() => result.current.reset("size"));
    expect(localStorage.getItem("nipa.layout.cols.t")).toBeNull();
  });

  it("reloads when the key changes", () => {
    localStorage.setItem("nipa.layout.cols.b", JSON.stringify({ path: 222 }));
    const { result, rerender } = renderHook(({ k }) => useColumnWidths(k, defaults), { initialProps: { k: "a" } });
    expect(result.current.width("path")).toBe(400);
    rerender({ k: "b" });
    expect(result.current.width("path")).toBe(222);
  });
});
