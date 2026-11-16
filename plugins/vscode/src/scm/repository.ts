import * as path from "node:path";

import * as vscode from "vscode";

import { DaemonClient, DaemonError } from "../daemon/client";
import { describeError, runOp } from "../daemon/ops";
import type { DaemonSession } from "../daemon/session";
import type { FileLockState, LockStatusResponse, OpEvent, OpResult, PushPlanResponse, RepoChange, StatusResponse } from "../daemon/types";
import { Change, ChangeKind, Changes, groupStatus, hasSnapshot, KIND_LABEL, KIND_LETTER } from "./changes";
import { relativeTo } from "./discovery";
import { SnapshotContentProvider, snapshotUri } from "./snapshot";

export interface RepositoryDeps {
  session: DaemonSession;
  snapshots: SnapshotContentProvider;
  log: (line: string) => void;
  onDidChangeDecorations: (uris: vscode.Uri[]) => void;
  lockRefreshMs: number;
  debounceMs?: number;
  retryDelayMs?: number;
}

export type GroupId = "staged" | "changes" | "conflicts";

export interface ResourceState extends vscode.SourceControlResourceState {
  readonly repo: NipaRepository;
  readonly change: Change;
  readonly group: GroupId;
}

export interface FileDecorationInfo {
  letter: string;
  tooltip: string;
  color?: string;
  lock?: FileLockState["state"];
}

const KIND_COLOR: Record<ChangeKind, string> = {
  added: "gitDecoration.addedResourceForeground",
  modified: "gitDecoration.modifiedResourceForeground",
  deleted: "gitDecoration.deletedResourceForeground",
  untracked: "gitDecoration.untrackedResourceForeground",
  conflict: "gitDecoration.conflictingResourceForeground",
  resolved: "gitDecoration.modifiedResourceForeground",
};

const LOCK_TEXT: Record<string, string> = {
  mine: "Locked by you",
  needed: "Needs a lock before push",
  free: "Not locked",
};

export class NipaRepository implements vscode.Disposable, vscode.QuickDiffProvider {
  readonly sc: vscode.SourceControl;
  readonly staged: vscode.SourceControlResourceGroup;
  readonly changes: vscode.SourceControlResourceGroup;
  readonly conflicts: vscode.SourceControlResourceGroup;
  state?: Changes;
  private kinds = new Map<string, ChangeKind>();
  private locks = new Map<string, FileLockState>();
  private busy?: string;
  private disposed = false;
  private refreshing?: Promise<void>;
  private again = false;
  private debounce?: NodeJS.Timeout;
  private lockTimer?: NodeJS.Timeout;
  private watchAbort?: AbortController;
  private watchLoop?: Promise<void>;

  constructor(
    readonly root: string,
    private readonly deps: RepositoryDeps,
  ) {
    this.sc = vscode.scm.createSourceControl("nipa", "Nipa", vscode.Uri.file(root));
    this.sc.quickDiffProvider = this;
    this.sc.acceptInputCommand = { command: "nipa.push", title: "Push", arguments: [this.sc] };
    this.sc.inputBox.placeholder = "Message (Ctrl+Enter to push)";
    this.staged = this.sc.createResourceGroup("staged", "Staged Changes");
    this.changes = this.sc.createResourceGroup("changes", "Changes");
    this.conflicts = this.sc.createResourceGroup("conflicts", "Conflicts");
    this.staged.hideWhenEmpty = true;
    this.conflicts.hideWhenEmpty = true;
  }

  get name(): string {
    return path.basename(this.root);
  }

  async open(): Promise<void> {
    await this.withClient((client) => client.call("WatchRepo", { root: this.root }));
    await this.refresh();
    this.watchLoop = this.watch();
    void this.refreshLocks();
    this.lockTimer = setInterval(() => void this.refreshLocks(), this.deps.lockRefreshMs);
  }

  contains(file: string): boolean {
    return relativeTo(this.root, file) !== undefined;
  }

  provideOriginalResource(uri: vscode.Uri): vscode.Uri | undefined {
    if (uri.scheme !== "file") {
      return undefined;
    }
    const rel = relativeTo(this.root, uri.fsPath);
    if (!rel || rel === ".nipa" || rel.startsWith(".nipa/")) {
      return undefined;
    }
    const kind = this.kinds.get(rel);
    if (kind === "untracked" || kind === "added") {
      return undefined;
    }
    return snapshotUri(this.root, rel);
  }

