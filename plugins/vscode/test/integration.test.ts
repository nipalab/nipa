import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";

import { afterAll, describe, expect, it } from "vitest";

import { DaemonError } from "../src/daemon/client";
import { Connection, connect } from "../src/daemon/connector";
import type { PingResponse } from "../src/daemon/types";

const bin = process.env.NIPA_VSCODE_DAEMON_BIN;

describe.skipIf(!bin)("real nipa serve", () => {
  let dir: string;
  let conn: Connection | undefined;

  afterAll(async () => {
    await conn?.client.call("Shutdown").catch(() => undefined);
    if (dir) {
      await fs.rm(dir, { recursive: true, force: true });
    }
  });

  it("starts the daemon and speaks the JSON bridge", async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "nipa-vscode-it-"));
    conn = await connect({ binary: bin, endpointPath: path.join(dir, "daemon.json"), startTimeoutMs: 20_000 });
    expect(conn.started).toBe(true);
    const pong = await conn.client.call<PingResponse>("Ping");
    expect(pong.version).toBe(conn.version);

    const err = await conn.client.call("WatchRepo", { root: dir }).catch((e) => e);
    expect(err).toBeInstanceOf(DaemonError);
    expect((err as DaemonError).status).toBeGreaterThanOrEqual(400);

    const notWatched = await conn.client.call("Status", { root: dir }).catch((e) => e);
    expect(notWatched).toBeInstanceOf(DaemonError);
    expect((notWatched as DaemonError).message).toMatch(/not watched|not a nipa working copy/);
  });
});
