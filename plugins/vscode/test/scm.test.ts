import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DaemonSession } from "../src/daemon/session";
import { Model } from "../src/scm/model";
import { NipaRepository, progressReporter } from "../src/scm/repository";
import { parseSnapshotUri, SnapshotContentProvider, snapshotUri } from "../src/scm/snapshot";
import { FakeDaemon, startFakeDaemon, status } from "./fakes/daemon";
import { CancellationTokenSource, fake, Uri } from "./fakes/vscode";

let daemon: FakeDaemon;
let root: string;
let session: DaemonSession;
let snapshots: SnapshotContentProvider;
let model: Model;
let logs: string[];
let decorated: Uri[][];
let current: Record<string, unknown>;

const waitFor = (check: () => boolean) => vi.waitFor(() => expect(check()).toBe(true), { timeout: 2000, interval: 5 });

beforeEach(async () => {
  fake.reset();
  daemon = await startFakeDaemon();
  root = await fs.mkdtemp(path.join(os.tmpdir(), "nipa-scm-"));
  await fs.mkdir(path.join(root, ".nipa"));
  await fs.writeFile(path.join(root, ".nipa", "config"), "{}");
  current = status({ modified: ["src/a.txt"], untracked: ["new.txt"], staged: ["art/hero.png"], added: ["art/hero.png"] });
  daemon.on("WatchRepo", (body) => ({ json: { repo: { root: body.root, url: "u", branch: "main", sparse: [] } } }));
  daemon.on("Status", () => ({ json: current }));
  daemon.on("Stage", (body) => {
    const staged = new Set([...(current.staged as string[]), ...(body.add ?? [])]);
    for (const p of body.unstage ?? []) {
      staged.delete(p);
    }
    current = { ...current, staged: [...staged], modified: (current.modified as string[]).filter((p) => !staged.has(p)) };
    return { json: current };
  });
  daemon.on("Restore", (body) => {
    current = { ...current, modified: (current.modified as string[]).filter((p) => !body.paths.includes(p)) };
    return { json: current };
  });
  daemon.on("WatchChanges", () => ({ lines: [], hold: true }));
  daemon.on("LockStatus", () => ({ json: { files: [{ path: "art/hero.png", state: "theirs", held_by: "u2", held_by_name: "Ana" }] } }));
  daemon.on("PushPlan", () => ({ json: { up_to_date: false, asset_violations: [], asset_impact: null, warnings: [] } }));
  daemon.on("ReadBlob", (body) => ({
    lines: [{ info: { path: body.blob.path, is_binary: body.blob.path.endsWith(".png") } }, { data: Buffer.from(`old ${body.blob.path}`).toString("base64") }],
  }));
  session = new DaemonSession(async () => ({ client: daemon.client(), version: "v", started: false }));
  snapshots = new SnapshotContentProvider(() => session.client());
  logs = [];
  decorated = [];
  model = new Model({ session, snapshots, log: (l) => logs.push(l), lockRefreshMs: 60_000, debounceMs: 5, retryDelayMs: 5 });
  model.onDidChangeFileDecorations((uris) => decorated.push(uris as unknown as Uri[]));
});

afterEach(async () => {
  model.dispose();
  snapshots.dispose();
  await daemon.close();
  await fs.rm(root, { recursive: true, force: true });
});

async function openRepo(): Promise<NipaRepository> {
  await model.discover([path.join(root, "src")], []);
  const repo = model.repositories[0];
  await waitFor(() => daemon.calls.some((c) => c.method === "LockStatus"));
  await waitFor(() => repo.decorationFor(path.join(root, "art/hero.png"))?.lock === "theirs");
  return repo;
}

function sc() {
  return fake.sourceControls[0];
}

