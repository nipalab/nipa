import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

import { DaemonClient } from "./client";
import { defaultEndpointPath, Endpoint, Env, readEndpoint } from "./endpoint";
import type { PingResponse } from "./types";

export const ENV_NIPA_BIN = "NIPA_BIN";

export interface ConnectorOptions {
  binary?: string;
  endpointPath?: string;
  env?: Env;
  platform?: NodeJS.Platform;
  startTimeoutMs?: number;
  pollIntervalMs?: number;
  start?: (bin: string, endpointPath?: string) => void | Promise<void>;
  alive?: (pid: number) => boolean;
  exists?: (file: string) => boolean;
  fetchImpl?: typeof fetch;
}

export interface Connection {
  client: DaemonClient;
  version: string;
  started: boolean;
}

export async function connect(opts: ConnectorOptions = {}): Promise<Connection> {
  const env = opts.env ?? process.env;
  const platform = opts.platform ?? process.platform;
  const file = opts.endpointPath || defaultEndpointPath(env, platform);
  const alive = opts.alive ?? processAlive;

  const existing = await attach(file, alive, opts.fetchImpl);
  if (existing) {
    return existing;
  }

  const bin = resolveBinary(opts.binary, env, platform, opts.exists ?? fs.existsSync);
  await (opts.start ?? startDetached)(bin, opts.endpointPath || undefined);

  const deadline = Date.now() + (opts.startTimeoutMs ?? 10_000);
  for (;;) {
    const conn = await attach(file, alive, opts.fetchImpl);
    if (conn) {
      return { ...conn, started: true };
    }
    if (Date.now() >= deadline) {
      throw new Error(`nipa daemon did not start within ${Math.round((opts.startTimeoutMs ?? 10_000) / 1000)}s`);
    }
    await delay(opts.pollIntervalMs ?? 100);
  }
}

async function attach(file: string, alive: (pid: number) => boolean, fetchImpl?: typeof fetch): Promise<Connection | undefined> {
  const ep = await readEndpoint(file);
  if (!ep || !alive(ep.pid)) {
    return undefined;
  }
  if (ep.json_port <= 0) {
    throw new Error(
      `The running nipa daemon (pid ${ep.pid}, version ${ep.version}) predates the JSON bridge. Stop it and update the nipa CLI, then reconnect.`,
    );
  }
  const client = clientFor(ep, fetchImpl);
  try {
    const pong = await client.call<PingResponse>("Ping", {}, { timeoutMs: 2_000 });
    return { client, version: pong.version, started: false };
  } catch {
    return undefined;
  }
}

export function clientFor(ep: Endpoint, fetchImpl?: typeof fetch): DaemonClient {
  return new DaemonClient(`http://127.0.0.1:${ep.json_port}`, ep.token, fetchImpl);
}

export function resolveBinary(configured: string | undefined, env: Env, platform: NodeJS.Platform, exists: (file: string) => boolean): string {
  if (configured) {
    return existing(configured, "the nipa.path setting", exists);
  }
  const fromEnv = env[ENV_NIPA_BIN];
  if (fromEnv) {
    return existing(fromEnv, ENV_NIPA_BIN, exists);
  }
  const name = platform === "win32" ? "nipa.exe" : "nipa";
  const sep = platform === "win32" ? ";" : ":";
  const pathVar = env.PATH ?? env.Path ?? "";
  for (const dir of pathVar.split(sep)) {
    if (!dir) {
      continue;
    }
    const candidate = path.join(dir, name);
    if (exists(candidate)) {
      return candidate;
    }
  }
  throw new Error("nipa CLI not found: set nipa.path in the settings, NIPA_BIN, or add it to PATH");
}

function existing(file: string, source: string, exists: (file: string) => boolean): string {
  if (!exists(file)) {
    throw new Error(`nipa CLI not found at ${file} (from ${source})`);
  }
  return file;
}

export function startDetached(bin: string, endpointPath?: string): Promise<void> {
  const args = endpointPath ? ["serve", "--endpoint", endpointPath] : ["serve"];
  return new Promise((resolve, reject) => {
    const child = spawn(bin, args, { detached: true, stdio: "ignore", windowsHide: true });
    child.once("error", (err) => reject(new Error(`could not start ${bin} serve: ${err.message}`)));
    child.once("spawn", () => {
      child.unref();
      resolve();
    });
  });
}

export function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM";
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
