import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DaemonSession } from "../src/daemon/session";
import { BlameController } from "../src/scm/blame";
import { fileTargets, pickAndSwitch } from "../src/scm/commands";
import { Model } from "../src/scm/model";
import type { NipaRepository } from "../src/scm/repository";
import { blobUri, parseBlobUri, SnapshotContentProvider } from "../src/scm/snapshot";
import { FakeDaemon, startFakeDaemon, status } from "./fakes/daemon";
import { CancellationTokenSource, fake, fakeEditor, Uri } from "./fakes/vscode";

let daemon: FakeDaemon;
let root: string;
let session: DaemonSession;
let snapshots: SnapshotContentProvider;
let model: Model;
let logs: string[];
let current: Record<string, unknown>;
let lockFiles: unknown[];
let locks: unknown[];
let lockWarnings: boolean;

const waitFor = (check: () => boolean) => vi.waitFor(() => expect(check()).toBe(true), { timeout: 2000, interval: 5 });
const calls = (method: string) => daemon.calls.filter((c) => c.method === method);

beforeEach(async () => {
  fake.reset();
  daemon = await startFakeDaemon();
  root = await fs.mkdtemp(path.join(os.tmpdir(), "nipa-p2-"));
  await fs.mkdir(path.join(root, ".nipa"));
  await fs.writeFile(path.join(root, ".nipa", "config"), "{}");
  current = status({ modified: ["src/a.txt"] });
  lockFiles = [];
  locks = [];
  lockWarnings = true;
  daemon.on("WatchRepo", (body) => ({ json: { repo: { root: body.root, url: "https://nipa.example.com/acme/game", branch: "feature", sparse: [] } } }));
  daemon.on("Status", () => ({ json: current }));
  daemon.on("ResolveConflict", (body) => ({ json: { ...current, resolved: [body.path] } }));
  daemon.on("WatchChanges", () => ({ lines: [], hold: true }));
  daemon.on("WatchEvents", () => ({ lines: [], hold: true }));
  daemon.on("LockStatus", () => ({ json: { user_id: "me", branch: "feature", default_branch: false, can_release_any: false, locks, files: lockFiles } }));
  daemon.on("ProxyLockFile", () => ({ json: { response: {}, warning: "" } }));
  daemon.on("ProxyUnlockFile", () => ({ json: {} }));
  daemon.on("ProxyLockRequestRelease", () => ({ json: {} }));
  daemon.on("SyncPlan", () => ({ json: { overwritten: [] } }));
  daemon.on("Update", () => ({ lines: [{ result: { sync: { branch: "feature", commit_id: "c2" } } }] }));
  daemon.on("Switch", () => ({ lines: [{ result: { sync: { branch: "main", commit_id: "c3" } } }] }));
  daemon.on("ShelveCreate", () => ({ lines: [{ result: { shelve: { shelf: { id: "s1" } } } }] }));
  session = new DaemonSession(async () => ({ client: daemon.client(), version: "v", started: false }));
  snapshots = new SnapshotContentProvider(() => session.client());
  logs = [];
  model = new Model({
    session,
    snapshots,
    log: (l) => logs.push(l),
    lockRefreshMs: 60_000,
    lockWarnings: () => lockWarnings,
    debounceMs: 5,
    retryDelayMs: 5,
  });
});

afterEach(async () => {
  model.dispose();
  snapshots.dispose();
  await daemon.close();
  await fs.rm(root, { recursive: true, force: true });
});

async function open(): Promise<NipaRepository> {
  const repo = await model.openRepository(root);
  return repo!;
}

const sc = () => fake.sourceControls[0];
const lockDetail = (p: string, held_by: string, extra: Record<string, unknown> = {}) => ({
  id: `L-${p}`,
  path: p,
  branch: "feature",
  global: false,
  held_by,
  held_by_name: held_by === "me" ? "Me" : "Ana",
  ...extra,
});

