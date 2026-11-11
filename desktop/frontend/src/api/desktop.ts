import { Events } from "@wailsio/runtime";
import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type {
  Account,
  Activity as RawActivity,
  AudioCompare,
  Check,
  Comment,
  CommentInput,
  MergeRequest as RawMergeRequest,
  MergeRequestDetail as RawMergeRequestDetail,
  MergeRequestFilter,
  Mergeability,
  NewMergeRequest,
  Person,
  Review,
  Thread as RawThread,
  TimelineEvent,
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
  ConflictFile,
  ConflictRevision,
  ConflictSet as RawConflictSet,
  ConflictText,
  ConflictTexts,
  MergeOutcome,
  ResolveInput,
  DaemonInfo,
  DiffHunk,
  DiffLine,
  Draft,
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
  LLMSettings,
  LockFailure,
  LockStatus as RawLockStatus,
  MetaWarning,
  BrokenReference,
  BudgetViolation,
  PushChecks as RawPushChecks,
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
  PropChange,
  RefChange,
  SceneChange,
  SceneCompare,
  SceneObject,
  TableColumn,
  TableCompare,
  TableRow,
  IgnoreRules,
  Navigation,
  RepoActivity as RawRepoActivity,
  TeamEvent,
  Release,
  UpdateState as RawUpdateState,
} from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import type { NotifySettings, Settings as RawSettings, Tool } from "../../bindings/github.com/nipalab/nipa/desktop/internal/workspace/models";

export type { IgnoreRules, Navigation, NotifySettings, TeamEvent, Tool };
export type Settings = Omit<RawSettings, "tools"> & { tools: Tool[] };
export type RepoActivity = Omit<RawRepoActivity, "events"> & { events: TeamEvent[] };
const settings = (s: RawSettings): Settings => ({ ...s, tools: s.tools ?? [] });
const activity = (a: RawRepoActivity): RepoActivity => ({ ...a, events: a.events ?? [] });

export type UpdatePhase = "idle" | "checking" | "upToDate" | "available" | "downloading" | "ready" | "error";
export type UpdateState = Omit<RawUpdateState, "phase" | "release" | "error"> & { phase: UpdatePhase; release: Release | null; error: string };
const updatePhases: UpdatePhase[] = ["idle", "checking", "upToDate", "available", "downloading", "ready", "error"];
export const updateState = (u: RawUpdateState | null): UpdateState => ({
  current: u?.current ?? "",
  enabled: u?.enabled ?? false,
  inPlace: u?.inPlace ?? false,
  phase: updatePhases.find((p) => p === u?.phase) ?? "idle",
  release: u?.release ?? null,
  written: u?.written ?? 0,
  total: u?.total ?? 0,
  error: u?.error ?? "",
  checkedAt: u?.checkedAt ?? "",
});
export type { Release };

export type { FileLock, FileLockState, LockFailure };
export type { Branch, HistoryQuery, RevertOutcome, RevertRequest, Tag };
export type { Check, Comment, CommentInput, MergeRequestFilter, Mergeability, NewMergeRequest, Person, Review, TimelineEvent };
export type MergeRequest = Omit<RawMergeRequest, "assignees"> & { assignees: Person[] };
export type MergeRequestDetail = { request: MergeRequest; mergeability: Mergeability; outstanding: string[] };
export type Thread = Omit<RawThread, "comments"> & { comments: Comment[] };
export type Activity = { reviews: Review[]; threads: Thread[]; timeline: TimelineEvent[]; checks: Check[] };
export type MergeRequestPage = { items: MergeRequest[]; next: number };

