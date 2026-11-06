import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ColumnResizer, PaneResizer, ResizeHandle } from "./resize-handle";

const setup = (value = 300) => {
  const props = { value, min: 200, max: 500, label: "Resize list", onPreview: vi.fn(), onCommit: vi.fn(), onReset: vi.fn() };
  render(<ResizeHandle {...props} />);
  return { ...props, handle: screen.getByRole("separator", { name: "Resize list" }) };
};

describe("ResizeHandle", () => {
  it("exposes its value to assistive tech", () => {
    const { handle } = setup();
    expect(handle).toHaveAttribute("aria-orientation", "vertical");
    expect(handle).toHaveAttribute("aria-valuenow", "300");
    expect(handle).toHaveAttribute("aria-valuemin", "200");
    expect(handle).toHaveAttribute("aria-valuemax", "500");
  });

  it("drags, clamps and commits on release", () => {
    const { handle, onPreview, onCommit } = setup();
    fireEvent.pointerDown(handle, { button: 0, clientX: 100, pointerId: 1 });
    expect(handle).toHaveAttribute("data-dragging", "true");
    expect(document.body.style.cursor).toBe("col-resize");
    fireEvent.pointerMove(handle, { clientX: 150, pointerId: 1 });
    expect(onPreview).toHaveBeenLastCalledWith(350);
    fireEvent.pointerMove(handle, { clientX: 900, pointerId: 1 });
    expect(onPreview).toHaveBeenLastCalledWith(500);
    fireEvent.pointerUp(handle, { pointerId: 1 });
    expect(onCommit).toHaveBeenCalledWith(500);
    expect(handle).not.toHaveAttribute("data-dragging");
    expect(document.body.style.cursor).toBe("");

    fireEvent.lostPointerCapture(handle);
    expect(onCommit).toHaveBeenCalledTimes(1);
  });

  it("ignores moves without a drag and secondary buttons", () => {
    const { handle, onPreview, onCommit } = setup();
    fireEvent.pointerMove(handle, { clientX: 150 });
    fireEvent.pointerDown(handle, { button: 2, clientX: 100 });
    fireEvent.pointerMove(handle, { clientX: 150 });
    fireEvent.pointerUp(handle);
    expect(onPreview).not.toHaveBeenCalled();
    expect(onCommit).not.toHaveBeenCalled();
  });

  it("finishes the drag when capture is lost", () => {
    const { handle, onCommit } = setup();
    fireEvent.pointerDown(handle, { button: 0, clientX: 100 });
    fireEvent.pointerMove(handle, { clientX: 80 });
    fireEvent.lostPointerCapture(handle);
    expect(onCommit).toHaveBeenCalledWith(280);
  });

  it("resizes from the keyboard and resets on double-click", async () => {
    const { handle, onCommit, onReset } = setup();
    handle.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(onCommit).toHaveBeenLastCalledWith(316);
    await userEvent.keyboard("{Shift>}{ArrowLeft}{/Shift}");
    expect(onCommit).toHaveBeenLastCalledWith(236);
    await userEvent.keyboard("{Home}");
    expect(onCommit).toHaveBeenLastCalledWith(200);
    await userEvent.keyboard("{End}");
    expect(onCommit).toHaveBeenLastCalledWith(500);
    await userEvent.keyboard("a");
    expect(onCommit).toHaveBeenCalledTimes(4);
    await userEvent.dblClick(handle);
    expect(onReset).toHaveBeenCalled();
  });
});

describe("resizer wrappers", () => {
  it("binds a pane size", async () => {
    const size = { width: 300, min: 200, max: 500, preview: vi.fn(), commit: vi.fn(), reset: vi.fn() };
    render(<PaneResizer size={size} label="Resize sidebar" />);
    const handle = screen.getByRole("separator", { name: "Resize sidebar" });
    fireEvent.pointerDown(handle, { button: 0, clientX: 0 });
    fireEvent.pointerMove(handle, { clientX: 10 });
    fireEvent.pointerUp(handle);
    expect(size.preview).toHaveBeenCalledWith(310);
    expect(size.commit).toHaveBeenCalledWith(310);
    await userEvent.dblClick(handle);
    expect(size.reset).toHaveBeenCalled();
  });

  it("binds one column of a table", async () => {
    const sizes = { width: () => 120, min: 48, max: 1200, preview: vi.fn(), commit: vi.fn(), reset: vi.fn() };
    render(<ColumnResizer sizes={sizes} id="path" label="Path" />);
    const handle = screen.getByRole("separator", { name: "Resize Path column" });
    fireEvent.pointerDown(handle, { button: 0, clientX: 0 });
    fireEvent.pointerMove(handle, { clientX: -20 });
    fireEvent.pointerUp(handle);
    expect(sizes.preview).toHaveBeenCalledWith("path", 100);
    expect(sizes.commit).toHaveBeenCalledWith("path", 100);
    await userEvent.dblClick(handle);
    expect(sizes.reset).toHaveBeenCalledWith("path");
  });
});
