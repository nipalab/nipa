import * as vscode from "vscode";

import { blame } from "../daemon/ops";
import { describeError } from "../daemon/ops";
import type { BlameResult } from "../daemon/types";
import { relativeTo } from "./discovery";
import { BLAME_WIDTH, blameLabels, nipaLink } from "./helpers";
import type { Model } from "./model";
import type { NipaRepository } from "./repository";

export interface BlameHost {
  model: Model;
  enabled: () => boolean;
  log: (line: string) => void;
  now?: () => number;
}

interface Entry {
  result?: BlameResult;
  pending?: Promise<BlameResult | undefined>;
  controller?: AbortController;
}

export class BlameController implements vscode.Disposable {
  private readonly decoration: vscode.TextEditorDecorationType;
  private readonly cache = new Map<string, Entry>();
  private readonly subscriptions: vscode.Disposable[] = [];
  private readonly watched = new Set<NipaRepository>();

  constructor(private readonly host: BlameHost) {
    this.decoration = vscode.window.createTextEditorDecorationType({
      before: {
        color: new vscode.ThemeColor("editorCodeLens.foreground"),
        margin: "0 1.5em 0 0",
        width: `${BLAME_WIDTH + 1}ch`,
      },
    });
    this.subscriptions.push(
      vscode.window.onDidChangeVisibleTextEditors(() => void this.renderVisible()),
      vscode.workspace.onDidChangeTextDocument((e) => this.onEdit(e.document)),
      vscode.workspace.onDidSaveTextDocument((doc) => {
        this.forget(doc.uri.fsPath);
        void this.renderVisible();
      }),
    );
  }

  async renderVisible(): Promise<void> {
    await Promise.all(vscode.window.visibleTextEditors.map((editor) => this.render(editor)));
  }

  async render(editor: vscode.TextEditor): Promise<void> {
    const doc = editor.document;
    if (!this.host.enabled() || doc.uri.scheme !== "file") {
      editor.setDecorations(this.decoration, []);
      return;
    }
    const repo = this.host.model.repositoryFor(doc.uri.fsPath);
    const rel = repo && relativeTo(repo.root, doc.uri.fsPath);
    if (!repo || !rel || doc.isDirty) {
      editor.setDecorations(this.decoration, []);
      return;
    }
    this.watch(repo);
    const result = await this.load(repo, rel, doc.uri.fsPath);
    if (!result || doc.isDirty || !this.host.enabled()) {
      editor.setDecorations(this.decoration, []);
      return;
    }
    editor.setDecorations(this.decoration, this.options(repo, result, doc.lineCount));
  }

  clear(): void {
    for (const editor of vscode.window.visibleTextEditors) {
      editor.setDecorations(this.decoration, []);
    }
  }

  dispose(): void {
    for (const entry of this.cache.values()) {
      entry.controller?.abort();
    }
    this.cache.clear();
    for (const s of this.subscriptions) {
      s.dispose();
    }
    this.decoration.dispose();
  }

  private options(repo: NipaRepository, result: BlameResult, lineCount: number): vscode.DecorationOptions[] {
    const labels = blameLabels(result.lines, result.commits, (this.host.now ?? Date.now)());
    const out: vscode.DecorationOptions[] = [];
    for (let i = 0; i < Math.min(labels.length, lineCount); i++) {
      const label = labels[i];
      out.push({
        range: new vscode.Range(i, 0, i, 0),
        renderOptions: { before: { contentText: label.first ? label.text : " " } },
        hoverMessage: label.first ? hover(repo, label.commit, label.uncommitted) : undefined,
      });
    }
    return out;
  }

  private load(repo: NipaRepository, rel: string, file: string): Promise<BlameResult | undefined> {
    const entry = this.cache.get(file);
    if (entry?.result) {
      return Promise.resolve(entry.result);
    }
    if (entry?.pending) {
      return entry.pending;
    }
    const controller = new AbortController();
    const next: Entry = { controller };
    next.pending = (async () => {
      try {
        const result = await blame(await this.host.model.client(), repo.root, rel, controller.signal);
        if (this.cache.get(file) === next) {
          next.result = result;
        }
        return result;
      } catch (err) {
        if (!controller.signal.aborted) {
          this.host.log(`blame ${rel}: ${describeError(err)}`);
        }
        if (this.cache.get(file) === next) {
          this.cache.delete(file);
        }
        return undefined;
      } finally {
        next.pending = undefined;
      }
    })();
    this.cache.set(file, next);
    return next.pending;
  }

  private onEdit(doc: vscode.TextDocument): void {
    if (!doc.isDirty) {
      return;
    }
    for (const editor of vscode.window.visibleTextEditors) {
      if (editor.document === doc) {
        editor.setDecorations(this.decoration, []);
      }
    }
  }

  private forget(file: string): void {
    const entry = this.cache.get(file);
    entry?.controller?.abort();
    this.cache.delete(file);
  }

  private watch(repo: NipaRepository): void {
    if (this.watched.has(repo)) {
      return;
    }
    this.watched.add(repo);
    this.subscriptions.push(
      repo.onDidChangeState(() => {
        for (const file of [...this.cache.keys()]) {
          if (repo.contains(file)) {
            this.forget(file);
          }
        }
        if (this.host.enabled()) {
          void this.renderVisible();
        }
      }),
    );
  }
}

function hover(repo: NipaRepository, commit: { id: string; author_name: string; author_email: string; message: string; created_at: string } | undefined, uncommitted: boolean): vscode.MarkdownString {
  const md = new vscode.MarkdownString();
  if (uncommitted) {
    md.appendMarkdown("**Not pushed yet**\n\nThis line is in your working file but not in the synced version.");
    return md;
  }
  if (!commit) {
    md.appendMarkdown("**Older history**\n\nThis line is older than the history blame reads.");
    return md;
  }
  md.appendMarkdown(`**${escape(commit.author_name)}**`);
  if (commit.author_email) {
    md.appendMarkdown(` <${escape(commit.author_email)}>`);
  }
  md.appendMarkdown(`\n\n${escape(commit.message)}\n\n`);
  md.appendMarkdown(`\`${commit.id}\` · ${new Date(commit.created_at).toLocaleString()}`);
  const link = nipaLink(repo.info?.url, ["commits", commit.id]);
  if (link) {
    md.appendMarkdown(` · [Open in Nipa Desktop](${link})`);
  }
  return md;
}

function escape(text: string): string {
  return text.replace(/[\\`*_{}[\]()#+\-.!<>|]/g, "\\$&");
}