const mergeRequest = (m: RawMergeRequest): MergeRequest => ({ ...m, assignees: m.assignees ?? [] });
const thread = (t: RawThread): Thread => ({ ...t, comments: t.comments ?? [] });
const detail = (d: RawMergeRequestDetail | null): MergeRequestDetail => {
  if (!d) throw new Error("The merge request was not found");
  return { request: mergeRequest(d.request), mergeability: d.mergeability, outstanding: d.outstanding ?? [] };
};
const required = <T>(v: T | null, what: string): T => {
  if (!v) throw new Error(`No ${what} was returned`);
  return v;
};
export type { ConflictFile, ConflictRevision, ConflictText, ConflictTexts, MergeOutcome, ResolveInput };
export type ConflictSet = Omit<RawConflictSet, "files" | "op"> & { op: "merge" | "revert" | ""; files: ConflictFile[] };
export type ResolveChoice = "ours" | "theirs" | "content" | "working" | "delete";
export type Commit = Omit<RawCommit, "parents"> & { parents: string[] };
export type HistoryPage = { commits: Commit[]; next: string };
export type { Account, CloneRequest, RemoteBranch, RemoteDir, RemoteProject };
export type RemoteListing = Omit<RawRemoteListing, "dirs"> & { dirs: RemoteDir[] };
export type LockStatus = Omit<RawLockStatus, "locks" | "files"> & { locks: FileLock[]; files: FileLockState[] };
export type LockResult = { locked: string[]; failed: LockFailure[] };
export type { BrokenReference, BudgetViolation };
export type PushChecks = Omit<RawPushChecks, "violations" | "broken" | "newlyUnused" | "warnings"> & {
  violations: BudgetViolation[];
  broken: BrokenReference[];
  newlyUnused: string[];
  warnings: string[];
};
export type { AudioCompare, AudioSide, AudioStats, MetaWarning, TableColumn, TableCompare, TableRow };
export type { PropChange, RefChange, SceneChange, SceneCompare, SceneObject };
export type { Draft, LLMSettings };
export type { DaemonInfo, DiffHunk, DiffLine, FileChange, FileDiff, FileSide, ImageCompare, ImageInfo, ImageLayer, ImageOptions, ImageRef, ImageStats, ModelCompare, ModelSide, ModelStats, OpOutcome, OpProgress, Repo, RepoChanged };

