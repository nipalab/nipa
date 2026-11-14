import { beforeEach, describe, expect, it } from "vitest";
import { labels, readTour, setMode, startTour, startTourOnce, storedMode, writeTour } from "@/lib/mode";
import { resetSettingsForTest } from "@/lib/settings";
import { simpleTabIds, visibleTabs } from "@/components/tabs";

beforeEach(() => {
  resetSettingsForTest();
  localStorage.clear();
});

describe("mode", () => {
  it("is unset until chosen, then remembered", () => {
    expect(storedMode()).toBeNull();
    setMode("simple");
    expect(storedMode()).toBe("simple");
    setMode("full");
    expect(storedMode()).toBe("full");
  });

  it("ignores garbage values", () => {
    localStorage.setItem("nipa.mode", "weird");
    expect(storedMode()).toBeNull();
  });

  it("renames actions in simple mode", () => {
    expect(labels(true)).toEqual({ update: "Get latest", push: "Submit" });
    expect(labels(false)).toEqual({ update: "Update", push: "Push" });
  });
});

describe("tour", () => {
  it("starts once and survives skipping", () => {
    expect(readTour()).toBeNull();
    startTourOnce();
    expect(readTour()).toBe(0);
    writeTour("done");
    startTourOnce();
    expect(readTour()).toBe("done");
    startTour();
    expect(readTour()).toBe(0);
  });

  it("rejects out-of-range steps", () => {
    localStorage.setItem("nipa.tour", "9");
    expect(readTour()).toBeNull();
  });
});

describe("visibleTabs", () => {
  it("limits simple mode and still shows conflicts", () => {
    expect(visibleTabs(false, true).map((t) => t.id).sort()).toEqual([...simpleTabIds].sort());
    expect(visibleTabs(true, true).map((t) => t.id)).toContain("conflicts");
    expect(visibleTabs(false).map((t) => t.id)).toContain("branches");
  });
});