describe("NipaRepository", () => {
  it("renders groups, status bar and decorations", async () => {
    const repo = await openRepo();
    expect(daemon.calls.find((c) => c.method === "WatchRepo")?.body).toEqual({ root });
    const [staged, changes, conflicts] = sc().groups;
    expect(staged.resourceStates.map((s) => s.change.path)).toEqual(["art/hero.png"]);
    expect(changes.resourceStates.map((s) => [s.change.path, s.contextValue])).toEqual([
      ["new.txt", "untracked"],
      ["src/a.txt", "modified"],
    ]);
    expect(conflicts.hideWhenEmpty).toBe(true);
    expect(sc().count).toBe(3);
    expect(sc().statusBarCommands?.[0].title).toBe("$(git-branch) main");
    expect(sc().inputBox.placeholder).toContain('"main"');
    expect(staged.resourceStates[0].decorations.tooltip).toBe("Added · Locked by Ana");

    const diff = changes.resourceStates[1].command;
    expect(diff.command).toBe("vscode.diff");
    expect(parseSnapshotUri(diff.arguments[0])).toEqual({ root, path: "src/a.txt" });
    expect(changes.resourceStates[0].command.command).toBe("vscode.open");

    const hero = model.provideFileDecoration(Uri.file(path.join(root, "art/hero.png")) as never);
    expect(hero).toMatchObject({ badge: "A", propagate: false });
    expect((hero?.color as { id: string }).id).toBe("list.errorForeground");
    const plain = model.provideFileDecoration(Uri.file(path.join(root, "src/a.txt")) as never);
    expect(plain).toMatchObject({ badge: "M", propagate: true });
    expect(model.provideFileDecoration(Uri.file(path.join(root, "clean.txt")) as never)).toBeUndefined();
    expect(model.provideFileDecoration(Uri.from({ scheme: "untitled", path: "x" }) as never)).toBeUndefined();
    expect(model.provideFileDecoration(Uri.file("/elsewhere/x") as never)).toBeUndefined();
    expect(decorated.length).toBeGreaterThan(0);
    expect(repo.name).toBe(path.basename(root));
  });

  it("decorates every lock state", async () => {
    daemon.on("LockStatus", () => ({
      json: {
        files: [
          { path: "src/a.txt", state: "needed" },
          { path: "new.txt", state: "mine" },
          { path: "art/hero.png", state: "theirs", held_by: "u2", held_by_name: "" },
        ],
      },
    }));
    const repo = model.repositories[0] ?? (await model.openRepository(root))!;
    await repo.refreshLocks();
    expect(repo.decorationFor(path.join(root, "src/a.txt"))).toMatchObject({ lock: "needed", color: "list.warningForeground" });
    expect(repo.decorationFor(path.join(root, "new.txt"))?.tooltip).toBe("Untracked · Locked by you");
    expect(repo.decorationFor(path.join(root, "art/hero.png"))?.tooltip).toBe("Added · Locked by u2");
  });

  it("clears locks when the server is unreachable or nothing changed", async () => {
    const repo = await openRepo();
    daemon.on("LockStatus", () => ({ status: 503, json: { code: "Unavailable", message: "offline" } }));
    await repo.refreshLocks();
    expect(repo.decorationFor(path.join(root, "art/hero.png"))?.lock).toBeUndefined();
    expect(logs.some((l) => l.includes("lock status: offline"))).toBe(true);
    current = status();
    await repo.refresh();
    const before = daemon.calls.filter((c) => c.method === "LockStatus").length;
    await repo.refreshLocks();
    expect(daemon.calls.filter((c) => c.method === "LockStatus").length).toBe(before);
  });

  it("answers quick diff with the synced version", async () => {
    const repo = await openRepo();
    const original = repo.provideOriginalResource(Uri.file(path.join(root, "src/a.txt")) as never);
    expect(parseSnapshotUri(original as never)).toEqual({ root, path: "src/a.txt" });
    expect(repo.provideOriginalResource(Uri.file(path.join(root, "clean.txt")) as never)).toBeDefined();
    expect(repo.provideOriginalResource(Uri.file(path.join(root, "new.txt")) as never)).toBeUndefined();
    expect(repo.provideOriginalResource(Uri.file(path.join(root, "art/hero.png")) as never)).toBeUndefined();
    expect(repo.provideOriginalResource(Uri.file(path.join(root, ".nipa/config")) as never)).toBeUndefined();
    expect(repo.provideOriginalResource(Uri.file(root) as never)).toBeUndefined();
    expect(repo.provideOriginalResource(Uri.from({ scheme: "git", path: "/x" }) as never)).toBeUndefined();
  });

  it("stages, unstages and discards", async () => {
    const repo = await openRepo();
    await repo.stage(["src/a.txt"]);
    expect(repo.state?.staged.map((c) => c.path)).toContain("src/a.txt");
    await repo.unstageAll();
    expect(repo.state?.staged).toEqual([]);
    await repo.stageAll();
    expect(daemon.calls.filter((c) => c.method === "Stage").at(-1)?.body.add).toEqual(["new.txt"]);
    await repo.unstage(["new.txt", "src/a.txt", "art/hero.png"]);

    fake.answers.push(undefined);
    await repo.discard([{ path: "src/a.txt", kind: "modified" }]);
    expect(daemon.calls.some((c) => c.method === "Restore")).toBe(false);
    fake.answers.push("Discard Changes");
    await repo.discard([
      { path: "src/a.txt", kind: "modified" },
      { path: "x.txt", kind: "deleted" },
    ]);
    expect(fake.messages.at(-1)?.text).toContain("2 files");
    expect(daemon.calls.find((c) => c.method === "Restore")?.body.paths).toEqual(["src/a.txt", "x.txt"]);
    await repo.discard([{ path: "new.txt", kind: "untracked" }]);
    fake.answers.push("Discard Changes");
    await repo.discard([{ path: "a/one.txt", kind: "modified" }]);
    expect(fake.messages.at(-1)?.text).toContain("'one.txt'");
  });

  it("does nothing for empty stage-all/unstage-all and reports errors", async () => {
    current = status();
    const repo = await model.openRepository(root);
    await repo!.stageAll();
    await repo!.unstageAll();
    expect(daemon.calls.some((c) => c.method === "Stage")).toBe(false);
    daemon.on("Stage", () => ({ status: 400, json: { code: "InvalidArgument", message: "ignored path" } }));
    await repo!.stage(["x"]);
    expect(fake.messages.at(-1)).toMatchObject({ level: "error", text: "Nipa: ignored path" });
  });

  it("pushes with checks and clears the message", async () => {
    const repo = await openRepo();
    daemon.on("Push", () => ({ lines: [{ started: { phase: "upload" } }, { result: { push: { commit_id: "c1" } } }] }));

    await repo.push();
    expect(fake.messages.at(-1)?.text).toBe("Enter a message before pushing.");

    sc().inputBox.value = "  ship it ";
    fake.answers.push(undefined);
    await repo.push();
    expect(fake.messages.at(-1)?.options?.detail).toContain("art/hero.png: Locked by Ana");
    expect(daemon.calls.some((c) => c.method === "Push")).toBe(false);

    fake.answers.push("Push Anyway");
    await repo.push();
    expect(daemon.calls.find((c) => c.method === "Push")?.body).toEqual({ root, message: "ship it" });
    expect(sc().inputBox.value).toBe("");
    expect(fake.messages.at(-1)?.text).toBe("Pushed commit c1.");
  });

  it("collects push plan warnings and tolerates plan errors", async () => {
    const repo = await openRepo();
    daemon.on("LockStatus", () => ({ json: { files: [] } }));
    daemon.on("PushPlan", () => ({
      json: {
        asset_violations: [{ path: "a.png", message: "too big" }],
        asset_impact: { broken: [{ from: "s.unity", raw: "guid", target: "", reason: "removed" }] },
        warnings: ["impact truncated"],
      },
    }));
    expect(await repo.pushWarnings()).toEqual(["a.png: too big", "s.unity: broken reference to guid (removed)", "impact truncated"]);
    daemon.on("PushPlan", () => ({ status: 500, json: { code: "Internal", message: "plan broke" } }));
    expect(await repo.pushWarnings()).toEqual([]);
    expect(logs.some((l) => l.includes("plan broke"))).toBe(true);
  });

  it("offers to stage tracked changes before pushing", async () => {
    current = status({ modified: ["src/a.txt"], untracked: ["new.txt"] });
    const repo = await model.openRepository(root);
    daemon.on("LockStatus", () => ({ json: { files: [] } }));
    daemon.on("Push", () => ({ lines: [{ result: {} }] }));
    sc().inputBox.value = "msg";
    fake.answers.push(undefined);
    await repo!.push();
    expect(fake.messages.at(-1)?.text).toContain("no staged changes");
    fake.answers.push("Stage and Push");
    await repo!.push();
    expect(daemon.calls.find((c) => c.method === "Stage")?.body.add).toEqual(["src/a.txt"]);
    expect(fake.messages.at(-1)?.text).toBe("Pushed.");

    current = status({ untracked: ["new.txt"] });
    await repo!.refresh();
    sc().inputBox.value = "msg";
    await repo!.push();
    expect(fake.messages.at(-1)?.text).toBe("There are no changes to push.");
  });

  it("refuses to push while detached and shows pending state", async () => {
    current = status({ head: { kind: "tag", name: "v1" }, pending: "revert", pending_remaining: 2 });
    const repo = await model.openRepository(root);
    expect(sc().inputBox.enabled).toBe(false);
    expect(sc().statusBarCommands?.map((c) => c.title)).toEqual(["$(tag) v1", "$(warning) Revert pending (2 left)", "$(sync)"]);
    await repo!.push();
    expect(fake.messages.at(-1)?.text).toContain('detached at tag "v1"');
  });

  it("updates, reports failures and refuses concurrent operations", async () => {
    const repo = await openRepo();
    daemon.on("Update", () => ({ lines: [{ result: { sync: { branch: "main", commit_id: "c9" } } }] }));
    await repo.update();
    expect(fake.messages.at(-1)?.text).toBe("Updated main to c9.");

    daemon.on("Update", () => ({ lines: [{ failure: { code: 503, message: "in maintenance", maintenance: { ends_at: "2026-01-01T00:00:00Z" } } }] }));
    await repo.update();
    expect(fake.messages.at(-1)?.level).toBe("error");
    expect(fake.messages.at(-1)?.text).toContain("Updating failed: in maintenance (estimated end");

    let release: (() => void) | undefined;
    daemon.on("Update", () => new Promise((resolve) => (release = () => resolve({ lines: [{ result: {} }] }))));
    const first = repo.update();
    await waitFor(() => sc().statusBarCommands?.at(-1)?.title === "$(sync~spin) Updating…" && release !== undefined);
    await repo.update();
    expect(fake.messages.at(-1)?.text).toContain("Updating is still running");
    release!();
    await first;
  });

  it("swallows a cancelled operation", async () => {
    const repo = await openRepo();
    daemon.on("Update", () => new Promise(() => undefined));
    fake.cancelProgress = true;
    const before = fake.messages.length;
    await repo.update();
    expect(fake.messages.length).toBe(before);
  });

  it("refreshes on change notices and re-reads snapshots after operations", async () => {
    const repo = await openRepo();
    const fired: string[] = [];
    snapshots.onDidChange((u) => fired.push(u.toString()));
    await snapshots.provideTextDocumentContent(snapshotUri(root, "src/a.txt") as never, new CancellationTokenSource().token);
    current = status({ modified: ["other.txt"] });
    daemon.held[0].write(`${JSON.stringify({ paths: ["other.txt"] })}\n`);
    await waitFor(() => repo.state?.unstaged[0]?.path === "other.txt");
    expect(fired).toEqual([]);
    daemon.held[0].write(`${JSON.stringify({ paths: [] })}\n`);
    await waitFor(() => fired.length === 1);
  });

  it("reconnects the watch stream and re-watches the clone", async () => {
    await openRepo();
    const watches = () => daemon.calls.filter((c) => c.method === "WatchChanges").length;
    daemon.release();
    await waitFor(() => watches() === 2);
    daemon.on("WatchChanges", () => ({ status: 400, json: { code: "InvalidArgument", message: 'repository "/x" is not watched' } }));
    daemon.release();
    await waitFor(() => daemon.calls.filter((c) => c.method === "WatchRepo").length >= 2);
    await waitFor(() => logs.some((l) => l.startsWith("watch:")));
  });

  it("reconnects the session when the daemon went away", async () => {
    const repo = await openRepo();
    const other = await startFakeDaemon();
    other.on("WatchRepo", () => ({ json: { repo: null } }));
    other.on("Status", () => ({ json: status({ modified: ["moved.txt"] }) }));
    other.on("WatchChanges", () => ({ lines: [], hold: true }));
    let first = true;
    const reconnecting = new DaemonSession(async () => {
      if (first) {
        first = false;
        return { client: daemon.client(), version: "v", started: false };
      }
      return { client: other.client(), version: "v2", started: true };
    });
    (repo as unknown as { deps: { session: DaemonSession } }).deps.session = reconnecting;
    await reconnecting.connection();
    await daemon.close();
    await repo.refresh();
    expect(repo.state?.unstaged[0]?.path).toBe("moved.txt");
    expect(other.calls.map((c) => c.method)).toContain("WatchRepo");
    repo.dispose();
    await other.close();
    daemon = await startFakeDaemon();
  });

  it("logs status failures and stops after dispose", async () => {
    const repo = await openRepo();
    daemon.on("Status", () => ({ status: 500, json: { code: "Internal", message: "db locked" } }));
    await Promise.all([repo.refresh(), repo.refresh()]);
    expect(logs).toContain("status: db locked");
    await repo.stopWatching();
    repo.dispose();
    expect(sc().disposed).toBe(true);
    await repo.refreshLocks();
    await repo.settled();
  });
});

