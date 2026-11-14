import { createService, scenarioFrom } from "./service";
import { spectrogramBytes, textureSvg, torusGlb, waveformPeaks } from "./media";
import * as d from "./data";
import { ROOT } from "./data";

const ref = (path: string) => ({ source: "working", path });

describe("demo service", () => {
  it("answers references and provenance", async () => {
    const s = createService("default");
    const rock = await s.AssetRefs(ROOT, "Assets/Textures/Environment/rock_albedo.png");
    expect(rock.asset).toBe(true);
    expect(rock.usedBy).toEqual(["Assets/Materials/Rock.mat"]);
    const cave = await s.AssetRefs(ROOT, "Assets/Levels/Cave/Cave.unity");
    expect(cave.uses!.filter((r) => !r.resolved)).toHaveLength(1);
    expect((await s.AssetRefs(ROOT, "Assets/Textures/Environment/bark_albedo.png")).usedBy).toEqual([]);
    expect((await s.AssetRefs(ROOT, "README.md")).asset).toBe(false);
    const prov = await s.Provenance(ROOT, ["Assets/Characters/Hero/hero_albedo.png", "Assets/UI/icon_sword.png", "README.md"]);
    expect(prov.origins!.map((o) => o.status)).toEqual(["edited", "missing"]);
    const saved = await s.SetProvenance(ROOT, { path: "Assets/UI/icon_sword.png", tool: " Krita ", model: "", seed: "4", license: "", prompt: "sword", note: "", humanEdited: true });
    expect(saved.origin).toMatchObject({ status: "recorded", tool: "Krita", seed: "4", source: "manual", required: true });
    expect(saved.origin.promptHash).toHaveLength(64);
    expect((await s.Provenance(ROOT, ["Assets/UI/icon_sword.png"])).origins![0].status).toBe("recorded");
    expect((await s.Provenance(ROOT, null)).origins).toEqual([]);
  });

  it("searches the demo project's files", async () => {
    const s = createService("default");
    const res = (await s.SearchFiles(ROOT, "hero alb", 5))!;
    expect(res.hits![0]).toMatchObject({ path: "Assets/Characters/Hero/hero_albedo.png", name: "hero_albedo.png", kind: "image", binary: true });
    expect((await s.SearchFiles(ROOT, "e", 3))!.hits).toHaveLength(3);
    expect((await s.SearchFiles(ROOT, "e", 0))!.hits!.length).toBeGreaterThan(3);
    expect((await s.SearchFiles(ROOT, "movement", 5))!.hits![0]).toMatchObject({ kind: "text", binary: false });
    expect((await s.SearchFiles("/elsewhere", "hero", 5))!.hits).toEqual([]);
    const all = (await s.MergeRequests(ROOT, { status: "", search: "", author: "", after: 0 }))!.items!.length;
    const some = (await s.MergeRequests(ROOT, { status: "", search: "HERO", author: "", after: 0 }))!.items!;
    expect(some.length).toBeGreaterThan(0);
    expect(some.length).toBeLessThan(all);
  });

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
    expect((await s.TableDiff(ROOT, null, null, ""))!.rowsChanged).toBe(2);
    const book = (await s.TableDiff(ROOT, null, ref("Assets/Data/balance.xlsx"), ""))!;
    expect(book.sheet).toBe("Weapons");
    expect(book.cellsChanged).toBe(6);
    expect(book.sheets!.map((x) => x.status)).toEqual(["changed", "changed", "added", "same", "removed"]);
    const armor = (await s.TableDiff(ROOT, null, ref("Assets/Data/balance.xlsx"), "Armor"))!;
    expect(armor.rowsRemoved).toBe(1);
    expect((await s.TableDiff(ROOT, ref("a.xlsx"), null, "Nope"))!.sheet).toBe("Weapons");
  });

  it("blames demo files and merges scenes", async () => {
    const s = createService("default");
    const working = await s.Blame(d.ROOT, d.MOVE, "");
    expect(working?.lines ?? []).toHaveLength(32);
    expect(working?.lines?.[20]).toEqual({ commitId: "", uncommitted: true });
    expect(working?.lines?.[0].commitId).toBe("c2");
    expect(working?.commits?.map((c) => c.id)).toEqual(["c7", "c3", "c2"]);
    const pushed = await s.Blame(d.ROOT, d.MOVE, "c11");
    expect(pushed?.lines?.[20]).toEqual({ commitId: "c11", uncommitted: false });
    expect((await s.Blame(d.ROOT, "nothing.txt", ""))?.lines).toEqual([]);
    expect((await s.MergeScene(d.ROOT, "a.tscn"))?.clean).toBe(true);
    await expect(s.OpenFileAt(d.ROOT, d.MOVE, 3)).resolves.toBeUndefined();
  });

  it("browses the project files", async () => {
    const s = createService("default");
    const top = await s.Files(ROOT, "");
    expect(top!.entries!.map((e) => e.name)).toEqual(["Assets", "ProjectSettings", "Source", ".nipaassets.yml", ".nipaignore", "README.md"]);
    expect(top!.entries![0].childrenChanged).toBe(true);
    const hero = await s.Files(ROOT, "Assets/Characters/Hero");
    const albedo = hero!.entries!.find((e) => e.name === "hero_albedo.png")!;
    expect(albedo).toMatchObject({ kind: "image", status: "modified", staged: true });
    const ui = await s.Files(ROOT, "Assets/UI");
    expect(ui!.entries!.find((e) => e.name === "icon_sword.png")).toMatchObject({ status: "untracked", tracked: false });
    expect((await s.FileInfo(ROOT, "main", "Assets/Characters/Hero/hero_albedo.png"))!.lastCommit!.id).toBe("c12");
    expect((await s.FileInfo(ROOT, "main", "Assets/UI/icon_sword.png"))!.lastCommit).toBeNull();
    expect((await s.FileInfo(ROOT, "main", "README.md"))!.lastCommit).not.toBeNull();
    expect((await s.FileInfo(ROOT, "main", "nope.txt"))!.entry.path).toBe("nope.txt");
    expect((await s.FileView(ROOT, "Source/Player/Movement.cs"))!.hunks![0].lines!.length).toBeGreaterThan(5);
    expect((await s.FileView(ROOT, "Source/Other.cs"))!.hunks![0].lines).toHaveLength(1);
    expect((await s.FileView(ROOT, "Assets/Characters/Hero/ring_of_dawn.glb"))!.kind).toBe("model");
    const thumb = await s.ImageDiff(ROOT, null, ref("Assets/UI/icon_shield.png"), { maxSize: 128, level: 0, face: 0 });
    expect(thumb!.old).toBeNull();
    expect(thumb!.new!.src).toMatch(/^data:image\/svg/);
    expect(await s.FileLink("x", "a/b.png")).toBe("nipa://nipalab.com/acme/starfall/file/a/b.png");
    expect(await s.RestoreRevision(ROOT, "a", "c3")).toEqual({ commitId: "c3" });
    await s.RevealFile(ROOT, "a");
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

  it("reports disk usage and cleans up", async () => {
    const s = createService("default");
    const usage = await s.DiskUsage(ROOT);
    expect(usage.workingBytes).toBeGreaterThan(20 * 1024 ** 3);
    expect(usage.sparse).toEqual([]);
    expect((await s.DiskUsage("/home/alex/studio/starfall-audio")).sparse).toEqual(["Music", "Sfx"]);
    expect((await s.DiskUsage("/elsewhere")).sparse).toEqual([]);
    expect((await s.CleanUp(ROOT)).skipped).toBe(false);
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

  it("models AI settings and drafts", async () => {
    const s = createService("default");
    expect((await s.LLMSettings())!.configured).toBe(true);
    expect((await s.SetLLM("", ""))!.configured).toBe(false);
    await s.SetLLMKey("k");
    expect((await s.LLMSettings())!.keySaved).toBe(true);
    await s.ClearLLMKey();
    expect((await s.LLMSettings())!.keySaved).toBe(false);
    await s.TestLLM("http://x", "m", "");
    expect((await s.GenerateCommitMessage(ROOT))!.text).toContain("hero_albedo.png");
    const mr = (await s.GenerateMergeRequest(ROOT, "", ""))!;
    expect(mr.title).not.toBe("");
    expect([mr.source, mr.target]).toEqual(["art/hero-pass", "main"]);
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
