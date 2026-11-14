import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  flushSettings,
  hydrateSettings,
  MAX_ENTRIES,
  MAX_ENTRY_BYTES,
  readSetting,
  removeSettings,
  repoSetting,
  repoSettingsPrefix,
  resetSettingsForTest,
  SAVE_DELAY_MS,
  writeSetting,
} from "./settings";

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

  it("moves localStorage settings over the first time and reports failed saves", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const save = vi.fn().mockRejectedValue(new Error("disk full"));
    localStorage.setItem("nipa.layout.sidebar", "300");
    localStorage.setItem("other", "x");
    await hydrateSettings(() => Promise.resolve(null), save);
    expect(readSetting("nipa.layout.sidebar")).toBe("300");
    expect(readSetting("other")).toBeNull();
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).toHaveBeenCalledWith({ "nipa.layout.sidebar": "300" });
    await expect(flushSettings()).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledWith("Could not save the interface state", expect.any(Error));
    warn.mockRestore();
  });

  it("keeps loading after the timeout and replays what changed meanwhile", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    let answer!: (v: Record<string, string>) => void;
    const ready = hydrateSettings(() => new Promise((res) => (answer = res)), save, { timeoutMs: 100 });
    vi.advanceTimersByTime(100);
    await ready;
    expect(readSetting("nipa.tab")).toBeNull();
    writeSetting("nipa.tab", '"files"');
    writeSetting("nipa.gone", null);
    removeSettings(repoSettingsPrefix("/old"));
    writeSetting(repoSetting("/old", "x"), "y");
    removeSettings(repoSettingsPrefix("/old"));
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).not.toHaveBeenCalled();

    answer({ "nipa.tab": '"history"', "nipa.gone": "1", "nipa.keep": "k", [repoSetting("/old", "tab")]: '"x"' });
    await vi.waitFor(() => expect(readSetting("nipa.keep")).toBe("k"));
    expect(readSetting("nipa.tab")).toBe('"files"');
    expect(readSetting("nipa.gone")).toBeNull();
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).toHaveBeenCalledWith({ "nipa.tab": '"files"', "nipa.keep": "k" });
  });

  it("retries a failed load", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    const load = vi.fn().mockRejectedValueOnce(new Error("starting")).mockResolvedValue({ "nipa.a": "1" });
    const ready = hydrateSettings(load, save, { timeoutMs: 10_000, retryDelayMs: 50 });
    await vi.advanceTimersByTimeAsync(50);
    await ready;
    expect(load).toHaveBeenCalledTimes(2);
    expect(readSetting("nipa.a")).toBe("1");
  });

  it("stays on localStorage when the stored state cannot be read", async () => {
    const save = vi.fn();
    const load = vi.fn().mockRejectedValue(new Error("no service"));
    const ready = hydrateSettings(load, save, { timeoutMs: 10_000, retries: 2, retryDelayMs: 10 });
    await vi.advanceTimersByTimeAsync(30);
    await ready;
    expect(load).toHaveBeenCalledTimes(3);
    writeSetting("nipa.a", "1");
    vi.advanceTimersByTime(SAVE_DELAY_MS);
    expect(save).not.toHaveBeenCalled();
    expect(readSetting("nipa.a")).toBe("1");
    await expect(flushSettings()).resolves.toBeUndefined();
  });

  it("leaves out entries the service would refuse", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const save = vi.fn().mockResolvedValue(undefined);
    const many: Record<string, string> = { "nipa.big": "x".repeat(MAX_ENTRY_BYTES) };
    for (let i = 0; i <= MAX_ENTRIES; i++) many[`nipa.k${i}`] = "v";
    await hydrateSettings(() => Promise.resolve(many), save);
    await flushSettings();
    const sent = save.mock.calls[0][0] as Record<string, string>;
    expect(sent["nipa.big"]).toBeUndefined();
    expect(Object.keys(sent)).toHaveLength(MAX_ENTRIES);
    expect(warn).toHaveBeenCalledWith("Not saving interface setting nipa.big: too large");
    warn.mockRestore();
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
