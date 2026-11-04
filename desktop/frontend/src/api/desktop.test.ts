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
  ModelDiff: vi.fn(),
  AudioDiff: vi.fn(),
  TableDiff: vi.fn(),
  MetaWarnings: vi.fn(),
  LockStatus: vi.fn(),
  Lock: vi.fn(),
  Unlock: vi.fn(),
  PickFile: vi.fn(),
  OpenFile: vi.fn(),
  AccountStatus: vi.fn(),
  SignIn: vi.fn(),
  SignInSSH: vi.fn(),
  ListProjects: vi.fn(),
  RemoteBranches: vi.fn(),
  RemoteTree: vi.fn(),
  ChooseFolder: vi.fn(),
  Clone: vi.fn(),
  SetSparse: vi.fn(),
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

  it("loads models", async () => {
    service.ModelDiff.mockResolvedValue({ old: null, new: null });
    const ref = { source: "working", path: "a.glb" };
    expect(await desktop.modelDiff("/a", null, ref)).toEqual({ old: null, new: null });
    expect(service.ModelDiff).toHaveBeenCalledWith("/a", null, ref);
  });

  it("loads audio, tables and Unity warnings", async () => {
    const ref = { source: "working", path: "a.wav" };
    service.AudioDiff.mockResolvedValue({ old: null, new: null });
    service.TableDiff.mockResolvedValue({ rows: [] });
    service.MetaWarnings.mockResolvedValueOnce(null).mockResolvedValueOnce([{ path: "Assets/a" }]);
    expect(await desktop.audioDiff("/a", ref, null)).toEqual({ old: null, new: null });
    expect(await desktop.tableDiff("/a", null, ref)).toEqual({ rows: [] });
    expect(await desktop.metaWarnings("/a")).toEqual([]);
    expect(await desktop.metaWarnings("/a")).toEqual([{ path: "Assets/a" }]);
    expect(service.AudioDiff).toHaveBeenCalledWith("/a", ref, null);
    expect(service.TableDiff).toHaveBeenCalledWith("/a", null, ref);
  });

  it("locks, releases and opens files", async () => {
    service.LockStatus.mockResolvedValueOnce({ userId: "me", branch: "main", locks: null, files: null }).mockResolvedValueOnce(null);
    expect(await desktop.lockStatus("/a")).toEqual({ userId: "me", branch: "main", locks: [], files: [] });
    await expect(desktop.lockStatus("/a")).rejects.toThrow("No lock information");
    service.Lock.mockResolvedValueOnce({ locked: null, failed: null }).mockResolvedValueOnce({ locked: ["x"], failed: [] });
    expect(await desktop.lock("/a", "main", ["x"])).toEqual({ locked: [], failed: [] });
    expect(await desktop.lock("/a", "main", ["x"])).toEqual({ locked: ["x"], failed: [] });
    expect(service.Lock).toHaveBeenCalledWith("/a", "main", ["x"]);
    service.Unlock.mockResolvedValue(undefined);
    await desktop.unlock("/a", "", "x");
    expect(service.Unlock).toHaveBeenCalledWith("/a", "", "x");
    service.PickFile.mockResolvedValue("art/a.psd");
    expect(await desktop.pickFile("/a")).toBe("art/a.psd");
    service.OpenFile.mockResolvedValue(undefined);
    await desktop.openFile("/a", "art/a.psd");
    expect(service.OpenFile).toHaveBeenCalledWith("/a", "art/a.psd");
  });

  it("signs in, browses servers and clones", async () => {
    service.AccountStatus.mockResolvedValueOnce({ signedIn: true }).mockResolvedValueOnce(null);
    expect(await desktop.accountStatus("h")).toEqual({ signedIn: true });
    await expect(desktop.accountStatus("h")).rejects.toThrow("No account information");
    service.SignIn.mockResolvedValue(undefined);
    service.SignInSSH.mockResolvedValue(undefined);
    await desktop.signIn("h", "a", "p");
    await desktop.signInSSH("h");
    expect(service.SignIn).toHaveBeenCalledWith("h", "a", "p");
    expect(service.SignInSSH).toHaveBeenCalledWith("h", "");
    service.ListProjects.mockResolvedValueOnce(null);
    service.RemoteBranches.mockResolvedValueOnce(null);
    expect(await desktop.listProjects("h")).toEqual([]);
    expect(await desktop.remoteBranches("h", "o", "p")).toEqual([]);
    service.RemoteTree.mockResolvedValueOnce(null).mockResolvedValueOnce({ dirs: [{ name: "a", path: "a" }], files: 1, fileBytes: 2 });
    expect(await desktop.remoteTree("h", "o", "p", "", "")).toEqual({ dirs: [], files: 0, fileBytes: 0 });
    expect(await desktop.remoteTree("h", "o", "p", "main", "a")).toEqual({ dirs: [{ name: "a", path: "a" }], files: 1, fileBytes: 2 });
    expect(service.RemoteTree).toHaveBeenLastCalledWith("h", "o", "p", "main", "a");
    service.ChooseFolder.mockResolvedValue("/x");
    expect(await desktop.chooseFolder("t")).toBe("/x");
    const req = { server: "h", org: "o", project: "p", branch: "", sparse: [], target: "/t" };
    service.Clone.mockResolvedValue({ root: "/t" });
    expect(await desktop.clone(req)).toEqual({ root: "/t" });
    service.SetSparse.mockResolvedValue({ branch: "main" });
    expect(await desktop.setSparse("/t", ["a"])).toEqual({ branch: "main" });
    expect(service.SetSparse).toHaveBeenCalledWith("/t", ["a"]);
  });

  it("asks for image previews at 2048 px and the first surface by default", async () => {
    service.ImageDiff.mockResolvedValue({ old: null, new: null, stats: null });
    const ref = { source: "working", path: "a.png" };
    await desktop.imageDiff("/a", null, ref);
    expect(service.ImageDiff).toHaveBeenCalledWith("/a", null, ref, { maxSize: 2048, level: 0, face: 0 });
    await desktop.imageDiff("/a", ref, null, { level: 2, face: 1 });
    expect(service.ImageDiff).toHaveBeenLastCalledWith("/a", ref, null, { maxSize: 2048, level: 2, face: 1 });
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
