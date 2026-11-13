import { render, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useShortcuts, type ShortcutHandlers } from "./useShortcuts";

function Probe({ handlers, enabled }: { handlers: ShortcutHandlers; enabled?: boolean }) {
  useShortcuts(handlers, enabled);
  return <input aria-label="field" />;
}

describe("useShortcuts", () => {
  it("runs the owned handlers and prevents the default", () => {
    const palette = vi.fn();
    const tab = vi.fn();
    render(<Probe handlers={{ palette, tab }} />);
    const e = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true, cancelable: true });
    document.dispatchEvent(e);
    expect(palette).toHaveBeenCalledTimes(1);
    expect(e.defaultPrevented).toBe(true);
    fireEvent.keyDown(document, { key: "4", ctrlKey: true });
    expect(tab).toHaveBeenCalledWith({ id: "tab", index: 3 }, expect.any(KeyboardEvent));
  });

  it("leaves keys without a handler alone", () => {
    render(<Probe handlers={{ palette: vi.fn() }} />);
    const e = new KeyboardEvent("keydown", { key: "b", ctrlKey: true, bubbles: true, cancelable: true });
    document.dispatchEvent(e);
    expect(e.defaultPrevented).toBe(false);
  });

  it("ignores plain keys typed in a field and uses the latest handlers", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { getByLabelText, rerender } = render(<Probe handlers={{ next: first }} />);
    fireEvent.keyDown(getByLabelText("field"), { key: "j" });
    expect(first).not.toHaveBeenCalled();
    rerender(<Probe handlers={{ next: second }} />);
    fireEvent.keyDown(document.body, { key: "j" });
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("does nothing while disabled", () => {
    const palette = vi.fn();
    render(<Probe handlers={{ palette }} enabled={false} />);
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });
    expect(palette).not.toHaveBeenCalled();
  });
});
