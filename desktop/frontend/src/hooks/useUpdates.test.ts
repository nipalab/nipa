import { describe, expect, it } from "vitest";
import { describeUpdate, downloadPercent } from "./useUpdates";
import type { UpdateState } from "@/api/desktop";

const base: UpdateState = { current: "1.0.0", enabled: true, inPlace: true, phase: "idle", release: null, written: 0, total: 0, error: "", checkedAt: "" };
const release = { version: "1.1.0", notes: "", publishedAt: "", page: "" };

describe("describeUpdate", () => {
  it.each<[Partial<UpdateState>, string, string | null]>([
    [{}, "Nipa 1.0.0", "check"],
    [{ phase: "checking" }, "Checking for updates…", null],
    [{ phase: "upToDate" }, "Nipa 1.0.0 is up to date.", "check"],
    [{ phase: "available", release }, "Nipa 1.1.0 is available.", "install"],
    [{ phase: "available", release, inPlace: false }, "Nipa 1.1.0 is available.", "open"],
    [{ phase: "downloading", release, written: 1, total: 3 }, "Downloading Nipa 1.1.0… 33%", null],
    [{ phase: "ready", release }, "Nipa 1.1.0 is ready.", "restart"],
    [{ phase: "error", error: "offline" }, "Could not check for updates: offline", "check"],
    [{ phase: "error", error: "bad signature", release }, "Could not download Nipa 1.1.0: bad signature", "install"],
    [{ phase: "error", error: "x", release, inPlace: false }, "Could not download Nipa 1.1.0: x", "open"],
  ])("%o", (patch, text, action) => {
    const got = describeUpdate({ ...base, ...patch });
    expect(got.text).toBe(text);
    expect(got.action).toBe(action);
  });

  it("computes the download percentage", () => {
    expect(downloadPercent(base)).toBe(0);
    expect(downloadPercent({ ...base, written: 3, total: 4 })).toBe(75);
    expect(downloadPercent({ ...base, written: 9, total: 4 })).toBe(100);
  });
});