describe("locks", () => {
  it("locks paths in the clone's branch scope and reports failures", async () => {
    const repo = await open();
    daemon.on("ProxyLockFile", (body) =>
      body.request.path === "bad.png"
        ? { status: 409, json: { code: "AlreadyExists", message: "locked by Ana" } }
        : { json: { response: {}, warning: body.request.path === "w.png" ? "pin failed" : "" } },
    );
    await repo.lockPaths(["a.png", "w.png", "bad.png"]);
    expect(calls("ProxyLockFile").map((c) => c.body)).toContainEqual({ root, request: { path: "a.png", branch: "main" } });
    expect(fake.messages.some((m) => m.level === "error" && m.text.includes("bad.png: locked by Ana"))).toBe(true);
    expect(fake.messages.some((m) => m.text === "Locked 2 paths.")).toBe(true);
    expect(logs).toContain("lock w.png: pin failed");
    await repo.lockPaths(["one.png"]);
    expect(fake.messages.at(-1)?.text).toBe("Locked one.png.");
  });

  it("unlocks my covering lock, refuses others' and lets admins force", async () => {
    const repo = await open();
    locks = [lockDetail("art", "me"), lockDetail("docs/spec.pdf", "ana", { global: true, branch: "" })];
    await repo.unlockPaths(["art/hero.png", "docs/spec.pdf", "free.png"]);
    expect(calls("ProxyUnlockFile").map((c) => c.body.request)).toEqual([{ path: "art", branch: "feature" }]);
    expect(fake.messages.map((m) => m.text)).toEqual(
      expect.arrayContaining(["Unlocked art.", 'docs/spec.pdf is locked by Ana. Use "Ask to Release" instead.', "free.png is not locked."]),
    );

    daemon.on("LockStatus", () => ({ json: { user_id: "me", can_release_any: true, locks, files: [] } }));
    fake.answers.push(undefined);
    await repo.unlockPaths(["docs/spec.pdf"]);
    expect(calls("ProxyUnlockFile")).toHaveLength(1);
    fake.answers.push("Release Lock");
    await repo.unlockPaths(["docs/spec.pdf"]);
    expect(calls("ProxyUnlockFile").at(-1)?.body.request).toEqual({ path: "docs/spec.pdf", branch: "" });

    daemon.on("ProxyUnlockFile", () => ({ status: 404, json: { code: "NotFound", message: "gone" } }));
    await repo.unlockPaths(["art/x"]);
    expect(fake.messages.at(-1)).toMatchObject({ level: "error", text: "Nipa: could not unlock art: gone" });
  });

  it("prefers locks in the clone's branch scope", async () => {
    const repo = await open();
    locks = [
      lockDetail("art.png", "bob", { global: true, branch: "", id: "mainline" }),
      lockDetail("art.png", "ana", { id: "feature" }),
      lockDetail("art.png", "me", { global: true, branch: "", id: "my-mainline" }),
      lockDetail("art.png", "me", { id: "my-feature" }),
    ];
    fake.inputBox = "";
    await repo.requestRelease("art.png");
    expect(calls("ProxyLockRequestRelease")[0].body.request.lock_id).toBe("feature");
    await repo.unlockPaths(["art.png"]);
    expect(calls("ProxyUnlockFile")[0].body.request).toEqual({ path: "art.png", branch: "feature" });

    daemon.on("LockStatus", () => ({ json: { user_id: "me", branch: "main", default_branch: true, locks, files: [] } }));
    await repo.requestRelease("art.png");
    expect(calls("ProxyLockRequestRelease")[1].body.request.lock_id).toBe("mainline");
    await repo.unlockPaths(["art.png"]);
    expect(calls("ProxyUnlockFile")[1].body.request).toEqual({ path: "art.png", branch: "" });

    locks = [lockDetail("art.png", "bob", { global: true, branch: "", id: "only-mainline" })];
    daemon.on("LockStatus", () => ({ json: { user_id: "me", branch: "feature", default_branch: false, can_release_any: false, locks, files: [] } }));
    await repo.requestRelease("art.png");
    expect(calls("ProxyLockRequestRelease")[2].body.request.lock_id).toBe("only-mainline");
  });

  it("asks the holder to release", async () => {
    const repo = await open();
    locks = [lockDetail("art/hero.png", "ana"), lockDetail("mine.png", "me")];
    await repo.requestRelease("mine.png");
    expect(fake.messages.at(-1)?.text).toBe("mine.png is not locked by anyone else.");
    fake.inputBox = undefined;
    await repo.requestRelease("art/hero.png");
    expect(calls("ProxyLockRequestRelease")).toHaveLength(0);
    expect(fake.inputBoxOptions.validateInput("x".repeat(501))).toContain("500");
    expect(fake.inputBoxOptions.validateInput("ok")).toBeUndefined();
    fake.inputBox = "need it for the demo";
    await repo.requestRelease("art/hero.png");
    expect(calls("ProxyLockRequestRelease")[0].body.request).toEqual({ lock_id: "L-art/hero.png", message: "need it for the demo" });
    expect(fake.messages.at(-1)?.text).toBe("Asked Ana to release art/hero.png.");
    daemon.on("ProxyLockRequestRelease", () => ({ status: 400, json: { code: "InvalidArgument", message: "you hold this lock" } }));
    await repo.requestRelease("art/hero.png");
    expect(fake.messages.at(-1)?.level).toBe("error");
  });

  it("warns once about binaries that need a lock or are locked by someone else", async () => {
    current = status({ modified: ["hero.png", "map.umap"] });
    lockFiles = [
      { path: "hero.png", state: "needed", held_by: "", held_by_name: "" },
      { path: "map.umap", state: "theirs", held_by: "u2", held_by_name: "Ana" },
    ];
    locks = [lockDetail("map.umap", "u2")];
    fake.answers.push("Lock", "Ask to Release");
    fake.inputBox = "";
    const repo = await open();
    await repo.refreshLocks();
    await waitFor(() => {
      const texts = fake.messages.map((m) => m.text);
      return texts.includes("Locked hero.png.") && texts.includes("Asked Ana to release map.umap.");
    });
    const warnings = fake.messages.filter((m) => m.level === "warning").map((m) => m.text);
    expect(warnings).toEqual(
      expect.arrayContaining([
        "hero.png changed but is not locked. Pushing a changed binary needs your lock.",
        "map.umap is locked by Ana. Your changes cannot be pushed until it is released.",
      ]),
    );
    const count = fake.messages.length;
    await repo.refreshLocks();
    expect(fake.messages.length).toBe(count);

    lockFiles = [];
    await repo.refreshLocks();
    lockFiles = [{ path: "hero.png", state: "needed" }];
    lockWarnings = false;
    await repo.refreshLocks();
    expect(fake.messages.length).toBeGreaterThanOrEqual(count);
    expect(fake.messages.filter((m) => m.text.startsWith("hero.png changed")).length).toBe(1);
  });
});

