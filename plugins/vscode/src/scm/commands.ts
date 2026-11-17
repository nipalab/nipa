import * as vscode from "vscode";

import { describeError } from "../daemon/ops";
import { relativeTo } from "./discovery";
import { nipaLink } from "./helpers";
import type { Model } from "./model";
import type { NipaRepository, ResourceState, SwitchTarget } from "./repository";

export interface CommandHost {
  model: Model;
  output: vscode.OutputChannel;
  reconnect: () => Promise<void>;
  toggleBlame: () => Promise<void>;
}

export interface FileTarget {
  repo: NipaRepository;
  rel: string;
  uri: vscode.Uri;
}

export function registerCommands(host: CommandHost): vscode.Disposable[] {
  const { model } = host;
  const repoCommand = (fn: (repo: NipaRepository) => Promise<void>) => async (arg?: unknown) => {
    const repo = await resolveRepository(model, arg);
    if (repo) {
      await guard(fn(repo));
    }
  };
  const stateCommand = (fn: (repo: NipaRepository, states: ResourceState[]) => Promise<void>) => async (...args: unknown[]) => {
    for (const [repo, list] of byRepo(resourceStates(model, args), (s) => s.repo)) {
      await guard(fn(repo, list));
    }
  };
  const fileCommand = (fn: (repo: NipaRepository, targets: FileTarget[]) => Promise<void>) => async (...args: unknown[]) => {
    const targets = fileTargets(model, args);
    if (!targets.length) {
      void vscode.window.showInformationMessage("Select a file in a Nipa clone first.");
      return;
    }
    for (const [repo, list] of byRepo(targets, (t) => t.repo)) {
      await guard(fn(repo, list));
    }
  };

  return [
    vscode.commands.registerCommand("nipa.refresh", repoCommand((repo) => repo.refresh())),
    vscode.commands.registerCommand("nipa.push", repoCommand((repo) => repo.push())),
    vscode.commands.registerCommand("nipa.update", repoCommand((repo) => repo.update())),
    vscode.commands.registerCommand("nipa.switch", repoCommand((repo) => pickAndSwitch(repo))),
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
    vscode.commands.registerCommand("nipa.lock", fileCommand((repo, t) => repo.lockPaths(t.map((x) => x.rel)))),
    vscode.commands.registerCommand("nipa.unlock", fileCommand((repo, t) => repo.unlockPaths(t.map((x) => x.rel)))),
    vscode.commands.registerCommand("nipa.requestRelease", fileCommand((repo, t) => repo.requestRelease(t[0].rel))),
    vscode.commands.registerCommand("nipa.takeOurs", fileCommand((repo, t) => repo.takeSide(t.map((x) => x.rel), "ours"))),
    vscode.commands.registerCommand("nipa.takeTheirs", fileCommand((repo, t) => repo.takeSide(t.map((x) => x.rel), "theirs"))),
    vscode.commands.registerCommand("nipa.markResolved", fileCommand((repo, t) => repo.markResolved(t.map((x) => x.rel)))),
    vscode.commands.registerCommand("nipa.compareConflict", fileCommand((repo, t) => repo.compareConflict(t[0].rel))),
    vscode.commands.registerCommand(
      "nipa.openInDesktop",
      fileCommand(async (repo, t) => {
        const link = nipaLink(repo.info?.url, ["file", t[0].rel]);
        if (!link) {
          void vscode.window.showWarningMessage("This clone has no server address to link to.");
          return;
        }
        await vscode.env.openExternal(vscode.Uri.parse(link));
      }),
    ),
    vscode.commands.registerCommand(
      "nipa.copyLink",
      fileCommand(async (repo, t) => {
        const link = nipaLink(repo.info?.url, ["file", t[0].rel]);
        if (link) {
          await vscode.env.clipboard.writeText(link);
          void vscode.window.showInformationMessage("Copied the nipa:// link.");
        }
      }),
    ),
    vscode.commands.registerCommand("nipa.toggleBlame", () => host.toggleBlame()),
    vscode.commands.registerCommand("nipa.reconnect", () => host.reconnect()),
    vscode.commands.registerCommand("nipa.showOutput", () => host.output.show()),
  ];
}

async function guard(work: Promise<void>): Promise<void> {
  try {
    await work;
  } catch (err) {
    void vscode.window.showErrorMessage(`Nipa: ${describeError(err)}`);
  }
}

function byRepo<T>(items: T[], repoOf: (item: T) => NipaRepository): Map<NipaRepository, T[]> {
  const out = new Map<NipaRepository, T[]>();
  for (const item of items) {
    const repo = repoOf(item);
    out.set(repo, [...(out.get(repo) ?? []), item]);
  }
  return out;
}

interface TargetItem extends vscode.QuickPickItem {
  target?: SwitchTarget;
}

export async function pickAndSwitch(repo: NipaRepository): Promise<void> {
  const { branches, tags } = await repo.listTargets();
  const current = repo.state?.detachedTag ? undefined : repo.state?.branch;
  const items: TargetItem[] = [
    { label: "Branches", kind: vscode.QuickPickItemKind.Separator },
    ...branches.map((b) => ({
      label: `$(git-branch) ${b.name}`,
      description: [b.name === current ? "current" : "", b.is_default ? "default" : "", b.is_protected ? "protected" : ""].filter(Boolean).join(" · "),
      target: { branch: b.name },
    })),
  ];
  if (tags.length) {
    items.push(
      { label: "Tags", kind: vscode.QuickPickItemKind.Separator },
      ...tags.map((t) => ({
        label: `$(tag) ${t.name}`,
        description: t.name === repo.state?.detachedTag ? "checked out" : t.message.split("\n")[0],
        target: { tag: t.name },
      })),
    );
  }
  const pick = await vscode.window.showQuickPick(items, { placeHolder: `Switch ${repo.name} to a branch, or check out a tag` });
  if (!pick?.target || (pick.target.branch && pick.target.branch === current)) {
    return;
  }
  await repo.switchTo(pick.target);
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
  for (const item of flatten(args)) {
    if (isResourceState(item)) {
      add(item);
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

export function fileTargets(model: Model, args: unknown[]): FileTarget[] {
  const out: FileTarget[] = [];
  const seen = new Set<string>();
  const add = (uri: vscode.Uri) => {
    if (uri.scheme !== "file" || seen.has(uri.fsPath)) {
      return;
    }
    const repo = model.repositoryFor(uri.fsPath);
    const rel = repo && relativeTo(repo.root, uri.fsPath);
    if (repo && rel) {
      seen.add(uri.fsPath);
      out.push({ repo, rel, uri });
    }
  };
  for (const item of flatten(args)) {
    if (isResourceState(item)) {
      add(item.resourceUri);
    } else if (isUri(item)) {
      add(item);
    }
  }
  if (out.length === 0) {
    const active = vscode.window.activeTextEditor?.document.uri;
    if (active) {
      add(active);
    }
  }
  return out;
}

function flatten(args: unknown[]): unknown[] {
  return args.flatMap((arg) => (Array.isArray(arg) ? arg : [arg]));
}

function isUri(arg: unknown): arg is vscode.Uri {
  return !!arg && typeof arg === "object" && "scheme" in arg && "fsPath" in arg;
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
