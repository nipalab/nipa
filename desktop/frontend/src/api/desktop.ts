import { Events } from "@wailsio/runtime";
import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type {
  Account,
  AudioCompare,
  AudioSide,
  AudioStats,
  Changes,
  CloneRequest,
  DaemonInfo,
  DiffHunk,
  DiffLine,
  FileChange,
  FileDiff,
  FileLock,
  FileLockState,
  FileSide,
  ImageCompare,
  ImageInfo,
  ImageLayer,
  ImageOptions,
  ImageRef,
  ImageStats,
  LockFailure,
  LockStatus as RawLockStatus,
  MetaWarning,
  ModelCompare,
  ModelSide,
  ModelStats,
  OpOutcome,
  OpProgress,
  RemoteBranch,
  RemoteDir,
  RemoteListing as RawRemoteListing,
  RemoteProject,
  Repo,
  RepoChanged,
  TableColumn,
  TableCompare,
  TableRow,
} from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";

export type { FileLock, FileLockState, LockFailure };
export type { Account, CloneRequest, RemoteBranch, RemoteDir, RemoteProject };
export type RemoteListing = Omit<RawRemoteListing, "dirs"> & { dirs: RemoteDir[] };
export type LockStatus = Omit<RawLockStatus, "locks" | "files"> & { locks: FileLock[]; files: FileLockState[] };
export type LockResult = { locked: string[]; failed: LockFailure[] };
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
  lockStatus: async (root: string): Promise<LockStatus> => {
    const s = await Service.LockStatus(root);
    if (!s) throw new Error("No lock information");
    return { ...s, locks: s.locks ?? [], files: s.files ?? [] };
  },
  lock: async (root: string, branch: string, paths: string[]): Promise<LockResult> => {
    const r = await Service.Lock(root, branch, paths);
    return { locked: r?.locked ?? [], failed: r?.failed ?? [] };
  },
  unlock: (root: string, branch: string, path: string): Promise<void> => Service.Unlock(root, branch, path),
  pickFile: (root: string): Promise<string> => Service.PickFile(root),
  openFile: (root: string, path: string): Promise<void> => Service.OpenFile(root, path),
  accountStatus: async (server: string): Promise<Account> => {
    const a = await Service.AccountStatus(server);
    if (!a) throw new Error("No account information");
    return a;
  },
  signIn: (server: string, email: string, password: string): Promise<void> => Service.SignIn(server, email, password),
  signInSSH: (server: string, email = ""): Promise<void> => Service.SignInSSH(server, email),
  listProjects: async (server: string): Promise<RemoteProject[]> => (await Service.ListProjects(server)) ?? [],
  remoteBranches: async (server: string, org: string, project: string): Promise<RemoteBranch[]> => (await Service.RemoteBranches(server, org, project)) ?? [],
  remoteTree: async (server: string, org: string, project: string, branch: string, path: string): Promise<RemoteListing> => {
    const l = await Service.RemoteTree(server, org, project, branch, path);
    return { files: l?.files ?? 0, fileBytes: l?.fileBytes ?? 0, dirs: l?.dirs ?? [] };
  },
  chooseFolder: (title: string): Promise<string> => Service.ChooseFolder(title),
  clone: (req: CloneRequest): Cancellable<Repo | null> => Service.Clone(req),
  setSparse: (root: string, paths: string[]): Cancellable<OpOutcome> => Service.SetSparse(root, paths),
  push: (root: string, message: string): Cancellable<OpOutcome> => Service.Push(root, message),
  update: (root: string): Cancellable<OpOutcome> => Service.Update(root),

  onRepoChanged: (fn: (change: RepoChanged) => void): (() => void) => Events.On("repo:changed", (ev) => fn(ev.data)),
  onOpProgress: (fn: (progress: OpProgress) => void): (() => void) => Events.On("op:progress", (ev) => fn(ev.data)),
};