describe("lock warning bookkeeping", () => {
  it("still warns after a silent refresh and again once the change came back", async () => {
    current = status({ modified: ["hero.png"] });
    lockFiles = [{ path: "hero.png", state: "needed", held_by: "", held_by_name: "" }];
    const repo = await open();
    const needed = () => fake.messages.filter((m) => m.text.startsWith("hero.png changed")).length;
    await waitFor(() => calls("LockStatus").length > 0);
    fake.messages = [];
    repo["warned"].clear();

    await repo.refreshLocks(false);
    expect(needed()).toBe(0);
    await repo.refreshLocks(true);
    expect(needed()).toBe(1);
    await repo.refreshLocks(true);
    expect(needed()).toBe(1);

    current = status();
    await repo.refresh();
    await repo.refreshLocks(true);
    current = status({ modified: ["hero.png"] });
    await repo.refresh();
    await repo.refreshLocks(true);
    expect(needed()).toBe(2);
  });
});

describe("conflicts", () => {
  beforeEach(() => {
    current = status({ conflicts: ["a.txt", "hero.png", "gone.txt", "done.txt"], resolved: ["done.txt"], pending: "merge" });
    daemon.on("Conflicts", () => ({
      json: {
        op: "merge",
        base: { id: "b1", label: "base" },
        ours: { id: "o1", label: "feature" },
        theirs: { id: "t1", label: "main" },
        files: [
          { path: "a.txt", kind: "text", binary: false, resolved: false, viewer: "text" },
          { path: "hero.png", kind: "binary", binary: true, resolved: false, viewer: "image" },
          { path: "gone.txt", kind: "modify_delete", binary: false, resolved: false, viewer: "text" },
        ],
      },
    }));
  });

  it("labels conflict rows and opens the right view", async () => {
    const repo = await open();
    await waitFor(() => repo.conflictFile("a.txt") !== undefined);
    const rows = sc().groups[2].resourceStates;
    expect(rows.map((r: { contextValue: string }) => r.contextValue)).toEqual(["conflict-text", "resolved", "conflict-other", "conflict-other"]);
    expect(rows[0].command).toMatchObject({ command: "vscode.open" });
    const binary = rows[3].command;
    expect(binary.command).toBe("vscode.diff");
    expect(parseBlobUri(binary.arguments[0])?.ref).toEqual({ source: "revision", revision: "o1", path: "hero.png" });
    expect(parseBlobUri(binary.arguments[1])?.ref).toEqual({ source: "revision", revision: "t1", path: "hero.png" });
    expect(binary.arguments[2]).toBe("hero.png (feature ↔ main)");
    expect(rows[1].command.command).toBe("vscode.diff");
  });

  it("compares sides, including shelf applies and missing sides", async () => {
    const repo = await open();
    await waitFor(() => repo.conflictSet !== undefined);
    await repo.compareConflict("a.txt");
    expect(fake.executed.at(-1)?.command).toBe("vscode.diff");

    daemon.on("Conflicts", () => ({ json: { op: "shelf", base: null, ours: null, theirs: { id: "s9", label: "" }, files: [] } }));
    await repo.refresh();
    await waitFor(() => repo.conflictSet?.op === "shelf");
    expect(repo.conflictSides("x")).toEqual({
      ours: { source: "snapshot", path: "x" },
      theirs: { source: "shelf", revision: "s9", path: "x" },
      labels: ["Mine", "Theirs"],
    });
    daemon.on("Conflicts", () => ({ json: { op: "shelf", base: null, ours: null, theirs: null, files: [] } }));
    await repo.refresh();
    await waitFor(() => repo.conflictSet?.theirs === null);
    await repo.compareConflict("x");
    expect(fake.messages.at(-1)?.text).toContain("does not exist on both sides");
  });

  it("takes a side and marks text resolved only without markers", async () => {
    const repo = await open();
    await repo.takeSide(["hero.png"], "theirs");
    expect(calls("ResolveConflict")[0].body).toEqual({ root, path: "hero.png", choice: "theirs" });

    const file = path.join(root, "a.txt");
    fake.files.set(file, "x\n<<<<<<< mine\na\n=======\nb\n>>>>>>> theirs\n");
    await repo.markResolved(["a.txt"]);
    expect(fake.messages.at(-1)?.text).toContain("still has conflict markers");
    fake.textDocuments = [{ uri: Uri.file(file), isDirty: true, lineCount: 1, getText: () => "" }];
    await repo.markResolved(["a.txt"]);
    expect(fake.messages.at(-1)?.text).toBe("Save a.txt before marking it resolved.");
    fake.textDocuments = [{ uri: Uri.file(file), isDirty: false, lineCount: 1, getText: () => "clean" }];
    await repo.markResolved(["a.txt"]);
    expect(calls("ResolveConflict").at(-1)?.body).toEqual({ root, path: "a.txt", choice: "working" });
    fake.textDocuments = [];
    await repo.markResolved(["unreadable.txt"]);
    expect(calls("ResolveConflict").at(-1)?.body.path).toBe("unreadable.txt");
  });

  it("logs a failing conflict listing", async () => {
    daemon.on("Conflicts", () => ({ status: 500, json: { code: "Internal", message: "state broken" } }));
    await open();
    await waitFor(() => logs.includes("conflicts: state broken"));
  });
});

