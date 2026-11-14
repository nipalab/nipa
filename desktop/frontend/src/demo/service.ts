import type { CancellablePromise } from "@wailsio/runtime";
import type * as Real from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/service";
import type { FileChange, FileDiff, ImageRef, OpOutcome } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import * as d from "./data";
import { demoFileInfo, demoFiles, demoFileView, demoSearch, fileImages } from "./files";
import { demoProvenance, demoRefs } from "./insight";
import { torusGlb } from "./media";

const ok = <T>(v: T): CancellablePromise<T> => Promise.resolve(v) as unknown as CancellablePromise<T>;
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v));

export const scenarioFrom = (search: string): string => new URLSearchParams(search).get("scenario") ?? "default";

export function createService(scenario: string): typeof Real {
  let state = clone(d.changes(scenario));
  const lockWatches = new Map<string, { branch: string; path: string }>();
  let llm = clone(d.llmSettings);
  let shelves = clone(d.shelves);
  const provenance = demoProvenance();
  const take = (list: FileChange[], paths: string[] | null) => list.filter((f) => (paths ?? []).includes(f.path));
  const without = (list: FileChange[], paths: string[] | null) => list.filter((f) => !(paths ?? []).includes(f.path));
  const done: OpOutcome = { branch: state.branch };

  const diffFor = (path: string): FileDiff | null => d.diffs[path] ?? null;
  const imageFor = (ref: ImageRef | null) => {
    if (!ref) return undefined;
    const known = d.images[ref.path];
    if (known) return known;
    const single = fileImages[ref.path];
    return single ? { old: single, new: single } : undefined;
  };

  return {
    Connect: () => ok({ version: "1.4.0", pid: 4242, port: 7070, started: true }),
    Repos: () => ok(clone(d.repos)),
    PickRepo: () => ok(null),
    AddRepo: (path) => ok({ root: path, name: path.split("/").pop() ?? path, url: "", branch: "main", sparse: null }),
    RemoveRepo: () => ok(undefined),
    Clone: () => ok(d.repos[0]),
    Navigate: () => ok(undefined),

    Changes: () => ok(clone(state)),
    Stage: (_root, paths) => {
      const moved = take(state.unstaged, paths);
      state = { ...state, staged: [...state.staged, ...moved], unstaged: without(state.unstaged, paths) };
      return ok(clone(state));
    },
    Unstage: (_root, paths) => {
      const moved = take(state.staged, paths);
      state = { ...state, unstaged: [...state.unstaged, ...moved], staged: without(state.staged, paths) };
      return ok(clone(state));
    },
    Discard: (_root, paths) => {
      state = { ...state, unstaged: without(state.unstaged, paths) };
      return ok(clone(state));
    },
    FileDiff: (_root, path) => ok(diffFor(path)),
    ImageDiff: (_root, old, next) => {
      const entry = imageFor(next) ?? imageFor(old);
      if (!entry) return ok(null);
      const changed = entry.old && entry.new ? 0.37 : 1;
      return ok({
        old: old ? entry.old : null,
        new: next ? entry.new : null,
        stats: {
          changedPixels: Math.round(512 * 512 * changed),
          totalPixels: 512 * 512,
          changedRatio: changed,
          maxDelta: 211,
          boundsX: 0,
          boundsY: 0,
          boundsWidth: 512,
          boundsHeight: 512,
          sizeChanged: false,
          formatChanged: false,
          bitDepthChanged: false,
          alphaChanged: false,
        },
      });
    },
    ModelDiff: (_root, old, next) =>
      ok({
        old: old ? modelSide(old.path, 0.95, 0.28, 3200) : null,
        new: next ? modelSide(next.path, 0.95, 0.34, 4800) : null,
      }),
    AudioDiff: () => ok({ old: clone(d.audio.old), new: clone(d.audio.new) }),
    VideoDiff: (_root, old, next) => ok({ old: old ? clone(d.video.old) : null, new: next ? clone(d.video.new) : null }),
    FontDiff: (_root, old, next) => ok({ old: old ? clone(d.font.old) : null, new: next ? clone(d.font.new) : null, changes: old && next ? clone(d.font.changes) : null }),
    OpenMedia: () => ok(undefined),
    HasMedia: () => ok(true),
    TableDiff: (_root, old, next, sheet) => ok(/\.xlsx$/i.test((next ?? old)?.path ?? "") ? d.workbook(sheet) : clone(d.table)),
    SceneDiff: () => ok(clone(d.scene)),
    MetaWarnings: () => ok([{ path: d.ICON, kind: "missing", related: `${d.ICON}.meta`, message: "Missing .meta file: Unity will generate a new GUID" }]),

    PushChecks: () => ok(d.pushChecks(state.staged.map((f) => f.path))),
    DiskUsage: (root) => ok(d.diskUsage(root)),
    CleanUp: () => ok(clone(d.cleanUp)),
    LockStatus: () => ok(d.locks()),
    Lock: (_root, _branch, paths) => ok({ locked: paths ?? [], failed: [] }),
    Unlock: () => ok(undefined),
    RequestRelease: () => ok(undefined),
    LockWatches: () => ok([...lockWatches.values()]),
    WatchLock: (_root, branch, path) => {
      lockWatches.set(`${branch}\0${path}`, { branch: String(branch), path: String(path) });
      return ok(undefined);
    },
    UnwatchLock: (_root, branch, path) => {
      lockWatches.delete(`${branch}\0${path}`);
      return ok(undefined);
    },
    PickFile: () => ok(""),
    OpenFile: () => ok(undefined),
    RevealFile: () => ok(undefined),
    Files: (_root, dir) => ok(demoFiles(dir, state)),
    FileInfo: (_root, _branch, path) => ok(demoFileInfo(String(path), state)),
    FileView: (_root, path) => ok(demoFileView(path)),
    SearchFiles: (root, query, limit) => ok(root === d.ROOT ? demoSearch(query, limit) : { hits: [], truncated: false }),
    FileLink: (_url, path) => ok(`nipa://${d.SERVER}/acme/starfall/file/${path}`),
    RestoreRevision: (_root, _path, commitId) => ok({ commitId }),
    AssetRefs: (_root, path) => ok(demoRefs(path)),
    Provenance: (_root, paths) => ok(provenance.read(Array.isArray(paths) ? paths : [])),
    SetProvenance: (_root, input) => ok(provenance.set(input)),

    AccountStatus: (server) => ok({ host: d.SERVER, address: server, signedIn: true, email: "alex@acme.dev", userId: d.people.me.id }),
    SignIn: () => ok({ mfaRequired: false, challenge: "", expiresIn: 0 }),
    SignInMFA: () => ok(undefined),
    SignInSSH: () => ok(undefined),
    ListProjects: () => ok(clone(d.projects)),
    RemoteBranches: () => ok(d.branches.map((b) => ({ name: b.name, isDefault: b.default, protected: b.protected }))),
    RemoteTree: (_s, _o, _p, _b, path) =>
      ok(
        path
          ? { dirs: [], files: 184, fileBytes: 2_400_000_000 }
          : {
              dirs: [
                { name: "Assets", path: "Assets" },
                { name: "Source", path: "Source" },
                { name: "ProjectSettings", path: "ProjectSettings" },
                { name: "Packages", path: "Packages" },
              ],
              files: 12,
              fileBytes: 48_000,
            },
      ),
    ChooseFolder: () => ok("/home/alex/studio"),
    ChooseProgram: () => ok("/usr/bin/gimp"),
    SetSparse: () => ok(done),

    Push: () => ok(done),
    Update: () => ok(done),
    SwitchBranch: (_root, branch) => ok({ branch }),
    CheckoutTag: () => ok(done),
    Merge: () => ok(done),
    AbortMerge: () => ok(done),
    Revert: () => ok(done),

    History: (_root, q) => {
      const limit = q.limit || 50;
      const from = q.start ? d.commits.findIndex((c) => c.id === q.start) : 0;
      const page = d.commits.slice(Math.max(0, from), Math.max(0, from) + limit);
      return ok({ commits: clone(page), next: "" });
    },
    CommitFiles: () => ok(clone(d.commitDiffs())),
    RevisionFiles: () => ok(clone(d.commitDiffs())),
    Branches: () => ok(clone(d.branches)),
    CreateBranch: (_root, name) => ok({ name, commitId: "c12", default: false, protected: false }),
    DeleteBranch: () => ok(undefined),
    Tags: () => ok(clone(d.tags)),
    CreateTag: (_root, name, _b, _c, message) => ok({ name, commitId: "c12", message, createdAt: d.ago(0), createdBy: d.people.me.name }),
    DeleteTag: () => ok(undefined),

    MergeRequests: (_root, f) => {
      const q = String(f?.search ?? "").toLowerCase();
      return ok({ items: clone(d.mergeRequests).filter((m) => !q || `${m.title} ${m.description}`.toLowerCase().includes(q)), next: 0 });
    },
    MergeRequest: (_root, n) => {
      const request = d.mergeRequests.find((m) => m.number === n) ?? d.mergeRequests[0];
      return ok({ request: clone(request), mergeability: { status: "mergeable", blockedBy: "", sourceCommitId: "c12", targetCommitId: "c11", mergeBaseId: "c8" }, outstanding: [] });
    },
    MergeRequestActivity: (_root, n) => ok(d.activityFor(Number(n) || 0)),
    MergeRequestCommits: () => ok(clone(d.commits.slice(0, 3))),
    CreateMergeRequest: (_root, input) =>
      ok({
        ...d.mergeRequests[0],
        title: input.title,
        source: input.source,
        target: input.target,
        draft: input.draft,
        assignees: (input.assignees ?? []).map((id) => Object.values(d.people).find((p) => p.id === id) ?? { id, name: id }),
      }),
    EditMergeRequest: (_root, n, title, description) => ok({ ...(d.mergeRequests.find((m) => m.number === n) ?? d.mergeRequests[0]), title, description }),
    SetDraft: (_root, n, draft) => ok({ ...(d.mergeRequests.find((m) => m.number === n) ?? d.mergeRequests[0]), draft }),
    MergeMergeRequest: (root, n) => ok({ request: { ...d.mergeRequests[0], number: n, status: "merged" }, mergeability: { status: "merged", blockedBy: "", sourceCommitId: "c12", targetCommitId: "c12", mergeBaseId: "c8" }, outstanding: [] }),
    CloseMergeRequest: () => ok(undefined),
    ReopenMergeRequest: () => ok(undefined),
    SubmitReview: () => ok(undefined),
    Comment: (_root, _n, input) =>
      ok({
        id: "t-new",
        path: input.path,
        oldLine: input.oldLine,
        newLine: input.newLine,
        outdated: false,
        resolved: false,
        resolvedBy: "",
        createdAt: d.ago(0),
        comments: [{ id: "k-new", author: d.people.me, body: input.body, createdAt: d.ago(0), edited: false }],
      }),
    Reply: (_root, _n, _t, body) => ok({ id: "k-reply", author: d.people.me, body, createdAt: d.ago(0), edited: false }),
    ResolveThread: () => ok(undefined),

    Shelves: () => ok({ shelves: clone(shelves), applying: "" }),
    Shelve: (_root, input) => {
      const picked = [...state.staged, ...state.unstaged].filter((f) => input.paths?.includes(f.path));
      const entries = picked.map((f) => ({ path: f.path, status: f.status === "untracked" ? "added" : f.status, staged: state.staged.includes(f), untracked: f.status === "untracked", locked: false, size: 0, binary: f.kind !== "text", kind: f.kind }));
      const shelf = { id: `demo${shelves.length}`, name: input.name || `${entries.length} files on ${state.branch}`, createdAt: d.ago(0), branch: state.branch, size: 0, entries };
      shelves = [shelf, ...shelves];
      state = { ...state, staged: without(state.staged, input.paths), unstaged: without(state.unstaged, input.paths) };
      return ok({ shelf });
    },
    ApplyShelf: (_root, id, keep) => {
      const shelf = shelves.find((s) => s.id === id);
      if (!shelf) return ok({});
      const back = (shelf.entries ?? []).map((e) => ({ path: e.path, status: e.status, kind: e.kind }));
      state = { ...state, unstaged: [...without(state.unstaged, back.map((f) => f.path)), ...back] };
      if (!keep) shelves = shelves.filter((s) => s.id !== id);
      return ok({ shelfApply: { shelfId: id, name: shelf.name, conflicts: [], dropped: !keep, aborted: false } });
    },
    AbortShelfApply: () => ok({ shelfApply: { shelfId: "", name: "", conflicts: [], dropped: false, aborted: true } }),
    DropShelf: (_root, id) => {
      shelves = shelves.filter((s) => s.id !== id);
      return ok(undefined);
    },
    ShelfFiles: (_root, id) => {
      const shelf = shelves.find((s) => s.id === id);
      const files = (shelf?.entries ?? []).flatMap((e) => {
        const diff = diffFor(e.path);
        return diff ? [{ ...clone(diff), oldSource: "shelf_base", newSource: "shelf", oldRevision: id, newRevision: id }] : [];
      });
      return ok(files);
    },
    SyncPlan: () => ok({ overwritten: [] }),

    Conflicts: () => ok(scenario === "conflicts" ? clone(d.conflictSet) : { op: "", base: { id: "", label: "" }, ours: { id: "", label: "" }, theirs: { id: "", label: "" }, files: [] }),
    ConflictTexts: () => ok(clone(d.conflictTexts)),
    ConflictDiff: (_root, path) => ok(diffFor(path)),
    MergeScene: () => ok(clone(d.sceneMerge)),
    Blame: (_root, path, revision) => ok(d.blame(path, revision)),
    OpenFileAt: () => ok(undefined),
    ResolveConflict: (_root, input) => {
      state = { ...state, conflicts: state.conflicts.filter((p) => p !== input.path), resolved: [...state.resolved, input.path] };
      return ok(clone(state));
    },

    Activity: () =>
      ok([
        {
          root: d.ROOT,
          name: "starfall",
          incoming: 3,
          reviewRequests: 1,
          error: "",
          events: [{ kind: "pushed", actor: "Mina Okafor", branch: "main", commitId: "c11", message: "Add dash ability to player movement", count: 3, path: "", number: 0 }],
        },
      ]),
    Settings: () => ok({ keepInTray: true, startAtLogin: false, pollSeconds: 30, notify: { pushes: true, locks: true, reviews: true, lockRequests: true }, tools: [{ extension: ".psd", command: "/usr/bin/photopea" }, { extension: ".blend", command: "/usr/bin/blender" }], manualUpdates: false }),
    SaveSettings: (s) => ok(s),
    UIState: () => ok({}),
    SaveUIState: () => ok(undefined),
    UploadWorkers: () => ok(16),
    SetUploadWorkers: () => ok(undefined),
    RestartDaemon: () => ok({ version: "1.4.0", pid: 4243, port: 7070, started: true }),
    LLMSettings: () => ok(clone(llm)),
    SetLLM: (endpoint, model) => {
      llm = { ...llm, endpoint, model, configured: endpoint !== "" && model !== "" };
      return ok(clone(llm));
    },
    SetLLMKey: () => {
      llm = { ...llm, keySaved: true };
      return ok(undefined);
    },
    ClearLLMKey: () => {
      llm = { ...llm, keySaved: false };
      return ok(undefined);
    },
    TestLLM: () => ok(undefined),
    GenerateCommitMessage: () => ok(d.commitDraft(state.staged.map((f) => f.path))),
    GenerateMergeRequest: (_root, source, target) => ok(d.mrDraft(source, target)),
    IgnoreRules: () => ok({ shared: "# Unity\nLibrary/\nTemp/\nLogs/\n*.csproj\n", local: "*.tmp\n.vscode/\n" }),
    SaveIgnoreRules: () => ok(undefined),
    OpenLink: () => ok({ root: d.ROOT, tab: "changes", number: 0, commit: "", server: "", org: "", project: "", path: "", action: "", branch: "" }),
    TakeNavigation: () => ok(null),
    UpdateStatus: () => ok({ current: "1.4.0", enabled: true, inPlace: true, phase: "upToDate", release: null, written: 0, total: 0, checkedAt: d.ago(5) }),
    CheckForUpdate: () => ok({ current: "1.4.0", enabled: true, inPlace: true, phase: "upToDate", release: null, written: 0, total: 0, checkedAt: d.ago(0) }),
    InstallUpdate: () => ok({ current: "1.4.0", enabled: true, inPlace: true, phase: "ready", release: null, written: 0, total: 0 }),
    RestartToUpdate: () => ok(undefined),
    OpenUpdatePage: () => ok(undefined),
  } as typeof Real;
}

const modelSide = (path: string, radius: number, tube: number, triangles: number) => ({
  path,
  format: "glb",
  size: triangles * 200,
  stats: {
    generator: "Blender 4.2",
    meshes: 1,
    primitives: 1,
    vertices: Math.round(triangles / 2),
    triangles,
    materials: ["Gold"],
    textures: 2,
    nodes: 1,
    skins: 0,
    joints: 0,
    animations: [],
    morphTargets: 0,
    min: [-1.2, -0.3, -1.2],
    max: [1.2, 0.3, 1.2],
  },
  data: torusGlb(radius, tube),
  files: null,
  missing: null,
});

