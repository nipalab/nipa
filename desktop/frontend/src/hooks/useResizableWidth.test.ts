import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { clampWidth, useResizableWidth } from "./useResizableWidth";

const bounds = { initial: 300, min: 200, max: 500 };

describe("useResizableWidth", () => {
  it("starts at the default width", () => {
    const { result } = renderHook(() => useResizableWidth("pane", bounds));
    expect(result.current.width).toBe(300);
    expect(result.current.min).toBe(200);
    expect(result.current.max).toBe(500);
  });

  it("restores and clamps the stored width", () => {
    localStorage.setItem("nipa.layout.pane", "420");
    expect(renderHook(() => useResizableWidth("pane", bounds)).result.current.width).toBe(420);
    localStorage.setItem("nipa.layout.pane", "9000");
    expect(renderHook(() => useResizableWidth("pane", bounds)).result.current.width).toBe(500);
    localStorage.setItem("nipa.layout.pane", "junk");
    expect(renderHook(() => useResizableWidth("pane", bounds)).result.current.width).toBe(300);
  });

  it("previews without saving, commits and resets", () => {
    const { result } = renderHook(() => useResizableWidth("pane", bounds));
    act(() => result.current.preview(150));
    expect(result.current.width).toBe(200);
    expect(localStorage.getItem("nipa.layout.pane")).toBeNull();

    act(() => result.current.commit(333.4));
    expect(result.current.width).toBe(333);
    expect(localStorage.getItem("nipa.layout.pane")).toBe("333");

    act(() => result.current.reset());
    expect(result.current.width).toBe(300);
    expect(localStorage.getItem("nipa.layout.pane")).toBeNull();
  });

  it("leaves room for the main pane on small windows", () => {
    expect(clampWidth(900, 200, 2000)).toBe(window.innerWidth - 320);
    expect(clampWidth(100, 200, 300)).toBe(200);
  });
});