describe("switching and updating", () => {
  it("lists branches and tags across pages", async () => {
    const repo = await open();
    const branchPages: unknown[] = [];
    daemon.on("ProxyBranchList", (body) => {
      branchPages.push(body.request);
      const first = !body.request.last_id;
      const count = first ? 100 : 3;
      return {
        json: {
          response: {
            branches: Array.from({ length: count }, (_, i) => ({ id: `${first ? "a" : "b"}${i}`, name: `${first ? "a" : "b"}${i}`, updated_at: "2026-01-01T00:00:00Z", is_default: false, is_protected: false })),
          },
        },
      };
    });
    daemon.on("ProxyTagList", (body) => ({
      json: { response: { tags: body.request.last_id ? [] : Array.from({ length: 100 }, (_, i) => ({ id: `t${i}`, name: `v${i}`, commit_id: "c", message: "", created_at: "x" })) } },
    }));
    const { branches, tags } = await repo.listTargets();
    expect(branches).toHaveLength(103);
    expect(tags).toHaveLength(100);
    expect(branchPages[1]).toEqual({ limit: 100, last_updated_at: "2026-01-01T00:00:00Z", last_id: "a99" });
  });

  it("switches through the picker", async () => {
    current = status({ branch: "feature" });
    const repo = await open();
    daemon.on("ProxyBranchList", () => ({
      json: {
        response: {
          branches: [
            { id: "1", name: "main", is_default: true, is_protected: true },
            { id: "2", name: "feature", is_default: false, is_protected: false },
          ],
        },
      },
    }));
    daemon.on("ProxyTagList", () => ({ json: { response: { tags: [{ id: "t", name: "v1.0", commit_id: "c", message: "First release\nmore" }] } } }));
    let seen: { label: string; description?: string }[] = [];
    fake.quickPick = (items) => {
      seen = items;
      return items.find((i: { label: string }) => i.label === "$(git-branch) main");
    };
    await pickAndSwitch(repo);
    expect(seen.map((i) => [i.label, i.description])).toEqual([
      ["Branches", undefined],
      ["$(git-branch) main", "default · protected"],
      ["$(git-branch) feature", "current"],
      ["Tags", undefined],
      ["$(tag) v1.0", "First release"],
    ]);
    expect(calls("Switch")[0].body).toEqual({ root, branch: "main" });
    expect(fake.messages.at(-1)?.text).toBe("Switched to main.");

    fake.quickPick = (items) => items.find((i: { label: string }) => i.label === "$(git-branch) feature");
    await pickAndSwitch(repo);
    fake.quickPick = () => undefined;
    await pickAndSwitch(repo);
    expect(calls("Switch")).toHaveLength(1);

    daemon.on("ProxyTagList", () => ({ json: { response: { tags: [] } } }));
    current = status({ branch: "feature", head: { kind: "tag", name: "v1.0" } });
    await repo.refresh();
    fake.quickPick = (items) => {
      seen = items;
      return undefined;
    };
    await pickAndSwitch(repo);
    expect(seen.find((i) => i.label.includes("feature"))?.description).toBe("");
    await repo.switchTo({ tag: "v1.0" });
    expect(calls("Switch").at(-1)?.body).toEqual({ root, tag: "v1.0" });
    expect(fake.messages.at(-1)?.text).toBe("Switched to tag v1.0.");
  });

  it("shelves the local changes an update or switch would replace", async () => {
    const repo = await open();
    daemon.on("SyncPlan", (body) => ({ json: { overwritten: body.tag ? Array.from({ length: 12 }, (_, i) => `f${i}.txt`) : ["src/a.txt"] } }));
    fake.answers.push(undefined);
    await repo.update();
    expect(calls("Update")).toHaveLength(0);
    expect(fake.messages.at(-1)?.text).toBe("Updating would replace your local changes in 1 file.");

    fake.answers.push("Shelve and Continue");
    await repo.update();
    expect(calls("ShelveCreate")[0].body).toEqual({ root, name: "Updating", paths: ["src/a.txt"] });
    expect(calls("Update")).toHaveLength(1);

    fake.answers.push("Shelve and Continue");
    daemon.on("ShelveCreate", () => ({ lines: [{ failure: { code: 409, message: "merge pending" } }] }));
    await repo.switchTo({ tag: "v2" });
    expect(fake.messages.find((m) => m.text.includes("12 files"))?.options.detail).toContain("…and 2 more");
    expect(calls("Switch")).toHaveLength(0);

    daemon.on("SyncPlan", () => ({ status: 501, json: { code: "Unimplemented", message: "old daemon" } }));
    await repo.update();
    expect(calls("Update")).toHaveLength(2);
    expect(logs).toContain("sync plan: old daemon");
  });
});