  decorationFor(file: string): FileDecorationInfo | undefined {
    const rel = relativeTo(this.root, file);
    const kind = rel ? this.kinds.get(rel) : undefined;
    if (!rel || !kind) {
      return undefined;
    }
    const lock = this.locks.get(rel);
    const info: FileDecorationInfo = { letter: KIND_LETTER[kind], tooltip: KIND_LABEL[kind], color: KIND_COLOR[kind] };
    if (lock) {
      info.lock = lock.state;
      info.tooltip += ` · ${lockText(lock)}`;
      if (lock.state === "theirs") {
        info.color = "list.errorForeground";
      } else if (lock.state === "needed") {
        info.color = "list.warningForeground";
      }
    }
    return info;
  }

  refresh(): Promise<void> {
    if (this.refreshing) {
      this.again = true;
      return this.refreshing;
    }
    this.refreshing = (async () => {
      do {
        this.again = false;
        await this.loadStatus();
      } while (this.again && !this.disposed);
    })().finally(() => {
      this.refreshing = undefined;
    });
    return this.refreshing;
  }

  scheduleRefresh(snapshots: boolean): void {
    if (snapshots) {
      this.deps.snapshots.refresh(this.root);
    }
    clearTimeout(this.debounce);
    this.debounce = setTimeout(() => void this.refresh(), this.deps.debounceMs ?? 300);
  }

  async stage(paths: string[]): Promise<void> {
    await this.statusCall("Stage", { root: this.root, add: paths });
  }

  async unstage(paths: string[]): Promise<void> {
    await this.statusCall("Stage", { root: this.root, unstage: paths });
  }

  async stageAll(): Promise<void> {
    const paths = (this.state?.unstaged ?? []).map((c) => c.path);
    if (paths.length) {
      await this.stage(paths);
    }
  }

  async unstageAll(): Promise<void> {
    const paths = (this.state?.staged ?? []).map((c) => c.path);
    if (paths.length) {
      await this.unstage(paths);
    }
  }

  async discard(changes: Change[]): Promise<void> {
    const paths = changes.filter((c) => c.kind !== "untracked").map((c) => c.path);
    if (!paths.length) {
      return;
    }
    const label = paths.length === 1 ? `'${path.posix.basename(paths[0])}'` : `${paths.length} files`;
    const choice = await vscode.window.showWarningMessage(
      `Discard the changes in ${label}? This cannot be undone.`,
      { modal: true },
      "Discard Changes",
    );
    if (choice !== "Discard Changes") {
      return;
    }
    await this.statusCall("Restore", { root: this.root, paths });
    this.deps.snapshots.refresh(this.root);
  }

  async push(): Promise<void> {
    if (this.state?.detachedTag) {
      void vscode.window.showWarningMessage(`HEAD is detached at tag "${this.state.detachedTag}". Switch to a branch before pushing.`);
      return;
    }
    const message = this.sc.inputBox.value.trim();
    if (!message) {
      void vscode.window.showWarningMessage("Enter a message before pushing.");
      return;
    }
    if (!this.state?.staged.length) {
      const tracked = (this.state?.unstaged ?? []).filter((c) => c.kind !== "untracked").map((c) => c.path);
      if (!tracked.length) {
        void vscode.window.showInformationMessage("There are no changes to push.");
        return;
      }
      const choice = await vscode.window.showWarningMessage(
        "There are no staged changes. Stage all changes to tracked files and push them?",
        { modal: true },
        "Stage and Push",
      );
      if (choice !== "Stage and Push") {
        return;
      }
      await this.stage(tracked);
    }
    const warnings = await this.pushWarnings();
    if (warnings.length) {
      const choice = await vscode.window.showWarningMessage(
        "Check before pushing",
        { modal: true, detail: warnings.join("\n") },
        "Push Anyway",
      );
      if (choice !== "Push Anyway") {
        return;
      }
    }
    const result = await this.runOperation("Pushing", "Push", { root: this.root, message });
    if (result) {
      this.sc.inputBox.value = "";
      const id = result.push?.commit_id;
      void vscode.window.showInformationMessage(id ? `Pushed commit ${id}.` : "Pushed.");
    }
  }

  async update(): Promise<void> {
    const result = await this.runOperation("Updating", "Update", { root: this.root });
    if (result?.sync) {
      void vscode.window.showInformationMessage(`Updated ${result.sync.branch} to ${result.sync.commit_id}.`);
    }
  }

  async pushWarnings(): Promise<string[]> {
    const warnings: string[] = [];
    try {
      const plan = await this.withClient((client) => client.call<PushPlanResponse>("PushPlan", { root: this.root }));
      for (const v of plan.asset_violations ?? []) {
        warnings.push(`${v.path}: ${v.message}`);
      }
      for (const b of plan.asset_impact?.broken ?? []) {
        warnings.push(`${b.from}: broken reference to ${b.target || b.raw} (${b.reason})`);
      }
      warnings.push(...(plan.warnings ?? []));
    } catch (err) {
      this.deps.log(`push plan: ${describeError(err)}`);
    }
    await this.refreshLocks();
    const staged = new Set((this.state?.staged ?? []).map((c) => c.path));
    for (const [file, lock] of this.locks) {
      if (staged.has(file) && (lock.state === "theirs" || lock.state === "needed")) {
        warnings.push(`${file}: ${lockText(lock)}`);
      }
    }
    return warnings;
  }

