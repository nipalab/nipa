import { createService, scenarioFrom } from "./service";
import { spectrogramBytes, textureSvg, torusGlb, waveformPeaks } from "./media";
import { ROOT } from "./data";

const ref = (path: string) => ({ source: "working", path });

describe("demo service", () => {
  it("reads the scenario from the query string", () => {
    expect(scenarioFrom("?scenario=conflicts")).toBe("conflicts");
    expect(scenarioFrom("")).toBe("default");
  });

  it("stages, unstages and discards files", async () => {
    const s = createService("default");
    const before = await s.Changes(ROOT);
    const path = before.unstaged![0].path;
    const staged = await s.Stage(ROOT, [path]);
    expect(staged.staged!.map((f) => f.path)).toContain(path);
    const back = await s.Unstage(ROOT, [path]);
    expect(back.unstaged!.map((f) => f.path)).toContain(path);
    const dropped = await s.Discard(ROOT, [path]);
    expect(dropped.unstaged!.map((f) => f.path)).not.toContain(path);
  });

  it("serves diffs and asset comparisons", async () => {
    const s = createService("default");
    const hero = "Assets/Characters/Hero/hero_albedo.png";
    expect((await s.FileDiff(ROOT, "Source/Player/Movement.cs"))!.hunks!.length).toBeGreaterThan(0);
    expect(await s.FileDiff(ROOT, "nope")).toBeNull();
    expect((await s.ImageDiff(ROOT, ref(hero), ref(hero), { maxSize: 1, level: 0, face: 0 }))!.stats!.changedRatio).toBeGreaterThan(0);
    expect((await s.ImageDiff(ROOT, null, ref("Assets/UI/icon_sword.png"), { maxSize: 1, level: 0, face: 0 }))!.old).toBeNull();
    expect(await s.ImageDiff(ROOT, null, null, { maxSize: 1, level: 0, face: 0 })).toBeNull();
    expect((await s.ModelDiff(ROOT, ref("a.glb"), null))!.new).toBeNull();
    expect((await s.AudioDiff(ROOT, null, null))!.new!.peaks).toHaveLength(2);
    expect((await s.TableDiff(ROOT, null, null))!.rowsChanged).toBe(2);
  });

  it("pages history and lists projects, branches, tags and merge requests", async () => {
    const s = createService("default");
    const first = await s.History(ROOT, { branch: "", path: "", start: "", limit: 3 });
    expect(first!.commits).toHaveLength(3);
    const from = await s.History(ROOT, { branch: "", path: "", start: "c5", limit: 0 });
    expect(from!.commits![0].id).toBe("c5");
    expect((await s.Branches(ROOT))!.length).toBeGreaterThan(1);
    expect((await s.Tags(ROOT))!.length).toBeGreaterThan(1);
    expect((await s.MergeRequests(ROOT, { status: "", search: "", author: "", after: 0 }))!.items!.length).toBeGreaterThan(1);
    expect((await s.MergeRequest(ROOT, 41))!.request.number).toBe(41);
    expect((await s.MergeRequest(ROOT, 999))!.request.number).toBe(42);
    expect((await s.MergeRequestActivity(ROOT, 42))!.threads).toHaveLength(1);
    expect((await s.ListProjects("x"))!.length).toBeGreaterThan(1);
    expect((await s.RemoteTree("x", "o", "p", "main", ""))!.dirs!.length).toBeGreaterThan(0);
    expect((await s.RemoteTree("x", "o", "p", "main", "Assets"))!.dirs).toHaveLength(0);
  });

  it("reports push checks for the staged files", async () => {
    const s = createService("default");
    const checks = await s.PushChecks(ROOT);
    expect(checks.violations!.map((v) => v.limit)).toEqual(["max_texture_size", "max_triangles"]);
    expect(checks.broken).toHaveLength(1);
    await s.Unstage(ROOT, ["Assets/Characters/Hero/hero_albedo.png", "Assets/Characters/Hero/ring_of_dawn.glb"]);
    const none = await s.PushChecks(ROOT);
    expect(none.violations).toEqual([]);
    expect(none.broken).toEqual([]);
    expect(none.newlyUnused).toEqual([]);
  });

  it("models write operations", async () => {
    const s = createService("default");
    expect((await s.CreateBranch(ROOT, "x", "", ""))!.name).toBe("x");
    expect((await s.CreateTag(ROOT, "t", "", "", "m"))!.message).toBe("m");
    expect((await s.SwitchBranch(ROOT, "main")).branch).toBe("main");
    expect((await s.Lock(ROOT, "b", ["p"]))!.locked).toEqual(["p"]);
    const input = { path: "f", oldLine: 0, newLine: 3, body: "hi" };
    expect((await s.Comment(ROOT, 42, input))!.comments![0].body).toBe("hi");
    expect((await s.Reply(ROOT, 42, "t1", "yo"))!.body).toBe("yo");
    expect((await s.CreateMergeRequest(ROOT, { title: "T", description: "", source: "a", target: "b", draft: true }))!.draft).toBe(true);
    expect((await s.CreateMergeRequest(ROOT, { title: "T", description: "", source: "a", target: "b", draft: false, assignees: ["u-mina", "u-x"] }))!.assignees).toEqual([
      { id: "u-mina", name: "Mina Okafor" },
      { id: "u-x", name: "u-x" },
    ]);
    expect((await s.EditMergeRequest(ROOT, 42, "N", "D"))!.title).toBe("N");
    expect((await s.SetDraft(ROOT, 42, true))!.draft).toBe(true);
    expect((await s.MergeMergeRequest(ROOT, 42, "ff", false))!.request.status).toBe("merged");
    expect((await s.SaveSettings({ keepInTray: false, startAtLogin: false, pollSeconds: 5, notify: { pushes: true, locks: true, reviews: true }, tools: [], manualUpdates: false })).pollSeconds).toBe(5);
    expect((await s.AddRepo("/x/y")).name).toBe("y");
    expect(await s.TakeNavigation()).toBeNull();
  });

  it("resolves conflicts in the conflict scenario", async () => {
    const s = createService("conflicts");
    const set = await s.Conflicts(ROOT);
    expect(set!.files).toHaveLength(2);
    expect((await s.ConflictTexts(ROOT, set!.files![0].path))!.working.text).toContain("<<<<<<<");
    const after = await s.ResolveConflict(ROOT, { path: set!.files![0].path, choice: "ours", content: "" });
    expect(after.conflicts).toHaveLength(1);
    expect((await createService("default").Conflicts(ROOT))!.files).toHaveLength(0);
  });
});

