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
  PushChecks: vi.fn(),
  DiskUsage: vi.fn(),
  CleanUp: vi.fn(),
  LLMSettings: vi.fn(),
  SetLLM: vi.fn(),
  SetLLMKey: vi.fn(),
  ClearLLMKey: vi.fn(),
  TestLLM: vi.fn(),
  GenerateCommitMessage: vi.fn(),
  GenerateMergeRequest: vi.fn(),
  LockStatus: vi.fn(),
  Lock: vi.fn(),
  Unlock: vi.fn(),
  PickFile: vi.fn(),
  OpenFile: vi.fn(),
  RevealFile: vi.fn(),
  Files: vi.fn(),
  FileInfo: vi.fn(),
  FileView: vi.fn(),
  FileLink: vi.fn(),
  RestoreRevision: vi.fn(),
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
  Merge: vi.fn(),
  AbortMerge: vi.fn(),
  Conflicts: vi.fn(),
  ConflictTexts: vi.fn(),
  ConflictDiff: vi.fn(),
  ResolveConflict: vi.fn(),
  History: vi.fn(),
  CommitFiles: vi.fn(),
  Branches: vi.fn(),
  CreateBranch: vi.fn(),
  DeleteBranch: vi.fn(),
  SwitchBranch: vi.fn(),
  Tags: vi.fn(),
  CreateTag: vi.fn(),
  DeleteTag: vi.fn(),
  CheckoutTag: vi.fn(),
  Revert: vi.fn(),
  MergeRequests: vi.fn(),
  MergeRequest: vi.fn(),
  MergeRequestActivity: vi.fn(),
  MergeRequestCommits: vi.fn(),
  RevisionFiles: vi.fn(),
  CreateMergeRequest: vi.fn(),
  EditMergeRequest: vi.fn(),
  SetDraft: vi.fn(),
  MergeMergeRequest: vi.fn(),
  CloseMergeRequest: vi.fn(),
  ReopenMergeRequest: vi.fn(),
  SubmitReview: vi.fn(),
  Comment: vi.fn(),
  Reply: vi.fn(),
  ResolveThread: vi.fn(),
  Activity: vi.fn(),
  Settings: vi.fn(),
  SaveSettings: vi.fn(),
  UploadWorkers: vi.fn(),
  SetUploadWorkers: vi.fn(),
  RestartDaemon: vi.fn(),
  ChooseProgram: vi.fn(),
  IgnoreRules: vi.fn(),
  SaveIgnoreRules: vi.fn(),
  OpenLink: vi.fn(),
  TakeNavigation: vi.fn(),
  UpdateStatus: vi.fn(),
  CheckForUpdate: vi.fn(),
  InstallUpdate: vi.fn(),
  RestartToUpdate: vi.fn(),
  OpenUpdatePage: vi.fn(),
}));
vi.mock("../../bindings/github.com/nipalab/nipa/desktop/internal/service", () => ({ Service: service }));

