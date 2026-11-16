import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { DaemonClient, DaemonError } from "../src/daemon/client";
import { connect, processAlive, resolveBinary } from "../src/daemon/connector";
import { defaultEndpointPath, readEndpoint } from "../src/daemon/endpoint";
import { describeError, readBlob, runOp } from "../src/daemon/ops";
import { DaemonSession } from "../src/daemon/session";
import { FakeDaemon, startFakeDaemon } from "./fakes/daemon";

let daemon: FakeDaemon;
let dir: string;

beforeEach(async () => {
  daemon = await startFakeDaemon();
  dir = await fs.mkdtemp(path.join(os.tmpdir(), "nipa-vscode-"));
});

afterEach(async () => {
  await daemon.close();
  await fs.rm(dir, { recursive: true, force: true });
});

async function writeEndpoint(file: string, overrides: Record<string, unknown> = {}) {
  await fs.mkdir(path.dirname(file), { recursive: true });
  await fs.writeFile(
    file,
    JSON.stringify({ pid: process.pid, port: 1, json_port: daemon.port, token: daemon.token, version: "v1.2.3", ...overrides }),
  );
}

describe("endpoint", () => {
  it("follows the OS config dir", () => {
    expect(defaultEndpointPath({ XDG_CONFIG_HOME: "/x" }, "linux")).toBe(path.join("/x", "nipa", "daemon.json"));
    expect(defaultEndpointPath({ HOME: "/h" }, "linux")).toBe(path.join("/h", ".config", "nipa", "daemon.json"));
    expect(defaultEndpointPath({ HOME: "/h" }, "darwin")).toBe(path.join("/h", "Library", "Application Support", "nipa", "daemon.json"));
    expect(defaultEndpointPath({ APPDATA: "/a" }, "win32")).toBe(path.join("/a", "nipa", "daemon.json"));
    expect(defaultEndpointPath({ USERPROFILE: "/u" }, "win32")).toBe(path.join("/u", "AppData", "Roaming", "nipa", "daemon.json"));
    expect(defaultEndpointPath({}, "linux")).toBe(path.join(".config", "nipa", "daemon.json"));
  });

  it("reads valid files and rejects broken ones", async () => {
    const file = path.join(dir, "daemon.json");
    expect(await readEndpoint(file)).toBeUndefined();
    await fs.writeFile(file, "{not json");
    expect(await readEndpoint(file)).toBeUndefined();
    await fs.writeFile(file, JSON.stringify({ pid: "x" }));
    expect(await readEndpoint(file)).toBeUndefined();
    await fs.writeFile(file, JSON.stringify({ pid: 7, token: "t" }));
    expect(await readEndpoint(file)).toEqual({ pid: 7, port: 0, json_port: 0, token: "t", version: "" });
  });
});