describe("demo media", () => {
  it("generates deterministic assets", () => {
    expect(textureSvg(["#000", "#111", "#222"], "#fff")).toMatch(/^data:image\/svg\+xml;base64,/);
    const glb = atob(torusGlb(1, 0.3));
    expect(glb.slice(0, 4)).toBe("glTF");
    const peaks = waveformPeaks(1, 10, 1);
    expect(peaks).toHaveLength(20);
    expect(atob(spectrogramBytes(1, 4, 3, 1))).toHaveLength(12);
  });
});

describe("demo wiring", () => {
  it("answers every service method", async () => {
    const s = createService("default");
    const input = { path: "p", oldLine: 0, newLine: 0, body: "b", title: "t", description: "", source: "a", target: "b", draft: false, choice: "ours", content: "", limit: 1, start: "" };
    const calls = Object.values(s) as ((...args: unknown[]) => Promise<unknown>)[];
    expect(calls.length).toBeGreaterThan(75);
    const settled = await Promise.allSettled(calls.map((fn) => fn(ROOT, "main", input, input, "x")));
    expect(settled.filter((r) => r.status === "rejected")).toHaveLength(0);
  });

  it("exposes the default service and a silent event bus", async () => {
    const { Service } = await import("./index");
    expect((await Service.Connect()).started).toBe(true);
    const { Events } = await import("./runtime");
    expect(Events.On("repo:changed", () => {})()).toBeUndefined();
  });
});