  async runOperation(title: string, method: string, body: object): Promise<OpResult | undefined> {
    if (this.busy) {
      void vscode.window.showWarningMessage(`${this.name}: ${this.busy} is still running.`);
      return undefined;
    }
    this.busy = title;
    this.render();
    try {
      return await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Notification, title: `Nipa: ${title} ${this.name}`, cancellable: true },
        async (progress, token) => {
          const controller = new AbortController();
          const sub = token.onCancellationRequested(() => controller.abort());
          const report = progressReporter(progress);
          try {
            return await this.withClient((client) => runOp(client, method, body, report, controller.signal), false);
          } finally {
            sub.dispose();
          }
        },
      );
    } catch (err) {
      if (!isAbort(err)) {
        void vscode.window.showErrorMessage(`Nipa: ${title} failed: ${describeError(err)}`);
      }
      return undefined;
    } finally {
      this.busy = undefined;
      this.deps.snapshots.refresh(this.root);
      await this.refresh();
      void this.refreshLocks();
    }
  }

  async refreshLocks(): Promise<void> {
    if (this.disposed) {
      return;
    }
    const previous = [...this.locks.keys()];
    if (!this.hasChanges()) {
      this.locks.clear();
    } else {
      try {
        const res = await this.withClient((client) => client.call<LockStatusResponse>("LockStatus", { root: this.root }));
        this.locks = new Map((res.files ?? []).map((f) => [f.path, f]));
      } catch (err) {
        this.deps.log(`lock status: ${describeError(err)}`);
        this.locks.clear();
      }
    }
    const touched = new Set([...previous, ...this.locks.keys()]);
    if (touched.size) {
      this.render();
      this.deps.onDidChangeDecorations([...touched].map((rel) => this.uriOf(rel)));
    }
  }

  dispose(): void {
    this.disposed = true;
    clearTimeout(this.debounce);
    clearInterval(this.lockTimer);
    this.watchAbort?.abort();
    this.sc.dispose();
  }

  async settled(): Promise<void> {
    await this.refreshing;
  }

  async stopWatching(): Promise<void> {
    this.watchAbort?.abort();
    await this.watchLoop;
  }

  uriOf(rel: string): vscode.Uri {
    return vscode.Uri.file(path.join(this.root, ...rel.split("/")));
  }

  private hasChanges(): boolean {
    return !!this.state && this.state.staged.length + this.state.unstaged.length > 0;
  }

  private async loadStatus(): Promise<void> {
    try {
      const status = await this.withClient((client) => client.call<StatusResponse>("Status", { root: this.root }));
      this.apply(status);
    } catch (err) {
      this.deps.log(`status: ${describeError(err)}`);
    }
  }

  private async statusCall(method: string, body: object): Promise<void> {
    try {
      const status = await this.withClient((client) => client.call<StatusResponse>(method, body));
      this.apply(status);
    } catch (err) {
      void vscode.window.showErrorMessage(`Nipa: ${describeError(err)}`);
    }
  }

  private apply(status: StatusResponse): void {
    if (this.disposed) {
      return;
    }
    const before = new Set(this.kinds.keys());
    const changes = groupStatus(status);
    this.state = changes;
    const kinds = new Map<string, ChangeKind>();
    for (const c of changes.staged) {
      kinds.set(c.path, c.kind);
    }
    for (const c of changes.unstaged) {
      kinds.set(c.path, c.kind);
    }
    for (const c of changes.conflicts) {
      kinds.set(c.path, c.kind);
    }
    this.kinds = kinds;
    for (const rel of kinds.keys()) {
      before.add(rel);
    }
    this.render();
    if (before.size) {
      this.deps.onDidChangeDecorations([...before].map((rel) => this.uriOf(rel)));
    }
  }

  private render(): void {
    const state = this.state;
    if (!state || this.disposed) {
      return;
    }
    this.staged.resourceStates = state.staged.map((c) => this.resource(c, "staged"));
    this.changes.resourceStates = state.unstaged.map((c) => this.resource(c, "changes"));
    this.conflicts.resourceStates = state.conflicts.map((c) => this.resource(c, "conflicts"));
    this.sc.count = state.staged.length + state.unstaged.length + state.conflicts.length;

    const head = state.detachedTag ? `$(tag) ${state.detachedTag}` : `$(git-branch) ${state.branch}`;
    const commands: vscode.Command[] = [
      { command: "nipa.refresh", title: head, tooltip: state.detachedTag ? `Detached at tag ${state.detachedTag}` : `Branch ${state.branch}`, arguments: [this.sc] },
    ];
    if (state.pending) {
      const remaining = state.pendingRemaining > 1 ? ` (${state.pendingRemaining} left)` : "";
      commands.push({ command: "nipa.showOutput", title: `$(warning) ${capitalize(state.pending)} pending${remaining}`, tooltip: `A ${state.pending} is waiting to be continued or aborted` });
    }
    commands.push(
      this.busy
        ? { command: "nipa.showOutput", title: `$(sync~spin) ${this.busy}…`, tooltip: this.busy }
        : { command: "nipa.update", title: "$(sync)", tooltip: "Update", arguments: [this.sc] },
    );
    this.sc.statusBarCommands = commands;
    this.sc.inputBox.enabled = !state.detachedTag;
    this.sc.inputBox.placeholder = state.detachedTag
      ? `Detached at tag "${state.detachedTag}": switch to a branch to push`
      : `Message (Ctrl+Enter to push on "${state.branch}")`;
  }

  private resource(change: Change, group: GroupId): ResourceState {
    const resourceUri = this.uriOf(change.path);
    const lock = this.locks.get(change.path);
    const tooltip = lock ? `${KIND_LABEL[change.kind]} · ${lockText(lock)}` : KIND_LABEL[change.kind];
    return {
      repo: this,
      change,
      group,
      resourceUri,
      contextValue: change.kind,
      decorations: {
        strikeThrough: change.kind === "deleted",
        faded: change.kind === "deleted",
        tooltip,
      },
      command: this.openCommand(change, resourceUri),
    };
  }

  private openCommand(change: Change, uri: vscode.Uri): vscode.Command {
    const name = path.posix.basename(change.path);
    if (change.kind === "deleted") {
      return { command: "vscode.open", title: "Open", arguments: [snapshotUri(this.root, change.path)] };
    }
    if (hasSnapshot(change.kind)) {
      return { command: "vscode.diff", title: "Open Changes", arguments: [snapshotUri(this.root, change.path), uri, `${name} (Nipa)`] };
    }
    return { command: "vscode.open", title: "Open", arguments: [uri] };
  }

  private async watch(): Promise<void> {
    while (!this.disposed) {
      const controller = new AbortController();
      this.watchAbort = controller;
      try {
        await this.withClient((client) =>
          client.stream<RepoChange>(
            "WatchChanges",
            { root: this.root },
            (change) => this.scheduleRefresh(!change.paths?.length),
            controller.signal,
          ),
        );
      } catch (err) {
        if (this.disposed || controller.signal.aborted) {
          return;
        }
        this.deps.log(`watch: ${describeError(err)}`);
      }
      if (this.disposed || controller.signal.aborted) {
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, this.deps.retryDelayMs ?? 2_000));
    }
  }

  private async withClient<T>(fn: (client: DaemonClient) => Promise<T>, retry = true): Promise<T> {
    const client = await this.deps.session.client();
    try {
      return await fn(client);
    } catch (err) {
      if (isAbort(err) || !retry) {
        throw err;
      }
      if (err instanceof DaemonError) {
        if (!/is not watched/.test(err.message)) {
          throw err;
        }
        await client.call("WatchRepo", { root: this.root });
        return fn(client);
      }
      const conn = await this.deps.session.reconnect(client);
      await conn.client.call("WatchRepo", { root: this.root });
      return fn(conn.client);
    }
  }
}

function lockText(lock: FileLockState): string {
  if (lock.state === "theirs") {
    return `Locked by ${lock.held_by_name || lock.held_by}`;
  }
  return LOCK_TEXT[lock.state] ?? lock.state;
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function isAbort(err: unknown): boolean {
  return err instanceof Error && (err.name === "AbortError" || (err as { code?: string }).code === "ABORT_ERR");
}

export function progressReporter(progress: vscode.Progress<{ message?: string; increment?: number }>): (event: OpEvent) => void {
  let reported = 0;
  return (event) => {
    if (event.queued) {
      progress.report({ message: `waiting for ${event.queued.ahead} operation(s)` });
    } else if (event.started) {
      progress.report({ message: event.started.phase });
    } else if (event.progress) {
      const done = Number(event.progress.objects_done);
      const total = Number(event.progress.objects_total);
      if (total > 0) {
        const percent = Math.min(100, Math.floor((done / total) * 100));
        progress.report({ message: `${event.progress.phase} ${done}/${total}`, increment: Math.max(0, percent - reported) });
        reported = Math.max(reported, percent);
      } else {
        progress.report({ message: event.progress.phase });
      }
    }
  };
}