describe("DaemonClient", () => {
  it("sends the token and decodes unary answers", async () => {
    daemon.on("Ping", () => ({ json: { version: "v1", pid: 1 } }));
    const res = await daemon.client().call<{ version: string }>("Ping");
    expect(res.version).toBe("v1");
    expect(daemon.calls[0]).toMatchObject({ method: "Ping", token: "secret", body: {} });
  });

  it("maps error bodies, including maintenance", async () => {
    daemon.on("Status", () => ({
      status: 503,
      json: { code: "Unavailable", message: "closed", maintenance: { mode: "full", ends_at: "2026-01-01T00:00:00Z" } },
    }));
    const err = await daemon.client().call("Status").catch((e) => e);
    expect(err).toBeInstanceOf(DaemonError);
    expect(err).toMatchObject({ status: 503, code: "Unavailable", message: "closed" });
    expect(describeError(err)).toContain("estimated end");
  });

  it("falls back to the status text on a non-JSON error", async () => {
    daemon.on("Status", () => ({ raw: "boom", status: 500 }));
    const err = (await daemon.client().call("Status").catch((e) => e)) as DaemonError;
    expect(err.status).toBe(500);
    expect(err.code).toBe("Unknown");
  });

  it("times out unary calls and honours a parent signal", async () => {
    daemon.on("Slow", () => new Promise((resolve) => setTimeout(() => resolve({ json: {} }), 500)));
    await expect(daemon.client().call("Slow", {}, { timeoutMs: 20 })).rejects.toThrow();
    const controller = new AbortController();
    controller.abort();
    await expect(daemon.client().call("Slow", {}, { signal: controller.signal })).rejects.toThrow();
    const late = new AbortController();
    const pending = daemon.client().call("Slow", {}, { signal: late.signal });
    late.abort();
    await expect(pending).rejects.toThrow();
  });

  it("reads NDJSON streams across chunk boundaries", async () => {
    const fetchImpl = (async () =>
      new Response(
        new ReadableStream({
          start(c) {
            c.enqueue(new TextEncoder().encode('{"a":1}\n{"a"'));
            c.enqueue(new TextEncoder().encode(':2}\n\n{"a":3}'));
            c.close();
          },
        }),
      )) as typeof fetch;
    const seen: number[] = [];
    await new DaemonClient("http://x", "t", fetchImpl).stream<{ a: number }>("S", {}, (m) => seen.push(m.a));
    expect(seen).toEqual([1, 2, 3]);
  });

  it("tolerates an empty stream body", async () => {
    const fetchImpl = (async () => new Response(null)) as typeof fetch;
    await new DaemonClient("http://x", "t", fetchImpl).stream("S", {}, () => undefined);
  });

  it("raises stream error lines", async () => {
    daemon.on("Update", () => ({ lines: [{ started: { phase: "x" } }, { error: { code: "Internal", message: "bad" } }] }));
    await expect(daemon.client().stream("Update", {}, () => undefined)).rejects.toThrow("bad");
  });
});

describe("ops", () => {
  it("returns the operation result", async () => {
    daemon.on("Push", () => ({ lines: [{ queued: { ahead: 0 } }, { result: { push: { commit_id: "abc" } } }] }));
    const events: unknown[] = [];
    const result = await runOp(daemon.client(), "Push", { root: "/r" }, (e) => events.push(e));
    expect(result.push?.commit_id).toBe("abc");
    expect(events).toHaveLength(2);
  });

  it("throws failures and missing results", async () => {
    daemon.on("Push", () => ({ lines: [{ failure: { code: 409, message: "locked", maintenance: null } }] }));
    await expect(runOp(daemon.client(), "Push", {})).rejects.toMatchObject({ status: 409, message: "locked" });
    daemon.on("Update", () => ({ lines: [] }));
    await expect(runOp(daemon.client(), "Update", {})).rejects.toThrow("Update ended without a result");
  });

  it("assembles blobs and reports failures", async () => {
    daemon.on("ReadBlob", (body) =>
      body.blob.path === "missing"
        ? { lines: [{ failure: { code: 404, message: "not found" } }] }
        : { lines: [{ info: { path: "a", is_binary: false } }, { data: Buffer.from("he").toString("base64") }, { data: Buffer.from("llo").toString("base64") }] },
    );
    const blob = await readBlob(daemon.client(), "/r", "snapshot", "a");
    expect(blob.data.toString()).toBe("hello");
    expect(blob.info?.path).toBe("a");
    await expect(readBlob(daemon.client(), "/r", "snapshot", "missing")).rejects.toMatchObject({ status: 404 });
  });

  it("describes any error", () => {
    expect(describeError(new DaemonError("x", 400, "InvalidArgument"))).toBe("x");
    expect(describeError(new DaemonError("x", 503, "Unavailable", { mode: "full" }))).toBe("x");
    expect(describeError(new Error("y"))).toBe("y");
    expect(describeError("z")).toBe("z");
  });
});

