import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSettings, hydrateSettings, readSetting, removeSettings, repoSetting, repoSettingsPrefix, resetSettingsForTest, SAVE_DELAY_MS, writeSetting } from "./settings";

describe("settings", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    resetSettingsForTest();
    vi.useRealTimers();
  });

  it("uses localStorage until hydrated", () => {
    writeSetting("nipa.a", "1");
    expect(readSetting("nipa.a")).toBe("1");
    writeSetting("nipa.a", null);
    expect(readSetting("nipa.a")).toBeNull();
  });

  it("serves the stored state and saves changes after a delay", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    localStorage.setItem("nipa.local", "ignored");
    await hydrateSettings(() => Promise.resolve({ "nipa.selectedRepo": "/work/a" }), save);
    expect(readSetting("nipa.selectedRepo")).toBe("/work/a");
    expect(readSetting("nipa.local")).toBeNull();

    writeSetting("nipa.selectedRepo", "/work/a");
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).not.toHaveBeenCalled();

    writeSetting("nipa.selectedRepo", "/work/b");
    writeSetting("nipa.tab", "files");
    vi.advanceTimersByTime(SAVE_DELAY_MS - 1);
    expect(save).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenLastCalledWith({ "nipa.selectedRepo": "/work/b", "nipa.tab": "files" });
    expect(localStorage.getItem("nipa.tab")).toBe("files");

    writeSetting("nipa.tab", null);
    window.dispatchEvent(new Event("pagehide"));
    expect(save).toHaveBeenLastCalledWith({ "nipa.selectedRepo": "/work/b" });
  });

  it("moves localStorage settings over the first time", async () => {
    const save = vi.fn().mockRejectedValue(new Error("disk full"));
    localStorage.setItem("nipa.layout.sidebar", "300");
    localStorage.setItem("other", "x");
    await hydrateSettings(() => Promise.resolve(null), save);
    expect(readSetting("nipa.layout.sidebar")).toBe("300");
    expect(readSetting("other")).toBeNull();
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).toHaveBeenCalledWith({ "nipa.layout.sidebar": "300" });
    await expect(flushSettings()).resolves.toBeUndefined();
  });

  it("stays on localStorage when the stored state cannot be read", async () => {
    const save = vi.fn();
    await hydrateSettings(() => Promise.reject(new Error("no service")), save);
    writeSetting("nipa.a", "1");
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).not.toHaveBeenCalled();
    expect(readSetting("nipa.a")).toBe("1");
    await expect(flushSettings()).resolves.toBeUndefined();
  });

  it("removes a clone's settings", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const a = repoSetting("/work/a", "tab");
    const b = repoSetting("/work/b", "tab");
    localStorage.setItem(a, '"files"');
    await hydrateSettings(() => Promise.resolve({ [a]: '"files"', [b]: '"history"' }), save);
    removeSettings(repoSettingsPrefix("/work/a"));
    expect(readSetting(a)).toBeNull();
    expect(readSetting(b)).toBe('"history"');
    expect(localStorage.getItem(a)).toBeNull();
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).toHaveBeenCalledWith({ [b]: '"history"' });
    removeSettings(repoSettingsPrefix("/work/none"));
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).toHaveBeenCalledTimes(1);
  });

  it("removes localStorage settings before hydration", () => {
    localStorage.setItem(repoSetting("/r", "tab"), "x");
    removeSettings(repoSettingsPrefix("/r"));
    expect(localStorage.getItem(repoSetting("/r", "tab"))).toBeNull();
  });
});
