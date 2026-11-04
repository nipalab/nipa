import { Events } from "@wailsio/runtime";
import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type {
  AudioCompare,
  AudioSide,
  AudioStats,
  Changes,
  DaemonInfo,
  DiffHunk,
  DiffLine,
  FileChange,
  FileDiff,
  FileSide,
  ImageCompare,
  ImageInfo,
  ImageLayer,
  ImageOptions,
  ImageRef,
  ImageStats,
  MetaWarning,
  ModelCompare,
  ModelSide,
  ModelStats,
  OpOutcome,
  OpProgress,
  Repo,
  RepoChanged,
  TableColumn,
  TableCompare,
  TableRow,
} from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";

export type { AudioCompare, AudioSide, AudioStats, MetaWarning, TableColumn, TableCompare, TableRow };
export type { DaemonInfo, DiffHunk, DiffLine, FileChange, FileDiff, FileSide, ImageCompare, ImageInfo, ImageLayer, ImageOptions, ImageRef, ImageStats, ModelCompare, ModelSide, ModelStats, OpOutcome, OpProgress, Repo, RepoChanged };

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
  imageDiff: (root: string, old: ImageRef | null, next: ImageRef | null, opts: Partial<ImageOptions> = {}): Cancellable<ImageCompare | null> =>
    Service.ImageDiff(root, old, next, { maxSize: 2048, level: 0, face: 0, ...opts }),
  modelDiff: (root: string, old: ImageRef | null, next: ImageRef | null): Cancellable<ModelCompare | null> => Service.ModelDiff(root, old, next),
  audioDiff: (root: string, old: ImageRef | null, next: ImageRef | null): Cancellable<AudioCompare | null> => Service.AudioDiff(root, old, next),
  tableDiff: (root: string, old: ImageRef | null, next: ImageRef | null): Cancellable<TableCompare | null> => Service.TableDiff(root, old, next),
  metaWarnings: async (root: string): Promise<MetaWarning[]> => (await Service.MetaWarnings(root)) ?? [],
  push: (root: string, message: string): Cancellable<OpOutcome> => Service.Push(root, message),
  update: (root: string): Cancellable<OpOutcome> => Service.Update(root),

  onRepoChanged: (fn: (change: RepoChanged) => void): (() => void) => Events.On("repo:changed", (ev) => fn(ev.data)),
  onOpProgress: (fn: (progress: OpProgress) => void): (() => void) => Events.On("op:progress", (ev) => fn(ev.data)),
};
