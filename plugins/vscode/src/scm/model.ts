import * as path from "node:path";

import * as vscode from "vscode";

import { describeError } from "../daemon/ops";
import { findRepoRoot } from "./discovery";
import { NipaRepository, RepositoryDeps } from "./repository";

export type RepositoryFactory = (root: string) => NipaRepository;

export class Model implements vscode.Disposable, vscode.FileDecorationProvider {
  private readonly repos = new Map<string, NipaRepository>();
  private readonly decorationsChanged = new vscode.EventEmitter<vscode.Uri[]>();
  readonly onDidChangeFileDecorations = this.decorationsChanged.event;

  constructor(
    private readonly deps: Omit<RepositoryDeps, "onDidChangeDecorations">,
    private readonly findRoot: (start: string) => Promise<string | undefined> = findRepoRoot,
  ) {}

  client() {
    return this.deps.session.client();
  }

  get repositories(): NipaRepository[] {
    return [...this.repos.values()];
  }

  async discover(folders: readonly string[], configs: readonly string[]): Promise<void> {
    const roots = new Set<string>();
    for (const folder of folders) {
      const root = await this.findRoot(folder);
      if (root) {
        roots.add(root);
      }
    }
    for (const config of configs) {
      roots.add(path.dirname(path.dirname(config)));
    }
    for (const [root, repo] of this.repos) {
      if (!roots.has(root)) {
        repo.dispose();
        this.repos.delete(root);
      }
    }
    await Promise.all([...roots].filter((root) => !this.repos.has(root)).map((root) => this.openRepository(root)));
  }

  async openRepository(root: string): Promise<NipaRepository | undefined> {
    const repo = new NipaRepository(root, { ...this.deps, onDidChangeDecorations: (uris) => this.decorationsChanged.fire(uris) });
    this.repos.set(root, repo);
    try {
      await repo.open();
      return repo;
    } catch (err) {
      this.deps.log(`open ${root}: ${describeError(err)}`);
      repo.dispose();
      this.repos.delete(root);
      throw err;
    }
  }

  repositoryFor(file: string): NipaRepository | undefined {
    let best: NipaRepository | undefined;
    for (const repo of this.repos.values()) {
      if (repo.contains(file) && (!best || repo.root.length > best.root.length)) {
        best = repo;
      }
    }
    return best;
  }

  repositoryForControl(sc: vscode.SourceControl): NipaRepository | undefined {
    return this.repositories.find((repo) => repo.sc === sc);
  }

  provideFileDecoration(uri: vscode.Uri): vscode.FileDecoration | undefined {
    if (uri.scheme !== "file") {
      return undefined;
    }
    const info = this.repositoryFor(uri.fsPath)?.decorationFor(uri.fsPath);
    if (!info) {
      return undefined;
    }
    const decoration = new vscode.FileDecoration(info.letter, info.tooltip, info.color ? new vscode.ThemeColor(info.color) : undefined);
    decoration.propagate = info.lock !== "theirs" && info.lock !== "needed";
    return decoration;
  }

  dispose(): void {
    for (const repo of this.repos.values()) {
      repo.dispose();
    }
    this.repos.clear();
    this.decorationsChanged.dispose();
  }
}
