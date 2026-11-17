import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import manifest from "../package.json";
import { FakeDaemon, startFakeDaemon, status } from "./fakes/daemon";
import { fake, fakeEditor, Uri } from "./fakes/vscode";

const connectMock = vi.hoisted(() => vi.fn());
vi.mock("../src/daemon/connector", () => ({ connect: connectMock }));

import { activate, deactivate } from "../src/extension";
import { resolveRepository, resourceStates } from "../src/scm/commands";

let daemon: FakeDaemon;
let roots: string[];
let subscriptions: { dispose(): unknown }[];

async function makeClone(name: string): Promise<string> {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), `nipa-${name}-`));
  await fs.mkdir(path.join(root, ".nipa"));
  await fs.writeFile(path.join(root, ".nipa", "config"), "{}");
  return root;
}

beforeEach(async () => {
  fake.reset();
  daemon = await startFakeDaemon();
  daemon.on("WatchRepo", () => ({ json: { repo: { root: "", url: "https://nipa.example.com/acme/game", branch: "main", sparse: [] } } }));
  daemon.on("Status", () => ({ json: status({ modified: ["a.txt", "b.txt"], staged: ["s.txt"] }) }));
  daemon.on("Stage", () => ({ json: status() }));
  daemon.on("Restore", () => ({ json: status() }));
  daemon.on("WatchChanges", () => ({ lines: [], hold: true }));
  daemon.on("LockStatus", () => ({ json: { files: [] } }));
  daemon.on("PushPlan", () => ({ json: {} }));
  daemon.on("Update", () => ({ lines: [{ result: { sync: { branch: "main", commit_id: "c" } } }] }));
  daemon.on("SyncPlan", () => ({ json: { overwritten: [] } }));
  daemon.on("WatchEvents", () => ({ lines: [], hold: true }));
  daemon.on("ProxyLockFile", () => ({ json: {} }));
  daemon.on("Blame", () => ({ lines: [{ result: { path: "a.txt", lines: [{ text: "x", commit_id: "", uncommitted: true }], commits: [] } }] }));
  connectMock.mockReset();
  connectMock.mockImplementation(async () => ({ client: daemon.client(), version: "v9", started: true }));
  roots = [await makeClone("one")];
  fake.workspaceFolders = [{ uri: Uri.file(roots[0]) }];
  subscriptions = [];
});

afterEach(async () => {
  for (const s of subscriptions) {
    s.dispose();
  }
  await daemon.close();
  for (const root of roots) {
    await fs.rm(root, { recursive: true, force: true });
  }
});

async function start() {
  fake.config.path = "/opt/nipa";
  fake.config.lockRefreshSeconds = 1;
  const api = await activate({ subscriptions } as never);
  return api;
}

const run = (id: string, ...args: unknown[]) => fake.commands.get(id)!(...args);

describe("activate", () => {
  it("opens clones, registers providers and logs the connection", async () => {
    const api = await start();
    expect(connectMock).toHaveBeenCalledWith({ binary: "/opt/nipa" });
    expect(api.model.repositories.map((r) => r.root)).toEqual([roots[0]]);
    expect(fake.contentProviders.has("nipa-blob")).toBe(true);
    expect(fake.decorationProviders).toHaveLength(1);
    expect(fake.output).toContain("started nipa daemon v9");
    expect([...fake.commands.keys()]).toEqual(
      expect.arrayContaining(["nipa.push", "nipa.update", "nipa.stage", "nipa.unstage", "nipa.discard", "nipa.reconnect", "nipa.showOutput"]),
    );
    deactivate();
  });

  it("rediscovers when workspace folders change", async () => {
    const api = await start();
    const second = await makeClone("two");
    roots.push(second);
    fake.foundFiles = [Uri.file(path.join(second, ".nipa", "config"))];
    fake.folderListeners[0]();
    await vi.waitFor(() => expect(api.model.repositories).toHaveLength(2));
  });

  it("offers retry and settings when the daemon cannot start", async () => {
    connectMock.mockRejectedValueOnce(new Error("nipa CLI not found"));
    fake.answers.push("Settings");
    await start();
    expect(fake.messages[0]).toMatchObject({ level: "error", text: "Nipa: nipa CLI not found" });
    expect(fake.executed).toContainEqual({ command: "workbench.action.openSettings", args: ["nipa.path"] });

    fake.reset();
    fake.workspaceFolders = [{ uri: Uri.file(roots[0]) }];
    connectMock.mockRejectedValueOnce(new Error("down"));
    fake.answers.push("Retry");
    const api = await activate({ subscriptions } as never);
    expect(api.model.repositories).toHaveLength(1);
  });

  it("reconnects on demand", async () => {
    const api = await start();
    await run("nipa.reconnect");
    expect(connectMock).toHaveBeenCalledTimes(2);
    expect(api.model.repositories).toHaveLength(1);
    run("nipa.showOutput");
    expect(fake.outputShown).toBe(1);
  });
});

