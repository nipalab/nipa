import { beforeEach, describe, expect, it, vi } from "vitest";

const service = vi.hoisted(() => ({
  Connect: vi.fn(),
  Repos: vi.fn(),
  PickRepo: vi.fn(),
  AddRepo: vi.fn(),
  RemoveRepo: vi.fn(),
  Changes: vi.fn(),
  Stage: vi.fn(),
  Unstage: vi.fn(),
  Discard: vi.fn(),
  FileDiff: vi.fn(),
  ImageDiff: vi.fn(),
  Push: vi.fn(),
  Update: vi.fn(),
}));
vi.mock("../../bindings/github.com/nipalab/nipa/desktop/internal/service", () => ({ Service: service }));

const events = vi.hoisted(() => ({ On: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({ Events: events }));

import { desktop } from "./desktop";

beforeEach(() => vi.resetAllMocks());

describe("desktop api", () => {
  it("forwards calls to the bound service", async () => {
    service.Connect.mockResolvedValue({ version: "v1" });
    service.PickRepo.mockResolvedValue(null);
    service.AddRepo.mockResolvedValue({ root: "/a" });
    service.RemoveRepo.mockResolvedValue(undefined);

    expect(await desktop.connect()).toEqual({ version: "v1" });
    expect(await desktop.pickRepo()).toBeNull();
    expect(await desktop.addRepo("/a/sub")).toEqual({ root: "/a" });
    await desktop.removeRepo("/a");
    expect(service.AddRepo).toHaveBeenCalledWith("/a/sub");
    expect(service.RemoveRepo).toHaveBeenCalledWith("/a");
  });

  it("normalises a null repository list", async () => {
    service.Repos.mockResolvedValueOnce(null).mockResolvedValueOnce([{ root: "/a" }]);
    expect(await desktop.repos()).toEqual([]);
    expect(await desktop.repos()).toEqual([{ root: "/a" }]);
  });

  it("fills missing change lists", async () => {
    service.Changes.mockResolvedValue({ branch: "main", detached: "", staged: null, unstaged: null, conflicts: null });
    const empty = { branch: "main", detached: undefined, staged: [], unstaged: [], conflicts: [] };
    expect(await desktop.changes("/a")).toEqual(empty);

    const full = { branch: "dev", detached: "v1", staged: [{ path: "x" }], unstaged: [{ path: "y" }], conflicts: ["z"] };
    service.Stage.mockResolvedValue(full);
    service.Unstage.mockResolvedValue(full);
    service.Discard.mockResolvedValue(full);
    expect(await desktop.stage("/a", ["y"])).toEqual(full);
    expect(await desktop.unstage("/a", ["x"])).toEqual(full);
    expect(await desktop.discard("/a", ["y"])).toEqual(full);
    expect(service.Stage).toHaveBeenCalledWith("/a", ["y"]);
    expect(service.Unstage).toHaveBeenCalledWith("/a", ["x"]);
    expect(service.Discard).toHaveBeenCalledWith("/a", ["y"]);
  });

  it("forwards diffs and operations", async () => {
    service.FileDiff.mockResolvedValue(null);
    service.Push.mockResolvedValue({ commitId: "c1" });
    service.Update.mockResolvedValue({ branch: "main" });
    expect(await desktop.fileDiff("/a", "x")).toBeNull();
    expect(await desktop.push("/a", "msg")).toEqual({ commitId: "c1" });
    expect(await desktop.update("/a")).toEqual({ branch: "main" });
    expect(service.Push).toHaveBeenCalledWith("/a", "msg");
  });

  it("asks for image previews at 2048 px by default", async () => {
    service.ImageDiff.mockResolvedValue({ old: null, new: null, stats: null });
    const ref = { source: "working", path: "a.png" };
    await desktop.imageDiff("/a", null, ref);
    expect(service.ImageDiff).toHaveBeenCalledWith("/a", null, ref, 2048);
  });

  it("subscribes to change and progress events", () => {
    const off = vi.fn();
    events.On.mockReturnValue(off);
    const changed = vi.fn();
    const progress = vi.fn();
    expect(desktop.onRepoChanged(changed)).toBe(off);
    desktop.onOpProgress(progress);

    expect(events.On.mock.calls.map((c) => c[0])).toEqual(["repo:changed", "op:progress"]);
    events.On.mock.calls[0][1]({ data: { root: "/a", paths: null } });
    events.On.mock.calls[1][1]({ data: { root: "/a", op: "push" } });
    expect(changed).toHaveBeenCalledWith({ root: "/a", paths: null });
    expect(progress).toHaveBeenCalledWith({ root: "/a", op: "push" });
  });
});
