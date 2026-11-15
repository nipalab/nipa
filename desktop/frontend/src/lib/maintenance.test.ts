import { describe, expect, it } from "vitest";
import { activeWindow, blockedNote, blocksSync, formatLocal, headline, maintenanceOf, maintenanceText, modeDescription, modeLabel, scopeName, upcomingWindow, type MaintenanceWindow } from "./maintenance";
import { maintenanceState } from "@/hooks/useMaintenance";
import { errorMessage } from "./utils";

const w = (over: Partial<MaintenanceWindow> = {}): MaintenanceWindow => ({ scope: "server", mode: "read_only", active: true, message: "", startsAt: "", endsAt: "", org: "", project: "", ...over });

describe("maintenance helpers", () => {
  it("picks the strictest active window and the earliest scheduled one", () => {
    const server = w({ mode: "read_only" });
    const org = w({ scope: "org", org: "acme", mode: "full" });
    const later = w({ active: false, startsAt: "2026-12-02T00:00:00Z" });
    const sooner = w({ active: false, scope: "project", project: "acme/game", startsAt: "2026-12-01T00:00:00Z" });
    expect(activeWindow([server, org, later])).toBe(org);
    expect(activeWindow([server, w({ scope: "project", mode: "read_only" })])).toBe(server);
    expect(activeWindow([later])).toBeUndefined();
    expect(activeWindow([w({ mode: "odd" })])).toBeUndefined();
    expect(upcomingWindow([server, later, sooner])).toBe(sooner);
    expect(upcomingWindow([server])).toBeUndefined();
  });

  it("names modes and scopes", () => {
    expect([modeLabel("read_only"), modeLabel("full"), modeLabel("frozen")]).toEqual(["read-only", "closed", "frozen"]);
    expect(modeDescription("read_only")).toContain("pushes");
    expect(modeDescription("full")).toContain("cannot be reached");
    expect(modeDescription("frozen")).toContain("Sign-in");
    expect(scopeName(w())).toBe("The server");
    expect(scopeName(w({ scope: "org", org: "acme" }))).toBe("Organization acme");
    expect(scopeName(w({ scope: "project", project: "acme/game" }))).toBe("Project acme/game");
    expect(blocksSync(w())).toBe(false);
    expect(blocksSync(w({ mode: "full" }))).toBe(true);
    expect(blocksSync(w({ mode: "frozen" }))).toBe(true);
    expect(blocksSync(undefined)).toBe(false);
  });

  it("formats headlines and notes", () => {
    expect(formatLocal("")).toBe("");
    expect(formatLocal("nope")).toBe("");
    expect(formatLocal("2026-11-16T09:00:00Z")).not.toBe("");
    expect(headline(w({ mode: "full" }))).toBe("The server is closed for maintenance");
    expect(headline(w({ active: false, startsAt: "2026-11-16T09:00:00Z" }))).toBe(`The server will be read-only for maintenance from ${formatLocal("2026-11-16T09:00:00Z")}`);
    expect(blockedNote(w({ scope: "org", org: "acme" }))).toBe("Not available: Organization acme is read-only for maintenance");
  });

  it("reads maintenance refusals from errors", () => {
    const window = w({ scope: "project", project: "acme/game", active: false, message: "Disk swap", endsAt: "2026-11-16T11:00:00Z" });
    const err = Object.assign(new Error("refused"), { cause: { message: "refused", maintenance: window } });
    expect(maintenanceOf(err)).toEqual({ ...window, active: true });
    expect(maintenanceOf(new Error("plain"))).toBeUndefined();
    expect(maintenanceOf(Object.assign(new Error("x"), { cause: "text" }))).toBeUndefined();
    expect(maintenanceOf(Object.assign(new Error("x"), { cause: { maintenance: {} } }))).toBeUndefined();
    expect(maintenanceOf(null)).toBeUndefined();
    expect(maintenanceOf("text")).toBeUndefined();
    expect(maintenanceText(maintenanceOf(err)!)).toBe(`Project acme/game is read-only for maintenance · Disk swap · Estimated end ${formatLocal(window.endsAt)}`);
    expect(maintenanceText(w())).toBe("The server is read-only for maintenance");
    expect(errorMessage(err)).toBe(maintenanceText(maintenanceOf(err)!));
  });

  it("derives what the clone can do", () => {
    expect(maintenanceState([])).toEqual({ active: undefined, upcoming: undefined, writes: undefined, sync: undefined });
    const readOnly = maintenanceState([w()]);
    expect(readOnly.writes).toBe("Not available: The server is read-only for maintenance");
    expect(readOnly.sync).toBeUndefined();
    const full = maintenanceState([w({ mode: "full" })]);
    expect(full.sync).toBe(full.writes);
  });
});