describe("commands", () => {
  it("runs repository commands from the source control or the only clone", async () => {
    const api = await start();
    const repo = api.model.repositories[0];
    await run("nipa.refresh", repo.sc);
    await run("nipa.stageAll", repo.changes);
    expect(daemon.calls.find((c) => c.method === "Stage")?.body.add).toEqual(["a.txt", "b.txt"]);
    await run("nipa.refresh");
    await run("nipa.unstageAll", repo.staged);
    expect(daemon.calls.filter((c) => c.method === "Stage").at(-1)?.body.unstage).toEqual(["s.txt"]);
    await run("nipa.update");
    expect(fake.messages.at(-1)?.text).toBe("Updated main to c.");
    await run("nipa.push");
    expect(fake.messages.at(-1)?.text).toBe("Enter a message before pushing.");
  });

  it("acts on selected resource states, deduplicated", async () => {
    const api = await start();
    const repo = api.model.repositories[0];
    const [a, b] = repo.changes.resourceStates;
    await run("nipa.stage", a, [a, b]);
    expect(daemon.calls.find((c) => c.method === "Stage")?.body.add).toEqual(["a.txt", "b.txt"]);
    await run("nipa.refresh");
    const staged = api.model.repositories[0].staged.resourceStates[0];
    await run("nipa.unstage", staged);
    fake.answers.push("Discard Changes");
    await run("nipa.discard", a);
    expect(daemon.calls.find((c) => c.method === "Restore")?.body.paths).toEqual(["a.txt"]);
    await run("nipa.openFile", a);
    expect(fake.executed.at(-1)).toEqual({ command: "vscode.open", args: [a.resourceUri] });
    await run("nipa.openChange", a, { ...b, command: undefined });
    expect(fake.executed.at(-1)?.command).toBe("vscode.diff");
  });

  it("falls back to the active editor's file", async () => {
    const api = await start();
    fake.activeFile = path.join(roots[0], "b.txt");
    expect(resourceStates(api.model, []).map((s) => s.change.path)).toEqual(["b.txt"]);
    fake.activeFile = path.join(roots[0], "clean.txt");
    expect(resourceStates(api.model, [])).toEqual([]);
    fake.activeFile = "/elsewhere/x";
    expect(resourceStates(api.model, [{ not: "a state" }])).toEqual([]);
  });

  it("picks among several clones", async () => {
    const api = await start();
    expect(await resolveRepository(api.model, undefined)).toBe(api.model.repositories[0]);
    const second = await makeClone("two");
    roots.push(second);
    await api.model.openRepository(second);
    const [one, two] = api.model.repositories;

    expect(await resolveRepository(api.model, two.sc)).toBe(two);
    expect(await resolveRepository(api.model, two.conflicts)).toBe(two);
    expect(await resolveRepository(api.model, { inputBox: {}, createResourceGroup: 1 })).toBe(undefined);

    fake.activeFile = path.join(second, "f.txt");
    expect(await resolveRepository(api.model, undefined)).toBe(two);
    fake.activeFile = "/elsewhere/f.txt";
    fake.quickPick = (items) => items.find((i: { repo: unknown }) => i.repo === one);
    expect(await resolveRepository(api.model, { id: "x", resourceStates: [] })).toBe(one);
  });

  it("explains when no clone is open", async () => {
    fake.workspaceFolders = undefined;
    const api = await start();
    expect(await resolveRepository(api.model, undefined)).toBeUndefined();
    await run("nipa.push");
    expect(fake.messages.at(-1)?.text).toBe("No Nipa clone is open in this workspace.");
  });
});

