import * as fs from "node:fs/promises";
import * as path from "node:path";

import * as vscode from "vscode";

import type { DaemonClient } from "../daemon/client";
import { readBlob } from "../daemon/ops";

export const SNAPSHOT_SCHEME = "nipa-snapshot";

interface SnapshotQuery {
  root: string;
  path: string;
}

export function snapshotUri(root: string, rel: string): vscode.Uri {
  const query: SnapshotQuery = { root, path: rel };
  return vscode.Uri.from({
    scheme: SNAPSHOT_SCHEME,
    path: path.posix.join("/", ...root.split(path.sep).filter(Boolean), rel),
    query: JSON.stringify(query),
  });
}

export function parseSnapshotUri(uri: vscode.Uri): SnapshotQuery | undefined {
  try {
    const parsed = JSON.parse(uri.query) as Partial<SnapshotQuery>;
    if (typeof parsed.root === "string" && typeof parsed.path === "string") {
      return { root: parsed.root, path: parsed.path };
    }
  } catch {
    return undefined;
  }
  return undefined;
}

export class SnapshotContentProvider implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private readonly served = new Map<string, vscode.Uri>();
  readonly onDidChange = this.changed.event;

  constructor(private readonly client: () => Promise<DaemonClient>) {}

  async provideTextDocumentContent(uri: vscode.Uri, token: vscode.CancellationToken): Promise<string> {
    const query = parseSnapshotUri(uri);
    if (!query) {
      return "";
    }
    this.served.set(uri.toString(), uri);
    const controller = new AbortController();
    const sub = token.onCancellationRequested(() => controller.abort());
    try {
      const blob = await readBlob(await this.client(), query.root, "snapshot", query.path, controller.signal);
      if (blob.info?.is_binary) {
        return "";
      }
      return blob.data.toString("utf8");
    } catch {
      return readWorking(query.root, query.path);
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
