import * as fs from "node:fs/promises";
import * as path from "node:path";

import * as vscode from "vscode";

import type { DaemonClient } from "../daemon/client";
import { BlobRef, readBlob } from "../daemon/ops";

export const SNAPSHOT_SCHEME = "nipa-blob";

interface BlobQuery {
  root: string;
  path: string;
  source?: string;
  revision?: string;
}

export function snapshotUri(root: string, rel: string): vscode.Uri {
  return blobUri(root, { source: "snapshot", path: rel });
}

export function blobUri(root: string, ref: BlobRef, label?: string): vscode.Uri {
  const query: BlobQuery = { root, path: ref.path };
  if (ref.source !== "snapshot") {
    query.source = ref.source;
  }
  if (ref.revision) {
    query.revision = ref.revision;
  }
  const segments = [...root.split(path.sep).filter(Boolean), ...(label ? [label] : []), ref.path];
  return vscode.Uri.from({ scheme: SNAPSHOT_SCHEME, path: path.posix.join("/", ...segments), query: JSON.stringify(query) });
}

export function parseSnapshotUri(uri: vscode.Uri): { root: string; path: string } | undefined {
  const q = parseQuery(uri);
  return q ? { root: q.root, path: q.path } : undefined;
}

export function parseBlobUri(uri: vscode.Uri): { root: string; ref: BlobRef } | undefined {
  const q = parseQuery(uri);
  if (!q) {
    return undefined;
  }
  const ref: BlobRef = { source: q.source ?? "snapshot", path: q.path };
  if (q.revision) {
    ref.revision = q.revision;
  }
  return { root: q.root, ref };
}

function parseQuery(uri: vscode.Uri): BlobQuery | undefined {
  try {
    const parsed = JSON.parse(uri.query) as Partial<BlobQuery>;
    if (typeof parsed.root === "string" && typeof parsed.path === "string") {
      return parsed as BlobQuery;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

export class SnapshotContentProvider implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private readonly served = new Map<string, vscode.Uri>();
  private readonly closed: vscode.Disposable;
  readonly onDidChange = this.changed.event;

  constructor(private readonly client: () => Promise<DaemonClient>) {
    this.closed = vscode.workspace.onDidCloseTextDocument((doc) => this.served.delete(doc.uri.toString()));
  }

  async provideTextDocumentContent(uri: vscode.Uri, token: vscode.CancellationToken): Promise<string> {
    const parsed = parseBlobUri(uri);
    if (!parsed) {
      return "";
    }
    this.served.set(uri.toString(), uri);
    const controller = new AbortController();
    const sub = token.onCancellationRequested(() => controller.abort());
    try {
      const blob = await readBlob(await this.client(), parsed.root, parsed.ref, controller.signal);
      if (blob.info?.is_binary) {
        return "";
      }
      return blob.data.toString("utf8");
    } catch {
      return parsed.ref.source === "snapshot" ? readWorking(parsed.root, parsed.ref.path) : "";
    } finally {
      sub.dispose();
    }
  }

  refresh(root: string): void {
    for (const uri of this.served.values()) {
      if (parseSnapshotUri(uri)?.root === root) {
        this.changed.fire(uri);
      }
    }
  }

  dispose(): void {
    this.closed.dispose();
    this.changed.dispose();
    this.served.clear();
  }
}

async function readWorking(root: string, rel: string): Promise<string> {
  try {
    return await fs.readFile(path.join(root, ...rel.split("/")), "utf8");
  } catch {
    return "";
  }
}
