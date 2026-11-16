import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { FakeDaemon, startFakeDaemon, status } from "./fakes/daemon";
import { fake, Uri } from "./fakes/vscode";

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
  daemon.on("WatchRepo", () => ({ json: { repo: null } }));
  daemon.on("Status", () => ({ json: status({ modified: ["a.txt", "b.txt"], staged: ["s.txt"] }) }));
  daemon.on("Stage", () => ({ json: status() }));
  daemon.on("Restore", () => ({ json: status() }));
  daemon.on("WatchChanges", () => ({ lines: [], hold: true }));
  daemon.on("LockStatus", () => ({ json: { files: [] } }));
  daemon.on("PushPlan", () => ({ json: {} }));
  daemon.on("Update", () => ({ lines: [{ result: { sync: { branch: "main", commit_id: "c" } } }] }));
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
    expect(fake.contentProviders.has("nipa-snapshot")).toBe(true);
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
