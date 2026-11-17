import * as http from "node:http";
import type { AddressInfo } from "node:net";

import { DaemonClient } from "../../src/daemon/client";

export type Reply =
  | { json: unknown; status?: number }
  | { lines: unknown[]; hold?: boolean }
  | { raw: string; status: number };

export type Handler = (body: any, req: http.IncomingMessage) => Reply | Promise<Reply>;

export interface FakeDaemon {
  port: number;
  token: string;
  calls: { method: string; body: any; token?: string }[];
  handlers: Map<string, Handler>;
  held: http.ServerResponse[];
  on(method: string, handler: Handler): void;
  client(): DaemonClient;
  release(): void;
  close(): Promise<void>;
}

export async function startFakeDaemon(token = "secret"): Promise<FakeDaemon> {
  const handlers = new Map<string, Handler>();
  const calls: FakeDaemon["calls"] = [];
  const held: http.ServerResponse[] = [];
  const server = http.createServer(async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) {
      chunks.push(chunk as Buffer);
    }
    const text = Buffer.concat(chunks).toString("utf8");
    const body = text ? JSON.parse(text) : {};
    const method = (req.url ?? "").replace(/^\/v1\//, "");
    calls.push({ method, body, token: req.headers["x-nipa-daemon-token"] as string | undefined });
    const handler = handlers.get(method);
    if (!handler) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ code: "Unimplemented", message: `unknown method /v1/${method}` }));
      return;
    }
    const reply = await handler(body, req);
    if ("raw" in reply) {
      res.writeHead(reply.status);
      res.end(reply.raw);
    } else if ("json" in reply) {
      res.writeHead(reply.status ?? 200, { "Content-Type": "application/json" });
      res.end(JSON.stringify(reply.json));
    } else {
      res.writeHead(200, { "Content-Type": "application/x-ndjson" });
      for (const line of reply.lines) {
        res.write(`${typeof line === "string" ? line : JSON.stringify(line)}\n`);
      }
      if (reply.hold) {
        held.push(res);
      } else {
        res.end();
      }
    }
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as AddressInfo).port;
  return {
    port,
    token,
    calls,
    handlers,
    held,
    on(method, handler) {
      handlers.set(method, handler);
    },
    client() {
      return new DaemonClient(`http://127.0.0.1:${port}`, token);
    },
    release() {
      for (const res of held.splice(0)) {
        res.end();
      }
    },
    close() {
      for (const res of held.splice(0)) {
        res.destroy();
      }
      server.closeAllConnections();
      return new Promise((resolve) => server.close(() => resolve()));
    },
  };
}

export function status(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    staged: [],
    deleted: [],
    modified: [],
    untracked: [],
    missing: [],
    conflicts: [],
    branch: "main",
    head: null,
    added: [],
    pending: "",
    pending_remaining: 0,
    resolved: [],
    ...overrides,
  };
}
