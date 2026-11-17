import * as vscode from "vscode";

import { connect } from "./daemon/connector";
import { describeError } from "./daemon/ops";
import { DaemonSession } from "./daemon/session";
import { BlameController } from "./scm/blame";
import { registerCommands } from "./scm/commands";
import { Model } from "./scm/model";
import { SNAPSHOT_SCHEME, SnapshotContentProvider } from "./scm/snapshot";

export interface Activation {
  model: Model;
  blame: BlameController;
  session: DaemonSession;
  rediscover: () => Promise<void>;
}

export async function activate(context: vscode.ExtensionContext): Promise<Activation> {
  const output = vscode.window.createOutputChannel("Nipa");
  const config = () => vscode.workspace.getConfiguration("nipa");
  const session = new DaemonSession(async () => {
    const conn = await connect({ binary: config().get<string>("path") || undefined });
    output.appendLine(`${conn.started ? "started" : "attached to"} nipa daemon ${conn.version}`);
    return conn;
  });
  const log = (line: string) => output.appendLine(line);
  const snapshots = new SnapshotContentProvider(() => session.client());
  const model = new Model({
    session,
    snapshots,
    log,
    lockRefreshMs: Math.max(5, config().get<number>("lockRefreshSeconds") ?? 30) * 1000,
    lockWarnings: () => config().get<boolean>("lockWarnings") !== false,
  });
  const blameEnabled = () => config().get<boolean>("blame") === true;
  const blame = new BlameController({ model, enabled: blameEnabled, log });

  const rediscover = async () => {
    const folders = (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath);
    const configs = (await vscode.workspace.findFiles("**/.nipa/config", "**/node_modules/**", 50)).map((u) => u.fsPath);
    try {
      await model.discover(folders, configs);
      await vscode.commands.executeCommand("setContext", "nipa.hasRepositories", model.repositories.length > 0);
      void blame.renderVisible();
    } catch (err) {
      const choice = await vscode.window.showErrorMessage(`Nipa: ${describeError(err)}`, "Retry", "Settings");
      if (choice === "Retry") {
        await rediscover();
      } else if (choice === "Settings") {
        await vscode.commands.executeCommand("workbench.action.openSettings", "nipa.path");
      }
    }
  };

  context.subscriptions.push(
    output,
    snapshots,
    model,
    blame,
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("nipa.blame")) {
        if (blameEnabled()) {
          void blame.renderVisible();
        } else {
          blame.clear();
        }
      }
    }),
    vscode.workspace.registerTextDocumentContentProvider(SNAPSHOT_SCHEME, snapshots),
    vscode.window.registerFileDecorationProvider(model),
    vscode.workspace.onDidChangeWorkspaceFolders(() => void rediscover()),
    ...registerCommands({
      model,
      output,
      toggleBlame: async () => {
        await config().update("blame", !blameEnabled(), vscode.ConfigurationTarget.Global);
      },
      reconnect: async () => {
        await session.reconnect();
        await rediscover();
        await Promise.all(model.repositories.map((repo) => repo.refresh()));
      },
    }),
  );

  await rediscover();
  return { model, blame, session, rediscover };
}

export function deactivate(): void {}
