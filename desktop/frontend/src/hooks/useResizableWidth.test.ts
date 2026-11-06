import { createElement } from "react";
import { act, render, renderHook, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
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

  describe("when the window resizes", () => {
    const original = window.innerWidth;
    const resizeTo = (w: number) =>
      act(() => {
        Object.defineProperty(window, "innerWidth", { configurable: true, value: w });
        window.dispatchEvent(new Event("resize"));
      });
    afterEach(() => resizeTo(original));

    it("shrinks the pane and grows it back to the saved width", () => {
      localStorage.setItem("nipa.layout.pane", "480");
      const { result } = renderHook(() => useResizableWidth("pane", bounds));
      expect(result.current.width).toBe(480);

      resizeTo(700);
      expect(result.current.width).toBe(380);
      expect(result.current.max).toBe(380);
      expect(localStorage.getItem("nipa.layout.pane")).toBe("480");

      resizeTo(1200);
      expect(result.current.width).toBe(480);
      expect(result.current.max).toBe(500);
    });
  });

  describe("inside a container", () => {
    afterEach(() => vi.unstubAllGlobals());

    function Pane() {
      const size = useResizableWidth("nested", { initial: 300, min: 200, max: 600 });
      return createElement("div", { ref: size.ref, "data-testid": "pane", "data-width": size.width, "data-max": size.max });
    }

    it("clamps against the parent width, not the window", () => {
      let notify = () => {};
      vi.stubGlobal(
        "ResizeObserver",
        class {
          constructor(cb: () => void) {
            notify = cb;
          }
          observe() {}
          disconnect() {}
        },
      );
      localStorage.setItem("nipa.layout.nested", "580");
      const parent = document.createElement("div");
      let parentWidth = 700;
      Object.defineProperty(parent, "clientWidth", { configurable: true, get: () => parentWidth });
      document.body.appendChild(parent);
      render(createElement(Pane), { container: parent });
      const pane = screen.getByTestId("pane");
      expect(pane).toHaveAttribute("data-width", "380");
      expect(pane).toHaveAttribute("data-max", "380");

      parentWidth = 1000;
      act(() => notify());
      expect(pane).toHaveAttribute("data-width", "580");
      expect(pane).toHaveAttribute("data-max", "600");
      parent.remove();
    });
  });
});
