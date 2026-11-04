import { Events } from "@wailsio/runtime";
import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type {
  Account,
  AudioCompare,
  Branch,
  Commit as RawCommit,
  HistoryQuery,
  RevertOutcome,
  RevertRequest,
  Tag,
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
export type { Branch, HistoryQuery, RevertOutcome, RevertRequest, Tag };
export type Commit = Omit<RawCommit, "parents"> & { parents: string[] };
export type HistoryPage = { commits: Commit[]; next: string };
export type { Account, CloneRequest, RemoteBranch, RemoteDir, RemoteProject };
export type RemoteListing = Omit<RawRemoteListing, "dirs"> & { dirs: RemoteDir[] };
export type LockStatus = Omit<RawLockStatus, "locks" | "files"> & { locks: FileLock[]; files: FileLockState[] };
export type LockResult = { locked: string[]; failed: LockFailure[] };
export type { AudioCompare, AudioSide, AudioStats, MetaWarning, TableColumn, TableCompare, TableRow };
export type { DaemonInfo, DiffHunk, DiffLine, FileChange, FileDiff, FileSide, ImageCompare, ImageInfo, ImageLayer, ImageOptions, ImageRef, ImageStats, ModelCompare, ModelSide, ModelStats, OpOutcome, OpProgress, Repo, RepoChanged };

export type ChangeSet = {
  branch: string;
  detached?: string;
  pending?: "merge" | "revert";
  pendingRemaining?: number;
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
  pending: c.pending === "merge" || c.pending === "revert" ? c.pending : undefined,
  pendingRemaining: c.pendingRemaining ?? 0,
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
  history: async (root: string, q: Partial<HistoryQuery>): Promise<HistoryPage> => {
    const page = await Service.History(root, { branch: "", path: "", start: "", limit: 0, ...q });
    return { next: page?.next ?? "", commits: (page?.commits ?? []).map((c) => ({ ...c, parents: c.parents ?? [] })) };
  },
  commitFiles: async (root: string, commitId: string, parent: string): Promise<FileDiff[]> => (await Service.CommitFiles(root, commitId, parent)) ?? [],
  branches: async (root: string): Promise<Branch[]> => (await Service.Branches(root)) ?? [],
  createBranch: async (root: string, name: string, from: { branch?: string; commit?: string } = {}): Promise<Branch> => {
    const b = await Service.CreateBranch(root, name, from.branch ?? "", from.commit ?? "");
    if (!b) throw new Error("The branch was not created");
    return b;
  },
  deleteBranch: (root: string, name: string): Promise<void> => Service.DeleteBranch(root, name),
  switchBranch: (root: string, branch: string): Cancellable<OpOutcome> => Service.SwitchBranch(root, branch),
  tags: async (root: string): Promise<Tag[]> => (await Service.Tags(root)) ?? [],
  createTag: async (root: string, name: string, from: { branch?: string; commit?: string } = {}, message = ""): Promise<Tag> => {
    const t = await Service.CreateTag(root, name, from.branch ?? "", from.commit ?? "", message);
    if (!t) throw new Error("The tag was not created");
    return t;
  },
  deleteTag: (root: string, name: string): Promise<void> => Service.DeleteTag(root, name),
  checkoutTag: (root: string, tag: string): Cancellable<OpOutcome> => Service.CheckoutTag(root, tag),
  revert: (root: string, req: Partial<RevertRequest>): Cancellable<OpOutcome> => Service.Revert(root, { target: "", mode: "", ...req }),

  onRepoChanged: (fn: (change: RepoChanged) => void): (() => void) => Events.On("repo:changed", (ev) => fn(ev.data)),
  onOpProgress: (fn: (progress: OpProgress) => void): (() => void) => Events.On("op:progress", (ev) => fn(ev.data)),
};