describe("phase 2 commands", () => {
  it("declares exactly the registered commands", async () => {
    await start();
    const declared = manifest.contributes.commands.map((c) => c.command).sort();
    expect([...fake.commands.keys()].sort()).toEqual(declared);
    const menuCommands = Object.values(manifest.contributes.menus).flatMap((items) => items.map((i: { command: string }) => i.command));
    for (const id of menuCommands) {
      expect(declared).toContain(id);
    }
    expect(fake.executed).toContainEqual({ command: "setContext", args: ["nipa.hasRepositories", true] });
  });

  it("activates for clones nested at any depth", () => {
    expect(manifest.activationEvents).toContain("workspaceContains:**/.nipa/config");
  });

  it("toggles blame through the setting", async () => {
    const api = await start();
    const editor = fakeEditor(path.join(roots[0], "a.txt"), { lines: 1 });
    fake.visibleEditors = [editor];
    await run("nipa.toggleBlame");
    expect(fake.configUpdates).toEqual([{ key: "blame", value: true, target: 1 }]);
    await vi.waitFor(() => expect(editor.decorations).toHaveLength(1));
    await run("nipa.toggleBlame");
    expect(editor.decorations).toEqual([]);
    fake.configListeners.fire({ affectsConfiguration: () => false });
    expect(api.blame).toBeDefined();
  });

  it("opens and copies desktop links and routes lock commands", async () => {
    await start();
    const file = Uri.file(path.join(roots[0], "art", "hero.png"));
    await run("nipa.openInDesktop", file);
    expect(fake.opened).toEqual(["nipa://nipa.example.com/acme/game/file/art/hero.png"]);
    await run("nipa.copyLink", file);
    expect(fake.clipboard).toBe("nipa://nipa.example.com/acme/game/file/art/hero.png");
    await run("nipa.lock", file, [file]);
    expect(daemon.calls.find((c) => c.method === "ProxyLockFile")?.body.request.path).toBe("art/hero.png");
    await run("nipa.lock");
    expect(fake.messages.at(-1)?.text).toBe("Select a file in a Nipa clone first.");
  });

  it("explains missing links and surfaces command failures", async () => {
    daemon.on("WatchRepo", () => ({ json: { repo: null } }));
    await start();
    const file = Uri.file(path.join(roots[0], "a.txt"));
    await run("nipa.openInDesktop", file);
    expect(fake.messages.at(-1)?.text).toBe("This clone has no server address to link to.");
    await run("nipa.copyLink", file);
    expect(fake.clipboard).toBe("");
    daemon.on("LockStatus", () => ({ status: 503, json: { code: "Unavailable", message: "server down" } }));
    await run("nipa.unlock", file);
    expect(fake.messages.at(-1)).toMatchObject({ level: "error", text: "Nipa: server down" });
  });

  it("routes conflict and switch commands", async () => {
    daemon.on("ResolveConflict", () => ({ json: status() }));
    daemon.on("ProxyBranchList", () => ({ json: { response: { branches: [] } } }));
    daemon.on("ProxyTagList", () => ({ json: { response: { tags: [] } } }));
    daemon.on("ProxyLockRequestRelease", () => ({ json: {} }));
    await start();
    const file = Uri.file(path.join(roots[0], "a.txt"));
    await run("nipa.takeOurs", file);
    await run("nipa.takeTheirs", file);
    fake.files.set(file.fsPath, "clean");
    await run("nipa.markResolved", file);
    expect(daemon.calls.filter((c) => c.method === "ResolveConflict").map((c) => c.body.choice)).toEqual(["ours", "theirs", "working"]);
    await run("nipa.compareConflict", file);
    expect(fake.messages.at(-1)?.text).toContain("does not exist on both sides");
    await run("nipa.requestRelease", file);
    expect(fake.messages.at(-1)?.text).toBe("a.txt is not locked by anyone else.");
    fake.quickPick = () => undefined;
    await run("nipa.switch");
    expect(daemon.calls.some((c) => c.method === "ProxyBranchList")).toBe(true);
  });
});
