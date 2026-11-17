import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readSetting, resetSettingsForTest, writeSetting } from "@/lib/settings";
import { applyStoredZoom, formatZoom, nextZoom, setZoom, storedZoom, useZoom } from "./zoom";

const api = vi.hoisted(() => ({ setZoom: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

beforeEach(() => {
  vi.resetAllMocks();
  resetSettingsForTest();
  localStorage.clear();
  api.setZoom.mockResolvedValue(undefined);
});

describe("zoom", () => {
  it("steps through the levels and stops at the ends", () => {
    expect(nextZoom(1, 1)).toBe(1.1);
    expect(nextZoom(1, -1)).toBe(0.9);
    expect(nextZoom(0.67, -1)).toBe(0.5);
    expect(nextZoom(0.5, -1)).toBe(0.5);
    expect(nextZoom(2, 1)).toBe(2);
    expect(nextZoom(1.3, 1)).toBe(1.5);
  });

  it("formats percentages", () => {
    expect(formatZoom(0.67)).toBe("67%");
    expect(formatZoom(1.25)).toBe("125%");
  });

  it("reads the stored level, snapped to a step", () => {
    expect(storedZoom()).toBe(1);
    writeSetting("nipa.zoom", "1.24");
    expect(storedZoom()).toBe(1.25);
    writeSetting("nipa.zoom", "nope");
    expect(storedZoom()).toBe(1);
    writeSetting("nipa.zoom", "-3");
    expect(storedZoom()).toBe(1);
  });

  it("saves, applies and notifies subscribers", () => {
    const { result } = renderHook(() => useZoom());
    expect(result.current).toBe(1);
    act(() => void setZoom(1.5));
    expect(result.current).toBe(1.5);
    expect(readSetting("nipa.zoom")).toBe("1.5");
    expect(api.setZoom).toHaveBeenLastCalledWith(1.5);
    act(() => void setZoom(1));
    expect(readSetting("nipa.zoom")).toBeNull();
    expect(api.setZoom).toHaveBeenLastCalledWith(1);
  });

  it("ignores a failing window call", async () => {
    api.setZoom.mockRejectedValue(new Error("no window"));
    expect(setZoom(0.8)).toBe(0.8);
    await Promise.resolve();
  });

  it("restores a stored level at startup only when it is not the default", () => {
    applyStoredZoom();
    expect(api.setZoom).not.toHaveBeenCalled();
    writeSetting("nipa.zoom", "0.9");
    applyStoredZoom();
    expect(api.setZoom).toHaveBeenCalledWith(0.9);
  });
});