export type ChangeSet = {
  branch: string;
  detached?: string;
  pending?: "merge" | "revert";
  pendingRemaining?: number;
  staged: FileChange[];
  unstaged: FileChange[];
  conflicts: string[];
  resolved: string[];
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
  resolved: c.resolved ?? [],
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
  sceneDiff: (root: string, old: ImageRef | null, next: ImageRef | null): Cancellable<SceneCompare | null> => Service.SceneDiff(root, old, next),
  metaWarnings: async (root: string): Promise<MetaWarning[]> => (await Service.MetaWarnings(root)) ?? [],
  pushChecks: async (root: string): Promise<PushChecks> => {
    const c = await Service.PushChecks(root);
    return {
      truncated: c?.truncated ?? false,
      scanned: c?.scanned ?? 0,
      violations: c?.violations ?? [],
      broken: c?.broken ?? [],
      newlyUnused: c?.newlyUnused ?? [],
      warnings: c?.warnings ?? [],
    };
  },
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
  mergeRequests: async (root: string, f: Partial<MergeRequestFilter>): Promise<MergeRequestPage> => {
    const page = await Service.MergeRequests(root, { status: "", search: "", author: "", after: 0, ...f });
    return { next: page?.next ?? 0, items: (page?.items ?? []).map(mergeRequest) };
  },
  mergeRequest: async (root: string, number: number): Promise<MergeRequestDetail> => detail(await Service.MergeRequest(root, number)),
  mergeRequestActivity: async (root: string, number: number): Promise<Activity> => {
    const a: RawActivity | null = await Service.MergeRequestActivity(root, number);
    return { reviews: a?.reviews ?? [], threads: (a?.threads ?? []).map(thread), timeline: a?.timeline ?? [], checks: a?.checks ?? [] };
  },
  mergeRequestCommits: async (root: string, number: number): Promise<Commit[]> => ((await Service.MergeRequestCommits(root, number)) ?? []).map((c) => ({ ...c, parents: c.parents ?? [] })),
  revisionFiles: async (root: string, oldRev: string, newRev: string): Promise<FileDiff[]> => (await Service.RevisionFiles(root, oldRev, newRev)) ?? [],
  createMergeRequest: async (root: string, req: NewMergeRequest): Promise<MergeRequest> => mergeRequest(required(await Service.CreateMergeRequest(root, req), "merge request")),
  editMergeRequest: async (root: string, number: number, title: string, description: string): Promise<MergeRequest> =>
    mergeRequest(required(await Service.EditMergeRequest(root, number, title, description), "merge request")),
  setDraft: async (root: string, number: number, draft: boolean): Promise<MergeRequest> => mergeRequest(required(await Service.SetDraft(root, number, draft), "merge request")),
  mergeMergeRequest: async (root: string, number: number, strategy: string, deleteSource: boolean): Promise<MergeRequestDetail> =>
    detail(await Service.MergeMergeRequest(root, number, strategy, deleteSource)),
  closeMergeRequest: (root: string, number: number): Promise<void> => Service.CloseMergeRequest(root, number),
  reopenMergeRequest: (root: string, number: number): Promise<void> => Service.ReopenMergeRequest(root, number),
  submitReview: (root: string, number: number, state: "approved" | "changes_requested" | "commented", body: string): Promise<void> => Service.SubmitReview(root, number, state, body),
  comment: async (root: string, number: number, input: Partial<CommentInput> & { body: string }): Promise<Thread> =>
    thread(required(await Service.Comment(root, number, { path: "", oldLine: 0, newLine: 0, ...input }), "comment")),
  reply: async (root: string, number: number, threadId: string, body: string): Promise<Comment> => required(await Service.Reply(root, number, threadId, body), "reply"),
  resolveThread: (root: string, number: number, threadId: string, resolved: boolean): Promise<void> => Service.ResolveThread(root, number, threadId, resolved),
  revert: (root: string, req: Partial<RevertRequest>): Cancellable<OpOutcome> => Service.Revert(root, { target: "", mode: "", ...req }),
  merge: (root: string, source: string): Cancellable<OpOutcome> => Service.Merge(root, source),
  abortMerge: (root: string): Cancellable<OpOutcome> => Service.AbortMerge(root),
  conflicts: async (root: string): Promise<ConflictSet> => {
    const set = await Service.Conflicts(root);
    const op = set?.op === "merge" || set?.op === "revert" ? set.op : "";
    const none = { id: "", label: "" };
    return { base: set?.base ?? none, ours: set?.ours ?? none, theirs: set?.theirs ?? none, op, files: set?.files ?? [] };
  },
  conflictTexts: async (root: string, path: string): Promise<ConflictTexts> => required(await Service.ConflictTexts(root, path), "file versions"),
  conflictDiff: (root: string, path: string): Promise<FileDiff | null> => Service.ConflictDiff(root, path),
  resolveConflict: async (root: string, path: string, choice: ResolveChoice, content = ""): Promise<ChangeSet> =>
    changeSet(await Service.ResolveConflict(root, { path, choice, content })),

  activity: async (): Promise<RepoActivity[]> => ((await Service.Activity()) ?? []).map(activity),
  settings: async (): Promise<Settings> => settings(await Service.Settings()),
  saveSettings: async (s: Settings): Promise<Settings> => settings(await Service.SaveSettings(s)),
  uploadWorkers: (): Promise<number> => Service.UploadWorkers(),
  setUploadWorkers: (n: number): Promise<void> => Service.SetUploadWorkers(n),
  restartDaemon: (): Promise<DaemonInfo> => Service.RestartDaemon(),
  llmSettings: async (): Promise<LLMSettings> => required(await Service.LLMSettings(), "AI settings"),
  setLLM: async (endpoint: string, model: string): Promise<LLMSettings> => required(await Service.SetLLM(endpoint, model), "AI settings"),
  setLLMKey: (key: string): Promise<void> => Service.SetLLMKey(key),
  clearLLMKey: (): Promise<void> => Service.ClearLLMKey(),
  testLLM: (endpoint: string, model: string, key: string): Promise<void> => Service.TestLLM(endpoint, model, key),
  generateCommitMessage: async (root: string): Promise<Draft> => required(await Service.GenerateCommitMessage(root), "draft"),
  generateMergeRequest: async (root: string, source: string, target: string): Promise<Draft> => required(await Service.GenerateMergeRequest(root, source, target), "draft"),
  chooseProgram: (): Promise<string> => Service.ChooseProgram(),
  ignoreRules: (root: string): Promise<IgnoreRules> => Service.IgnoreRules(root),
  saveIgnoreRules: (root: string, rules: IgnoreRules): Promise<void> => Service.SaveIgnoreRules(root, rules),
  openLink: (raw: string): Promise<Navigation> => Service.OpenLink(raw),
  takeNavigation: (): Promise<Navigation | null> => Service.TakeNavigation(),
  updateStatus: async (): Promise<UpdateState> => updateState(await Service.UpdateStatus()),
  checkForUpdate: async (): Promise<UpdateState> => updateState(await Service.CheckForUpdate()),
  installUpdate: async (): Promise<UpdateState> => updateState(await Service.InstallUpdate()),
  restartToUpdate: (): Promise<void> => Service.RestartToUpdate(),
  openUpdatePage: (): Promise<void> => Service.OpenUpdatePage(),

  onRepoActivity: (fn: (a: RepoActivity) => void): (() => void) => Events.On("repo:activity", (ev) => fn(activity(ev.data))),
  onNavigate: (fn: () => void): (() => void) => Events.On("app:navigate", () => fn()),
  onRepoChanged: (fn: (change: RepoChanged) => void): (() => void) => Events.On("repo:changed", (ev) => fn(ev.data)),
  onOpProgress: (fn: (progress: OpProgress) => void): (() => void) => Events.On("op:progress", (ev) => fn(ev.data)),
  onUpdate: (fn: (state: UpdateState) => void): (() => void) => Events.On("update:state", (ev) => fn(updateState(ev.data))),
};
