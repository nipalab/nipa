import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Toast } from "./Toast";

afterEach(() => vi.useRealTimers());

describe("Toast", () => {
  it("dismisses success messages on its own", () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(<Toast message="Pushed" tone="success" onDismiss={onDismiss} />);
    expect(screen.getByRole("status")).toHaveTextContent("Pushed");
    act(() => vi.advanceTimersByTime(4000));
    expect(onDismiss).toHaveBeenCalledOnce();
  });

  it("keeps errors until dismissed", () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(<Toast message="Failed" onDismiss={onDismiss} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Failed");
    act(() => vi.advanceTimersByTime(10000));
    expect(onDismiss).not.toHaveBeenCalled();
  });
});