describe("progressReporter", () => {
  it("reports queue, phases and increments", () => {
    const reports: unknown[] = [];
    const report = progressReporter({ report: (r) => reports.push(r) });
    report({ queued: { ahead: 1 } });
    report({ started: { phase: "scan" } });
    report({ progress: { phase: "upload", objects_done: "1", objects_total: "4", bytes_done: "0", bytes_total: "0" } });
    report({ progress: { phase: "upload", objects_done: "2", objects_total: "4", bytes_done: "0", bytes_total: "0" } });
    report({ progress: { phase: "apply", objects_done: "0", objects_total: "0", bytes_done: "0", bytes_total: "0" } });
    report({ result: {} });
    expect(reports).toEqual([
      { message: "waiting for 1 operation(s)" },
      { message: "scan" },
      { message: "upload 1/4", increment: 25 },
      { message: "upload 2/4", increment: 25 },
      { message: "apply" },
    ]);
  });
});

describe("SnapshotContentProvider", () => {
  it("serves text, hides binaries and falls back to the working file", async () => {
    const token = new CancellationTokenSource().token;
    expect(await snapshots.provideTextDocumentContent(snapshotUri(root, "src/a.txt") as never, token)).toBe("old src/a.txt");
    expect(await snapshots.provideTextDocumentContent(snapshotUri(root, "art/hero.png") as never, token)).toBe("");
    daemon.on("ReadBlob", () => ({ lines: [{ failure: { code: 404, message: "not tracked" } }] }));
    await fs.writeFile(path.join(root, "w.txt"), "working");
    expect(await snapshots.provideTextDocumentContent(snapshotUri(root, "w.txt") as never, token)).toBe("working");
    expect(await snapshots.provideTextDocumentContent(snapshotUri(root, "none.txt") as never, token)).toBe("");
    expect(await snapshots.provideTextDocumentContent(Uri.from({ scheme: "nipa-blob", path: "/x", query: "bad" }) as never, token)).toBe("");
    expect(parseSnapshotUri(Uri.from({ scheme: "nipa-blob", query: '{"root":1}' }) as never)).toBeUndefined();
  });

  it("only refreshes documents of the given clone", async () => {
    const token = new CancellationTokenSource().token;
    await snapshots.provideTextDocumentContent(snapshotUri(root, "a.txt") as never, token);
    await snapshots.provideTextDocumentContent(snapshotUri("/other", "b.txt") as never, token);
    const fired: string[] = [];
    snapshots.onDidChange((u) => fired.push(parseSnapshotUri(u as never)!.path));
    snapshots.refresh(root);
    expect(fired).toEqual(["a.txt"]);
  });
});

