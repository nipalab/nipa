import { describe, expect, it } from "vitest";

import type { FileLockDetail, MaintenanceWindow } from "../src/daemon/types";
import { blameLabels, coveringLock, coversPath, hasConflictMarkers, maintenanceNote, nipaLink, relativeAge } from "../src/scm/helpers";

const lock = (path: string, held_by = "u1"): FileLockDetail => ({ id: path, path, branch: "", global: true, held_by, held_by_name: "" });

describe("nipaLink", () => {
  it("builds desktop links like FileLink", () => {
    expect(nipaLink("https://nipa.example.com/acme/game", ["file", "art/hero 1.png"])).toBe("nipa://nipa.example.com/acme/game/file/art/hero%201.png");
    expect(nipaLink("http://localhost:8080/acme/game", ["commits", "abc"])).toBe("nipa://localhost:8080/acme/game/commits/abc");
    expect(nipaLink("http://10.0.0.5:8080/acme/game", [])).toBe("nipa://10.0.0.5:8080/acme/game?server=http%3A%2F%2F10.0.0.5%3A8080");
    expect(nipaLink("https://127.0.0.1/acme/game", [])).toBe("nipa://127.0.0.1/acme/game?server=https%3A%2F%2F127.0.0.1");
  });

  it("refuses unusable addresses", () => {
    expect(nipaLink(undefined, [])).toBeUndefined();
    expect(nipaLink("not a url", [])).toBeUndefined();
    expect(nipaLink("ftp://host/a/b", [])).toBeUndefined();
    expect(nipaLink("https://host/only-org", [])).toBeUndefined();
  });
});

describe("locks", () => {
  it("matches exact paths and directory prefixes only", () => {
    expect(coversPath("art", "art/hero.png")).toBe(true);
    expect(coversPath("/art/", "art")).toBe(true);
    expect(coversPath("art", "artwork/x")).toBe(false);
    expect(coversPath("", "anything")).toBe(true);
  });

  it("prefers the most specific covering lock", () => {
    const locks = [lock("art"), lock("art/hero.png", "u2"), lock("docs")];
    expect(coveringLock(locks, "art/hero.png")?.path).toBe("art/hero.png");
    expect(coveringLock(locks, "art/hero.png", (l) => l.held_by === "u1")?.path).toBe("art");
    expect(coveringLock(locks, "src/a.ts")).toBeUndefined();
  });
});

describe("hasConflictMarkers", () => {
  it("finds marker lines", () => {
    expect(hasConflictMarkers("a\n<<<<<<< mine\nb\n=======\nc\n>>>>>>> theirs\n")).toBe(true);
    expect(hasConflictMarkers(">>>>>>>")).toBe(true);
    expect(hasConflictMarkers("a <<<<<<< not at line start\n<<<<<<<<< eight\n")).toBe(false);
  });
});

describe("relativeAge", () => {
  const now = Date.parse("2026-11-17T12:00:00Z");
  it("scales the unit", () => {
    expect(relativeAge("2026-11-17T11:59:40Z", now)).toBe("now");
    expect(relativeAge("2026-11-17T11:15:00Z", now)).toBe("45 min");
    expect(relativeAge("2026-11-17T02:00:00Z", now)).toBe("10 h");
    expect(relativeAge("2026-11-10T12:00:00Z", now)).toBe("7 d");
    expect(relativeAge("2026-07-17T12:00:00Z", now)).toBe("4 mo");
    expect(relativeAge("2023-11-17T12:00:00Z", now)).toBe("3 y");
    expect(relativeAge("garbage", now)).toBe("");
    expect(relativeAge("2027-01-01T00:00:00Z", now)).toBe("now");
  });
});

describe("blameLabels", () => {
  it("labels each run once and marks unpushed and older lines", () => {
    const commits = [
      { id: "c1", hash: "h", author_name: "Ana", author_email: "", message: "m", created_at: "2026-11-15T12:00:00Z" },
      { id: "c2", hash: "h", author_name: "A very long author name indeed", author_email: "", message: "m", created_at: "bad" },
    ];
    const labels = blameLabels(
      [
        { text: "", commit_id: "c1", uncommitted: false },
        { text: "", commit_id: "c1", uncommitted: false },
        { text: "", commit_id: "", uncommitted: true },
        { text: "", commit_id: "", uncommitted: false },
        { text: "", commit_id: "gone", uncommitted: false },
        { text: "", commit_id: "c2", uncommitted: false },
      ],
      commits,
      Date.parse("2026-11-17T12:00:00Z"),
    );
    expect(labels.map((l) => [l.text, l.first])).toEqual([
      ["Ana · 2 d", true],
      ["Ana · 2 d", false],
      ["Not pushed", true],
      ["Older", true],
      ["Older", true],
      ["A very long author na…", true],
    ]);
    expect(labels[0].commit?.id).toBe("c1");
  });
});

describe("maintenanceNote", () => {
  const w = (mode: string, active: boolean, extra: Partial<MaintenanceWindow> = {}): MaintenanceWindow => ({ mode, active, message: "", org: "", project: "", ...extra });
  it("shows the strictest active window", () => {
    expect(maintenanceNote(undefined)).toBeUndefined();
    expect(maintenanceNote({ incoming: 0, review_requests: 0, error: "", maintenance: null })).toBeUndefined();
    expect(maintenanceNote({ incoming: 0, review_requests: 0, error: "", maintenance: { server: w("full", false), org: null, project: null } })).toBeUndefined();
    const note = maintenanceNote({
      incoming: 0,
      review_requests: 0,
      error: "",
      maintenance: { server: null, org: w("read_only", true), project: w("full", true, { message: "DB move", ends_at: "2026-11-17T13:00:00Z" }) },
    });
    expect(note?.title).toBe("$(tools) closed");
    expect(note?.tooltip).toContain("Project maintenance (closed)\nDB move\nEstimated end");
    expect(maintenanceNote({ incoming: 0, review_requests: 0, error: "", maintenance: { server: w("odd", true), org: null, project: null } })?.title).toBe("$(tools) odd");
  });
});