const events = vi.hoisted(() => ({ On: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({ Events: events }));

import { desktop, updateState } from "./desktop";

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
    const empty = { branch: "main", detached: undefined, pending: undefined, pendingRemaining: 0, staged: [], unstaged: [], conflicts: [], resolved: [] };
    expect(await desktop.changes("/a")).toEqual(empty);

    const full = { branch: "dev", detached: "v1", pending: "merge", pendingRemaining: 0, staged: [{ path: "x" }], unstaged: [{ path: "y" }], conflicts: ["z"], resolved: ["z"] };
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

  it("loads push checks with empty lists for nulls", async () => {
    const violation = { path: "a.png", rule: "textures", limit: "power_of_two", actual: "6x4", max: "", message: "not a power of two" };
    service.PushChecks.mockResolvedValueOnce(null).mockResolvedValueOnce({
      violations: [violation],
      broken: null,
      newlyUnused: ["old.png"],
      warnings: null,
      truncated: true,
      scanned: 4,
    });
    expect(await desktop.pushChecks("/a")).toEqual({ truncated: false, scanned: 0, violations: [], broken: [], newlyUnused: [], warnings: [] });
    expect(await desktop.pushChecks("/a")).toEqual({ truncated: true, scanned: 4, violations: [violation], broken: [], newlyUnused: ["old.png"], warnings: [] });
    expect(service.PushChecks).toHaveBeenCalledWith("/a");
  });

  it("reports disk usage and cleans up", async () => {
    service.DiskUsage.mockResolvedValueOnce({ workingFiles: 2, workingBytes: 10, cacheObjects: 1, cacheBytes: 4, pins: null, sparse: null })
      .mockResolvedValueOnce({ workingFiles: 0, workingBytes: 0, cacheObjects: 0, cacheBytes: 0, pins: ["art"], sparse: ["code"] })
      .mockResolvedValueOnce(null);
    expect(await desktop.diskUsage("/a")).toEqual({ workingFiles: 2, workingBytes: 10, cacheObjects: 1, cacheBytes: 4, pins: [], sparse: [] });
    expect(await desktop.diskUsage("/a")).toEqual({ workingFiles: 0, workingBytes: 0, cacheObjects: 0, cacheBytes: 0, pins: ["art"], sparse: ["code"] });
    await expect(desktop.diskUsage("/a")).rejects.toThrow("No disk usage");
    expect(service.DiskUsage).toHaveBeenCalledWith("/a");
    service.CleanUp.mockResolvedValueOnce({ objects: 3, bytes: 9, skipped: false, reason: "" }).mockResolvedValueOnce(null);
    expect(await desktop.cleanUp("/a")).toEqual({ objects: 3, bytes: 9, skipped: false, reason: "" });
    await expect(desktop.cleanUp("/a")).rejects.toThrow("No clean-up result");
    expect(service.CleanUp).toHaveBeenCalledWith("/a");
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

  it("browses files and restores versions", async () => {
    service.Files.mockResolvedValueOnce({ dir: "a", entries: null }).mockResolvedValueOnce(null);
    expect(await desktop.files("/r", "a")).toEqual({ dir: "a", entries: [] });
    expect(await desktop.files("/r", "b")).toEqual({ dir: "b", entries: [] });
    service.FileInfo.mockResolvedValueOnce({ entry: { path: "a/x" }, lastCommit: null }).mockResolvedValueOnce(null);
    expect((await desktop.fileInfo("/r", "a/x")).entry.path).toBe("a/x");
    await expect(desktop.fileInfo("/r", "a/x")).rejects.toThrow("No file information");
    service.FileView.mockResolvedValue(null);
    expect(await desktop.fileView("/r", "a/x")).toBeNull();
    service.FileLink.mockResolvedValue("nipa://h/o/p/file/a/x");
    expect(await desktop.fileLink("https://h/o/p", "a/x")).toBe("nipa://h/o/p/file/a/x");
    service.RevealFile.mockResolvedValue(undefined);
    await desktop.revealFile("/r", "a/x");
    expect(service.RevealFile).toHaveBeenCalledWith("/r", "a/x");
    service.RestoreRevision.mockResolvedValue({ commitId: "c1" });
    expect(await desktop.restoreRevision("/r", "a/x", "c1")).toEqual({ commitId: "c1" });
    expect(service.RestoreRevision).toHaveBeenCalledWith("/r", "a/x", "c1");
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

  it("pages history and normalises missing lists", async () => {
    service.History.mockResolvedValueOnce({ next: "c1", commits: [{ id: "c2", parents: null }] }).mockResolvedValueOnce(null);
    expect(await desktop.history("/a", { path: "art" })).toEqual({ next: "c1", commits: [{ id: "c2", parents: [] }] });
    expect(service.History).toHaveBeenCalledWith("/a", { branch: "", path: "art", start: "", limit: 0 });
    expect(await desktop.history("/a", {})).toEqual({ next: "", commits: [] });

    service.CommitFiles.mockResolvedValue(null);
    service.Branches.mockResolvedValue(null);
    service.Tags.mockResolvedValue(null);
    expect(await desktop.commitFiles("/a", "c2", "c1")).toEqual([]);
    expect(await desktop.branches("/a")).toEqual([]);
    expect(await desktop.tags("/a")).toEqual([]);
    expect(service.CommitFiles).toHaveBeenCalledWith("/a", "c2", "c1");
  });

  it("creates and deletes branches and tags", async () => {
    service.CreateBranch.mockResolvedValueOnce({ name: "fix" }).mockResolvedValueOnce(null);
    service.CreateTag.mockResolvedValueOnce({ name: "v1" }).mockResolvedValueOnce(null);
    expect(await desktop.createBranch("/a", "fix", { commit: "c2" })).toEqual({ name: "fix" });
    expect(service.CreateBranch).toHaveBeenLastCalledWith("/a", "fix", "", "c2");
    await expect(desktop.createBranch("/a", "fix")).rejects.toThrow("not created");
    expect(service.CreateBranch).toHaveBeenLastCalledWith("/a", "fix", "", "");
    expect(await desktop.createTag("/a", "v1", { branch: "main" }, "ship")).toEqual({ name: "v1" });
    expect(service.CreateTag).toHaveBeenLastCalledWith("/a", "v1", "main", "", "ship");
    await expect(desktop.createTag("/a", "v1")).rejects.toThrow("not created");
    expect(service.CreateTag).toHaveBeenLastCalledWith("/a", "v1", "", "", "");

    await desktop.deleteBranch("/a", "fix");
    await desktop.deleteTag("/a", "v1");
    expect(service.DeleteBranch).toHaveBeenCalledWith("/a", "fix");
    expect(service.DeleteTag).toHaveBeenCalledWith("/a", "v1");
  });

  it("starts switch, tag checkout and revert operations", async () => {
    service.SwitchBranch.mockResolvedValue({ branch: "fix" });
    service.CheckoutTag.mockResolvedValue({ branch: "main" });
    service.Revert.mockResolvedValue({ revert: { committed: true } });
    expect(await desktop.switchBranch("/a", "fix")).toEqual({ branch: "fix" });
    expect(await desktop.checkoutTag("/a", "v1")).toEqual({ branch: "main" });
    expect(await desktop.revert("/a", { mode: "abort" })).toEqual({ revert: { committed: true } });
    expect(service.Revert).toHaveBeenCalledWith("/a", { target: "", mode: "abort" });
  });

  it("reports a pending revert in the change set", async () => {
    service.Changes.mockResolvedValue({ branch: "main", pending: "revert", pendingRemaining: 2, staged: null, unstaged: null, conflicts: null });
    expect(await desktop.changes("/a")).toMatchObject({ pending: "revert", pendingRemaining: 2, conflicts: [] });
    service.Changes.mockResolvedValue({ branch: "main", pending: "other" });
    expect(await desktop.changes("/a")).toMatchObject({ pending: undefined, pendingRemaining: 0 });
  });

  it("normalises merge requests and their activity", async () => {
    service.MergeRequests.mockResolvedValueOnce({ next: 4, items: [{ number: 5, assignees: null }] }).mockResolvedValueOnce(null);
    expect(await desktop.mergeRequests("/a", { status: "open" })).toEqual({ next: 4, items: [{ number: 5, assignees: [] }] });
    expect(service.MergeRequests).toHaveBeenCalledWith("/a", { status: "open", search: "", author: "", after: 0 });
    expect(await desktop.mergeRequests("/a", {})).toEqual({ next: 0, items: [] });

    service.MergeRequest.mockResolvedValueOnce({ request: { number: 5, assignees: null }, mergeability: { status: "mergeable" }, outstanding: null }).mockResolvedValueOnce(null);
    expect(await desktop.mergeRequest("/a", 5)).toEqual({ request: { number: 5, assignees: [] }, mergeability: { status: "mergeable" }, outstanding: [] });
    await expect(desktop.mergeRequest("/a", 6)).rejects.toThrow("not found");

    service.MergeRequestActivity.mockResolvedValueOnce({ reviews: null, threads: [{ id: "t", comments: null }], timeline: null, checks: null }).mockResolvedValueOnce(null);
    expect(await desktop.mergeRequestActivity("/a", 5)).toEqual({ reviews: [], threads: [{ id: "t", comments: [] }], timeline: [], checks: [] });
    expect(await desktop.mergeRequestActivity("/a", 5)).toEqual({ reviews: [], threads: [], timeline: [], checks: [] });

    service.MergeRequestCommits.mockResolvedValueOnce([{ id: "c", parents: null }]).mockResolvedValueOnce(null);
    expect(await desktop.mergeRequestCommits("/a", 5)).toEqual([{ id: "c", parents: [] }]);
    expect(await desktop.mergeRequestCommits("/a", 5)).toEqual([]);
    service.RevisionFiles.mockResolvedValue(null);
    expect(await desktop.revisionFiles("/a", "b", "s")).toEqual([]);
  });

  it("forwards merge request actions", async () => {
    service.CreateMergeRequest.mockResolvedValueOnce({ number: 7, assignees: null }).mockResolvedValueOnce(null);
    expect(await desktop.createMergeRequest("/a", { title: "t", description: "", source: "a", target: "b", draft: false })).toEqual({ number: 7, assignees: [] });
    await expect(desktop.createMergeRequest("/a", { title: "t", description: "", source: "a", target: "b", draft: false })).rejects.toThrow("No merge request");
    service.EditMergeRequest.mockResolvedValue({ number: 7 });
    service.SetDraft.mockResolvedValue({ number: 7 });
    service.MergeMergeRequest.mockResolvedValue({ request: { number: 7 }, mergeability: {}, outstanding: [] });
    expect((await desktop.editMergeRequest("/a", 7, "t", "d")).number).toBe(7);
    expect((await desktop.setDraft("/a", 7, true)).number).toBe(7);
    expect((await desktop.mergeMergeRequest("/a", 7, "ff", false)).request.number).toBe(7);
    await desktop.closeMergeRequest("/a", 7);
    await desktop.reopenMergeRequest("/a", 7);
    await desktop.submitReview("/a", 7, "approved", "");
    await desktop.resolveThread("/a", 7, "t", true);
    expect(service.MergeMergeRequest).toHaveBeenCalledWith("/a", 7, "ff", false);
    expect(service.ResolveThread).toHaveBeenCalledWith("/a", 7, "t", true);

    service.Comment.mockResolvedValue({ id: "t", comments: null });
    expect(await desktop.comment("/a", 7, { body: "hi" })).toEqual({ id: "t", comments: [] });
    expect(service.Comment).toHaveBeenCalledWith("/a", 7, { path: "", oldLine: 0, newLine: 0, body: "hi" });
    service.Reply.mockResolvedValueOnce({ id: "c" }).mockResolvedValueOnce(null);
    expect(await desktop.reply("/a", 7, "t", "ok")).toEqual({ id: "c" });
    await expect(desktop.reply("/a", 7, "t", "ok")).rejects.toThrow("No reply");
  });

  it("merges and resolves conflicts", async () => {
    service.Merge.mockResolvedValue({ merge: { conflicts: ["a"] } });
    service.AbortMerge.mockResolvedValue({ merge: { aborted: true } });
    expect(await desktop.merge("/a", "feature")).toEqual({ merge: { conflicts: ["a"] } });
    expect(service.Merge).toHaveBeenCalledWith("/a", "feature");
    expect(await desktop.abortMerge("/a")).toEqual({ merge: { aborted: true } });

    service.Conflicts.mockResolvedValueOnce(null);
    expect(await desktop.conflicts("/a")).toEqual({ op: "", base: { id: "", label: "" }, ours: { id: "", label: "" }, theirs: { id: "", label: "" }, files: [] });
    const full = { op: "revert", base: { id: "b", label: "x" }, ours: { id: "o", label: "main" }, theirs: { id: "t", label: "y" }, files: [{ path: "a" }] };
    service.Conflicts.mockResolvedValueOnce(full);
    expect(await desktop.conflicts("/a")).toEqual(full);
    service.Conflicts.mockResolvedValueOnce({ op: "other", files: null });
    expect((await desktop.conflicts("/a")).op).toBe("");

    service.ConflictTexts.mockResolvedValueOnce({ base: {} }).mockResolvedValueOnce(null);
    expect(await desktop.conflictTexts("/a", "f")).toEqual({ base: {} });
    await expect(desktop.conflictTexts("/a", "f")).rejects.toThrow("No file versions");
    service.ConflictDiff.mockResolvedValue(null);
    expect(await desktop.conflictDiff("/a", "f")).toBeNull();

    service.ResolveConflict.mockResolvedValue({ branch: "main", conflicts: ["f"], resolved: ["f"] });
    expect((await desktop.resolveConflict("/a", "f", "content", "x")).resolved).toEqual(["f"]);
    expect(service.ResolveConflict).toHaveBeenCalledWith("/a", { path: "f", choice: "content", content: "x" });
    await desktop.resolveConflict("/a", "f", "ours");
    expect(service.ResolveConflict).toHaveBeenLastCalledWith("/a", { path: "f", choice: "ours", content: "" });
  });

  it("forwards AI settings and generation calls", async () => {
    const llm = { endpoint: "http://localhost:11434/v1", model: "qwen", keySaved: true, configured: true };
    service.LLMSettings.mockResolvedValueOnce(llm).mockResolvedValueOnce(null);
    expect(await desktop.llmSettings()).toEqual(llm);
    await expect(desktop.llmSettings()).rejects.toThrow("No AI settings was returned");
    service.SetLLM.mockResolvedValueOnce(llm);
    expect(await desktop.setLLM("http://localhost:11434/v1", "qwen")).toEqual(llm);
    expect(service.SetLLM).toHaveBeenCalledWith("http://localhost:11434/v1", "qwen");
    await desktop.setLLMKey("sk");
    expect(service.SetLLMKey).toHaveBeenCalledWith("sk");
    await desktop.clearLLMKey();
    expect(service.ClearLLMKey).toHaveBeenCalled();
    await desktop.testLLM("http://x", "m", "");
    expect(service.TestLLM).toHaveBeenCalledWith("http://x", "m", "");

    const draft = { title: "", text: "feat: a", model: "qwen", source: "", target: "" };
    service.GenerateCommitMessage.mockResolvedValueOnce(draft).mockResolvedValueOnce(null);
    expect(await desktop.generateCommitMessage("/a")).toEqual(draft);
    await expect(desktop.generateCommitMessage("/a")).rejects.toThrow("No draft");
    service.GenerateMergeRequest.mockResolvedValueOnce({ ...draft, title: "T" });
    expect((await desktop.generateMergeRequest("/a", "feature", "main")).title).toBe("T");
    expect(service.GenerateMergeRequest).toHaveBeenCalledWith("/a", "feature", "main");
  });

  it("forwards settings, activity and navigation calls", async () => {
    const st = { keepInTray: true, startAtLogin: false, pollSeconds: 60, notify: { pushes: true, locks: true, reviews: true }, tools: null, manualUpdates: false };
    service.Settings.mockResolvedValue(st);
    service.SaveSettings.mockResolvedValue({ ...st, tools: [{ extension: ".blend", command: "blender" }] });
    expect(await desktop.settings()).toEqual({ ...st, tools: [] });
    expect((await desktop.saveSettings({ ...st, tools: [] })).tools).toEqual([{ extension: ".blend", command: "blender" }]);
    expect(service.SaveSettings).toHaveBeenCalledWith({ ...st, tools: [] });

    service.Activity.mockResolvedValueOnce(null).mockResolvedValueOnce([{ root: "/a", events: null }]);
    expect(await desktop.activity()).toEqual([]);
    expect(await desktop.activity()).toEqual([{ root: "/a", events: [] }]);

    service.UploadWorkers.mockResolvedValue(4);
    expect(await desktop.uploadWorkers()).toBe(4);
    await desktop.setUploadWorkers(8);
    expect(service.SetUploadWorkers).toHaveBeenCalledWith(8);
    await desktop.restartDaemon();
    await desktop.chooseProgram();
    expect(service.RestartDaemon).toHaveBeenCalled();
    expect(service.ChooseProgram).toHaveBeenCalled();
    await desktop.ignoreRules("/a");
    await desktop.saveIgnoreRules("/a", { shared: "x", local: "" });
    expect(service.IgnoreRules).toHaveBeenCalledWith("/a");
    expect(service.SaveIgnoreRules).toHaveBeenCalledWith("/a", { shared: "x", local: "" });
    await desktop.openLink("nipa://h/o/p");
    await desktop.takeNavigation();
    expect(service.OpenLink).toHaveBeenCalledWith("nipa://h/o/p");
    expect(service.TakeNavigation).toHaveBeenCalled();
  });

  it("subscribes to activity and navigation events", () => {
    const activity = vi.fn();
    const navigate = vi.fn();
    desktop.onRepoActivity(activity);
    desktop.onNavigate(navigate);
    expect(events.On.mock.calls.map((c) => c[0])).toEqual(["repo:activity", "app:navigate"]);
    events.On.mock.calls[0][1]({ data: { root: "/a", events: null } });
    events.On.mock.calls[1][1]({ data: { root: "/a" } });
    expect(activity).toHaveBeenCalledWith({ root: "/a", events: [] });
    expect(navigate).toHaveBeenCalledWith();
  });

  it("normalizes and forwards update calls", async () => {
    const empty = { current: "", enabled: false, inPlace: false, phase: "idle", release: null, written: 0, total: 0, error: "", checkedAt: "" };
    expect(updateState(null)).toEqual(empty);
    expect(updateState({ current: "1.0.0", enabled: true, inPlace: true, phase: "bogus", written: 1, total: 2 })).toEqual({
      ...empty,
      current: "1.0.0",
      enabled: true,
      inPlace: true,
      written: 1,
      total: 2,
    });
    const release = { version: "1.1.0", notes: "", publishedAt: "", page: "" };
    service.UpdateStatus.mockResolvedValue({ ...empty, phase: "available", release });
    service.CheckForUpdate.mockResolvedValue({ ...empty, phase: "upToDate" });
    service.InstallUpdate.mockResolvedValue({ ...empty, phase: "ready", release, error: "x" });
    expect((await desktop.updateStatus()).release).toEqual(release);
    expect((await desktop.checkForUpdate()).phase).toBe("upToDate");
    expect(await desktop.installUpdate()).toMatchObject({ phase: "ready", error: "x" });
    await desktop.restartToUpdate();
    await desktop.openUpdatePage();
    expect(service.RestartToUpdate).toHaveBeenCalled();
    expect(service.OpenUpdatePage).toHaveBeenCalled();

    const fn = vi.fn();
    events.On.mockClear();
    desktop.onUpdate(fn);
    expect(events.On.mock.calls[0][0]).toBe("update:state");
    events.On.mock.calls[0][1]({ data: { phase: "ready", current: "1.0.0" } });
    expect(fn).toHaveBeenCalledWith({ ...empty, phase: "ready", current: "1.0.0" });
  });
});