describe("activity", () => {
  it("shows incoming commits and maintenance in the status bar", async () => {
    daemon.on("WatchEvents", () => ({
      lines: [
        {
          incoming: 3,
          review_requests: 0,
          error: "server unreachable",
          maintenance: { server: { mode: "read_only", active: true, message: "", org: "", project: "" }, org: null, project: null },
        },
      ],
      hold: true,
    }));
    await open();
    await waitFor(() => sc().statusBarCommands?.some((c: { title: string }) => c.title === "$(sync) 3↓") ?? false);
    const titles = sc().statusBarCommands!.map((c: { title: string }) => c.title);
    expect(titles).toEqual(["$(git-branch) main", "$(tools) read-only", "$(sync) 3↓"]);
    expect(sc().statusBarCommands![0].command).toBe("nipa.switch");
    expect(sc().statusBarCommands![2].tooltip).toBe("Update (3 incoming commits)");
    expect(logs).toContain("activity: server unreachable");
  });

  it("retries the activity stream", async () => {
    daemon.on("WatchEvents", () => ({ status: 500, json: { code: "Internal", message: "poll broke" } }));
    const repo = await open();
    await waitFor(() => calls("WatchEvents").length >= 2);
    expect(logs).toContain("activity: poll broke");
    await repo.stopWatching();
  });
});