describe("connector", () => {
  it("attaches to a running daemon", async () => {
    daemon.on("Ping", () => ({ json: { version: "v1.2.3", pid: process.pid } }));
    const file = path.join(dir, "daemon.json");
    await writeEndpoint(file);
    const conn = await connect({ endpointPath: file, start: () => expect.fail("must not start") });
    expect(conn).toMatchObject({ version: "v1.2.3", started: false });
  });

  it("starts the daemon when none answers", async () => {
    daemon.on("Ping", () => ({ json: { version: "v2", pid: process.pid } }));
    const file = path.join(dir, "daemon.json");
    const started: string[][] = [];
    const conn = await connect({
      endpointPath: file,
      binary: "/bin/nipa",
      pollIntervalMs: 5,
      start: (bin, ep) => {
        started.push([bin, ep ?? ""]);
        void writeEndpoint(file, { version: "v2" });
      },
    });
    expect(started).toEqual([["/bin/nipa", file]]);
    expect(conn.started).toBe(true);
  });

  it("ignores a dead pid and an unanswered ping", async () => {
    const file = path.join(dir, "daemon.json");
    await writeEndpoint(file);
    await expect(
      connect({ endpointPath: file, binary: "nipa", alive: () => false, start: () => undefined, startTimeoutMs: 30, pollIntervalMs: 5 }),
    ).rejects.toThrow("did not start");
    await expect(
      connect({ endpointPath: file, binary: "nipa", start: () => undefined, startTimeoutMs: 30, pollIntervalMs: 5 }),
    ).rejects.toThrow("did not start");
  });

  it("refuses a daemon without the JSON bridge", async () => {
    const file = path.join(dir, "daemon.json");
    await writeEndpoint(file, { json_port: 0 });
    await expect(connect({ endpointPath: file })).rejects.toThrow("predates the JSON bridge");
  });

  it("resolves the binary from settings, env and PATH", () => {
    const none = () => false;
    expect(resolveBinary("/cfg/nipa", {}, "linux", none)).toBe("/cfg/nipa");
    expect(resolveBinary(undefined, { NIPA_BIN: "/env/nipa" }, "linux", none)).toBe("/env/nipa");
    expect(resolveBinary(undefined, { PATH: ":/a:/b" }, "linux", (f) => f === path.join("/b", "nipa"))).toBe(path.join("/b", "nipa"));
    expect(resolveBinary(undefined, { Path: "C:\\a" }, "win32", (f) => f.endsWith("nipa.exe"))).toContain("nipa.exe");
    expect(() => resolveBinary(undefined, {}, "linux", none)).toThrow("nipa CLI not found");
  });

  it("checks process liveness", () => {
    expect(processAlive(process.pid)).toBe(true);
    expect(processAlive(2 ** 22 + 12345)).toBe(false);
  });
});

describe("DaemonSession", () => {
  it("shares one connection and notifies listeners", async () => {
    let n = 0;
    const session = new DaemonSession(async () => ({ client: daemon.client(), version: `v${++n}`, started: false }));
    const seen: string[] = [];
    const sub = session.onConnected((c) => seen.push(c.version));
    const [a, b] = await Promise.all([session.connection(), session.connection()]);
    expect(a).toBe(b);
    expect(await session.client()).toBe(a.client);
    expect((await session.reconnect(daemon.client())).version).toBe("v1");
    expect((await session.reconnect(a.client)).version).toBe("v2");
    expect((await session.reconnect()).version).toBe("v3");
    sub.dispose();
    await session.reconnect();
    expect(seen).toEqual(["v1", "v2", "v3"]);
  });

  it("retries after a failed connection", async () => {
    let fail = true;
    const session = new DaemonSession(async () => {
      if (fail) {
        throw new Error("down");
      }
      return { client: daemon.client(), version: "v", started: false };
    });
    await expect(session.connection()).rejects.toThrow("down");
    await expect(session.reconnect(daemon.client())).rejects.toThrow("down");
    fail = false;
    expect((await session.connection()).version).toBe("v");
  });
});
