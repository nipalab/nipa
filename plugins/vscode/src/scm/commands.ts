import * as vscode from "vscode";

import type { Model } from "./model";
import type { NipaRepository, ResourceState } from "./repository";

export interface CommandHost {
  model: Model;
  output: vscode.OutputChannel;
  reconnect: () => Promise<void>;
}

export function registerCommands(host: CommandHost): vscode.Disposable[] {
  const { model } = host;
  const repoCommand = (fn: (repo: NipaRepository) => Promise<void>) => async (arg?: unknown) => {
    const repo = await resolveRepository(model, arg);
    if (repo) {
      await fn(repo);
    }
  };
  const stateCommand = (fn: (repo: NipaRepository, states: ResourceState[]) => Promise<void>) => async (...args: unknown[]) => {
    const states = resourceStates(model, args);
    const byRepo = new Map<NipaRepository, ResourceState[]>();
    for (const state of states) {
      byRepo.set(state.repo, [...(byRepo.get(state.repo) ?? []), state]);
    }
    for (const [repo, list] of byRepo) {
      await fn(repo, list);
    }
  };

  return [
    vscode.commands.registerCommand("nipa.refresh", repoCommand((repo) => repo.refresh())),
    vscode.commands.registerCommand("nipa.push", repoCommand((repo) => repo.push())),
    vscode.commands.registerCommand("nipa.update", repoCommand((repo) => repo.update())),
    vscode.commands.registerCommand("nipa.stageAll", repoCommand((repo) => repo.stageAll())),
    vscode.commands.registerCommand("nipa.unstageAll", repoCommand((repo) => repo.unstageAll())),
    vscode.commands.registerCommand("nipa.stage", stateCommand((repo, states) => repo.stage(states.map((s) => s.change.path)))),
    vscode.commands.registerCommand("nipa.unstage", stateCommand((repo, states) => repo.unstage(states.map((s) => s.change.path)))),
    vscode.commands.registerCommand("nipa.discard", stateCommand((repo, states) => repo.discard(states.map((s) => s.change)))),
    vscode.commands.registerCommand(
      "nipa.openFile",
      stateCommand(async (_repo, states) => {
        for (const state of states) {
          await vscode.commands.executeCommand("vscode.open", state.resourceUri);
        }
      }),
    ),
    vscode.commands.registerCommand(
      "nipa.openChange",
      stateCommand(async (_repo, states) => {
        for (const state of states) {
          if (state.command) {
            await vscode.commands.executeCommand(state.command.command, ...(state.command.arguments ?? []));
          }
        }
      }),
    ),
    vscode.commands.registerCommand("nipa.reconnect", () => host.reconnect()),
    vscode.commands.registerCommand("nipa.showOutput", () => host.output.show()),
  ];
}

export async function resolveRepository(model: Model, arg: unknown): Promise<NipaRepository | undefined> {
  if (isSourceControl(arg)) {
    const repo = model.repositoryForControl(arg);
    if (repo) {
      return repo;
    }
  }
  if (isResourceGroup(arg)) {
    const repo = model.repositories.find((r) => r.staged === arg || r.changes === arg || r.conflicts === arg);
    if (repo) {
      return repo;
    }
  }
  const repos = model.repositories;
  if (repos.length === 0) {
    void vscode.window.showInformationMessage("No Nipa clone is open in this workspace.");
    return undefined;
  }
  if (repos.length === 1) {
    return repos[0];
  }
  const active = vscode.window.activeTextEditor?.document.uri;
  if (active?.scheme === "file") {
    const repo = model.repositoryFor(active.fsPath);
    if (repo) {
      return repo;
    }
  }
  const pick = await vscode.window.showQuickPick(
    repos.map((repo) => ({ label: repo.name, description: repo.root, repo })),
    { placeHolder: "Choose a Nipa clone" },
  );
  return pick?.repo;
}

export function resourceStates(model: Model, args: unknown[]): ResourceState[] {
  const out: ResourceState[] = [];
  const seen = new Set<string>();
  const add = (state: ResourceState) => {
    const key = `${state.repo.root}\0${state.group}\0${state.change.path}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push(state);
    }
  };
  for (const arg of args) {
    for (const item of Array.isArray(arg) ? arg : [arg]) {
      if (isResourceState(item)) {
        add(item);
      }
    }
  }
  if (out.length === 0) {
    const active = vscode.window.activeTextEditor?.document.uri;
    if (active?.scheme === "file") {
      const repo = model.repositoryFor(active.fsPath);
      const state = repo && [...repo.changes.resourceStates, ...repo.staged.resourceStates].find((s) => s.resourceUri.fsPath === active.fsPath);
      if (state && isResourceState(state)) {
        add(state);
      }
    }
  }
  return out;
}

function isSourceControl(arg: unknown): arg is vscode.SourceControl {
  return !!arg && typeof arg === "object" && "inputBox" in arg && "createResourceGroup" in arg;
}

function isResourceGroup(arg: unknown): arg is vscode.SourceControlResourceGroup {
  return !!arg && typeof arg === "object" && "resourceStates" in arg && "id" in arg;
}

function isResourceState(arg: unknown): arg is ResourceState {
  return !!arg && typeof arg === "object" && "repo" in arg && "change" in arg && "resourceUri" in arg;
}