describe("blob documents", () => {
  it("stops refreshing documents once they are closed", async () => {
    daemon.on("ReadBlob", () => ({ lines: [{ data: Buffer.from("x").toString("base64") }] }));
    const token = new CancellationTokenSource().token;
    const a = blobUri(root, { source: "snapshot", path: "a.txt" });
    const b = blobUri(root, { source: "snapshot", path: "b.txt" });
    await snapshots.provideTextDocumentContent(a as never, token);
    await snapshots.provideTextDocumentContent(b as never, token);
    fake.closeListeners.fire({ uri: a });
    const fired: string[] = [];
    snapshots.onDidChange((u) => fired.push(u.toString()));
    snapshots.refresh(root);
    expect(fired).toEqual([b.toString()]);
  });

  it("serves revision and shelf sides without the working fallback", async () => {
    daemon.on("ReadBlob", (body) =>
      body.blob.source === "revision" ? { lines: [{ data: Buffer.from(`rev ${body.blob.revision}`).toString("base64") }] } : { lines: [{ failure: { code: 404, message: "no" } }] },
    );
    const token = new CancellationTokenSource().token;
    const rev = blobUri(root, { source: "revision", revision: "o1", path: "a.txt" }, "ours");
    expect(rev.path).toContain("/ours/a.txt");
    expect(await snapshots.provideTextDocumentContent(rev as never, token)).toBe("rev o1");
    await fs.writeFile(path.join(root, "a.txt"), "working");
    expect(await snapshots.provideTextDocumentContent(blobUri(root, { source: "shelf", revision: "s", path: "a.txt" }) as never, token)).toBe("");
    expect(parseBlobUri(Uri.from({ scheme: "nipa-blob", query: "nope" }) as never)).toBeUndefined();
  });
});

