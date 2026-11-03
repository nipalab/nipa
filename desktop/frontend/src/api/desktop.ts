import { Events } from "@wailsio/runtime";
import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type {
  Changes,
  DaemonInfo,
  DiffHunk,
  DiffLine,
  FileChange,
  FileDiff,
  FileSide,
  OpOutcome,
  OpProgress,
  Repo,
  RepoChanged,
} from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";

export type { DaemonInfo, DiffHunk, DiffLine, FileChange, FileDiff, FileSide, OpOutcome, OpProgress, Repo, RepoChanged };

export type ChangeSet = {
  branch: string;
  detached?: string;
  staged: FileChange[];
  unstaged: FileChange[];
  conflicts: string[];
};

// Cancellable is a pending call the UI can abort; Wails cancels the Go
// context, which stops the daemon operation.
export type Cancellable<T> = Promise<T> & { cancel?: () => void };

const changeSet = (c: Changes): ChangeSet => ({
  branch: c.branch,
  detached: c.detached || undefined,
  staged: c.staged ?? [],
  unstaged: c.unstaged ?? [],
  conflicts: c.conflicts ?? [],
});

export const desktop = {
  connect: (): Promise<DaemonInfo> => Service.Connect(),
  repos: async (): Promise<Repo[]> => (await Service.Repos()) ?? [],
  pickRepo: (): Promise<Repo | null> => Service.PickRepo(),
  addRepo: (path: string): Promise<Repo> => Service.AddRepo(path),
  removeRepo: (root: string): Promise<void> => Service.RemoveRepo(root),

  changes: async (root: string): Promise<ChangeSet> => changeSet(await Service.Changes(root)),
  stage: async (root: string, paths: string[]): Promise<ChangeSet> => changeSet(await Service.Stage(root, paths)),
  unstage: async (root: string, paths: string[]): Promise<ChangeSet> => changeSet(await Service.Unstage(root, paths)),
  discard: async (root: string, paths: string[]): Promise<ChangeSet> => changeSet(await Service.Discard(root, paths)),
  fileDiff: (root: string, path: string): Promise<FileDiff | null> => Service.FileDiff(root, path),
  push: (root: string, message: string): Cancellable<OpOutcome> => Service.Push(root, message),
  update: (root: string): Cancellable<OpOutcome> => Service.Update(root),

  onRepoChanged: (fn: (change: RepoChanged) => void): (() => void) => Events.On("repo:changed", (ev) => fn(ev.data)),
  onOpProgress: (fn: (progress: OpProgress) => void): (() => void) => Events.On("op:progress", (ev) => fn(ev.data)),
};