describe("Model", () => {
  it("discovers clones from folders and config files and drops removed ones", async () => {
    const nested = path.join(root, "nested");
    await fs.mkdir(path.join(nested, ".nipa"), { recursive: true });
    await fs.writeFile(path.join(nested, ".nipa", "config"), "{}");
    await model.discover([root, os.tmpdir()], [path.join(nested, ".nipa", "config")]);
    expect(model.repositories.map((r) => r.root).sort()).toEqual([root, nested].sort());
    expect(model.repositoryFor(path.join(nested, "x.txt"))?.root).toBe(nested);
    expect(model.repositoryFor(path.join(root, "x.txt"))?.root).toBe(root);
    expect(model.repositoryForControl(fake.sourceControls[1] as never)?.root).toBe(nested);
    await model.discover([root], []);
    expect(model.repositories.map((r) => r.root)).toEqual([root]);
  });

  it("drops a clone that cannot be opened", async () => {
    daemon.on("WatchRepo", () => ({ status: 400, json: { code: "InvalidArgument", message: "no .nipa/config" } }));
    await expect(model.discover([root], [])).rejects.toThrow("no .nipa/config");
    expect(model.repositories).toEqual([]);
    expect(logs.some((l) => l.includes("no .nipa/config"))).toBe(true);
  });
});