describe("blame", () => {
  const blameResult = {
    path: "src/a.txt",
    revision: "",
    lines: [
      { text: "a", commit_id: "c1", uncommitted: false },
      { text: "b", commit_id: "c1", uncommitted: false },
      { text: "c", commit_id: "", uncommitted: true },
      { text: "d", commit_id: "", uncommitted: false },
    ],
    commits: [{ id: "c1", hash: "h", author_name: "Ana", author_email: "ana@example.com", message: "Fix *it*", created_at: "2026-11-16T12:00:00Z" }],
    truncated: false,
  };
  let enabled = true;
  let blame: BlameController;

  beforeEach(() => {
    enabled = true;
    daemon.on("Blame", () => ({ lines: [{ progress: { done: 1, total: 2 } }, { result: blameResult }] }));
  });

  afterEach(() => blame?.dispose());

  it("decorates visible editors and caches results", async () => {
    const repo = await open();
    blame = new BlameController({ model, enabled: () => enabled, log: (l) => logs.push(l), now: () => Date.parse("2026-11-17T12:00:00Z") });
    const editor = fakeEditor(path.join(root, "src/a.txt"), { lines: 3 });
    fake.visibleEditors = [editor];
    await blame.renderVisible();
    expect(editor.decorations.map((d) => d.renderOptions.before.contentText)).toEqual(["Ana · 1 d", " ", "Not pushed"]);
    expect(editor.decorations[0].hoverMessage.value).toContain("**Ana** <ana@example\\.com>");
    expect(editor.decorations[0].hoverMessage.value).toContain("Fix \\*it\\*");
    expect(editor.decorations[0].hoverMessage.value).toContain("[Open in Nipa Desktop](nipa://nipa.example.com/acme/game/commits/c1)");
    expect(editor.decorations[1].hoverMessage).toBeUndefined();
    expect(editor.decorations[2].hoverMessage.value).toContain("Not pushed yet");
    expect(calls("Blame")[0].body).toEqual({ root, path: "src/a.txt" });

    await blame.renderVisible();
    expect(calls("Blame")).toHaveLength(1);

    fake.saveListeners.fire(editor.document);
    await waitFor(() => calls("Blame").length === 2);

    await repo.refresh();
    await waitFor(() => calls("Blame").length === 3);
    expect(repo).toBeDefined();
  });

  it("shows the older-history hover", async () => {
    await open();
    blame = new BlameController({ model, enabled: () => true, log: () => undefined });
    const editor = fakeEditor(path.join(root, "src/a.txt"), { lines: 10 });
    await blame.render(editor as never);
    expect(editor.decorations[3].hoverMessage.value).toContain("Older history");
  });

  it("skips dirty, foreign and disabled editors and clears on edits", async () => {
    await open();
    blame = new BlameController({ model, enabled: () => enabled, log: (l) => logs.push(l) });
    const dirty = fakeEditor(path.join(root, "src/a.txt"), { dirty: true });
    dirty.decorations = [1];
    await blame.render(dirty as never);
    expect(dirty.decorations).toEqual([]);
    const outside = fakeEditor("/elsewhere/x.txt");
    await blame.render(outside as never);
    const untitled = fakeEditor(path.join(root, "src/a.txt"));
    (untitled.document as { uri: Uri }).uri = Uri.from({ scheme: "untitled", path: "/x" });
    await blame.render(untitled as never);
    expect(calls("Blame")).toHaveLength(0);

    const editor = fakeEditor(path.join(root, "src/a.txt"));
    fake.visibleEditors = [editor];
    await blame.renderVisible();
    expect(editor.decorations.length).toBeGreaterThan(0);
    fake.changeListeners.fire({ document: { ...editor.document, isDirty: false } });
    expect(editor.decorations.length).toBeGreaterThan(0);
    editor.document.isDirty = true;
    fake.changeListeners.fire({ document: editor.document });
    expect(editor.decorations).toEqual([]);

    enabled = false;
    editor.document.isDirty = false;
    await blame.render(editor as never);
    expect(editor.decorations).toEqual([]);
    enabled = true;
    await blame.render(editor as never);
    blame.clear();
    expect(editor.decorations).toEqual([]);
  });

  it("logs failures, ignores late results and shares in-flight requests", async () => {
    const repo = await open();
    blame = new BlameController({ model, enabled: () => true, log: (l) => logs.push(l) });
    daemon.on("Blame", () => ({ lines: [{ failure: { code: 400, message: "binary file" } }] }));
    const editor = fakeEditor(path.join(root, "src/a.txt"));
    await blame.render(editor as never);
    expect(logs).toContain("blame src/a.txt: binary file");
    expect(editor.decorations).toEqual([]);

    daemon.on("Blame", () => ({ lines: [] }));
    await blame.render(editor as never);
    expect(logs).toContain("blame src/a.txt: Blame ended without a result");

    let release!: () => void;
    daemon.on("Blame", () => new Promise((resolve) => (release = () => resolve({ lines: [{ result: blameResult }] }))));
    const a = blame.render(editor as never);
    const b = blame.render(editor as never);
    await waitFor(() => release !== undefined && calls("Blame").length === 3);
    fake.saveListeners.fire(editor.document);
    release();
    await Promise.all([a, b]);
    expect(calls("Blame").length).toBeGreaterThanOrEqual(3);
    expect(repo).toBeDefined();
  });

  it("drops a result that finished after its file was forgotten", async () => {
    await open();
    blame = new BlameController({ model, enabled: () => true, log: (l) => logs.push(l) });
    let release!: () => void;
    daemon.on("Blame", () => new Promise((resolve) => (release = () => resolve({ lines: [{ result: blameResult }] }))));
    const editor = fakeEditor(path.join(root, "src/a.txt"));
    const pending = blame.render(editor as never);
    await waitFor(() => release !== undefined);
    blame.dispose();
    release();
    await pending;
    expect(logs.filter((l) => l.startsWith("blame"))).toEqual([]);
  });
});

describe("file targets", () => {
  it("collects files from URIs, resource states and the active editor", async () => {
    const repo = await open();
    const a = Uri.file(path.join(root, "a.png"));
    const b = Uri.file(path.join(root, "b.png"));
    const targets = fileTargets(model, [a, [a, b, Uri.file("/elsewhere/c"), Uri.from({ scheme: "untitled", path: "/d" })]]);
    expect(targets.map((t) => t.rel)).toEqual(["a.png", "b.png"]);
    expect(fileTargets(model, [sc().groups[1].resourceStates[0]]).map((t) => t.rel)).toEqual(["src/a.txt"]);
    fake.activeFile = path.join(root, "x.txt");
    expect(fileTargets(model, [undefined]).map((t) => t.rel)).toEqual(["x.txt"]);
    fake.activeFile = undefined;
    expect(fileTargets(model, [])).toEqual([]);
    expect(repo).toBeDefined();
  });
});
