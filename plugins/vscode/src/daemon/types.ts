export interface PingResponse {
  version: string;
  pid: number;
}

export interface RepoInfo {
  root: string;
  url: string;
  branch: string;
  sparse: string[];
}

export interface WatchRepoResponse {
  repo: RepoInfo | null;
}

export interface StatusHead {
  kind: string;
  name: string;
}

export interface StatusResponse {
  staged: string[];
  deleted: string[];
  modified: string[];
  untracked: string[];
  missing: string[];
  conflicts: string[];
  branch: string;
  head: StatusHead | null;
  added: string[];
  pending: string;
  pending_remaining: number;
  resolved: string[];
}

export interface MaintenanceInfo {
  scope?: string;
  mode?: string;
  message?: string;
  starts_at?: string | null;
  ends_at?: string | null;
  org?: string;
  project?: string;
}

export interface OpFailure {
  code: number;
  message: string;
  maintenance?: MaintenanceInfo | null;
}

export interface OpProgress {
  phase: string;
  objects_done: string;
  objects_total: string;
  bytes_done: string;
  bytes_total: string;
}

export interface SyncResult {
  branch: string;
  commit_id: string;
}

export interface PushResult {
  commit_id: string;
  commit_hash: string;
  tree_hash: string;
}

export interface OpResult {
  sync?: SyncResult;
  push?: PushResult;
  [outcome: string]: unknown;
}

export interface OpEvent {
  queued?: { ahead: number };
  started?: { phase: string };
  progress?: OpProgress;
  result?: OpResult;
  failure?: OpFailure;
}

export interface BlobInfo {
  path: string;
  size_bytes: string;
  is_binary: boolean;
  hash: string;
  kind: string;
}

export interface BlobEvent {
  info?: BlobInfo;
  data?: string;
  failure?: OpFailure;
}

export interface RepoChange {
  paths: string[];
}

export interface FileLockState {
  path: string;
  state: "mine" | "theirs" | "needed" | "free" | string;
  lock_path: string;
  held_by: string;
  held_by_name: string;
  tracked: boolean;
}

export interface LockStatusResponse {
  user_id: string;
  branch: string;
  default_branch: boolean;
  can_release_any: boolean;
  locks: FileLockDetail[];
  files: FileLockState[];
}

export interface AssetViolation {
  path: string;
  message: string;
}

export interface BrokenReference {
  from: string;
  raw: string;
  target: string;
  reason: string;
}

export interface PushPlanResponse {
  up_to_date: boolean;
  changes: { path: string; status: string; binary: boolean; size_bytes: string }[];
  upload_objects: number;
  upload_bytes: string;
  asset_violations: AssetViolation[];
  asset_impact: { broken: BrokenReference[]; newly_unused: string[]; truncated: boolean } | null;
  warnings: string[];
}

export interface FileLockDetail {
  id: string;
  path: string;
  branch: string;
  global: boolean;
  held_by: string;
  held_by_name: string;
  merge_request_number?: string | null;
  acquired_at?: string | null;
  expires_at?: string | null;
}

export interface BlameLine {
  text: string;
  commit_id: string;
  uncommitted: boolean;
}

export interface BlameCommit {
  id: string;
  hash: string;
  author_name: string;
  author_email: string;
  message: string;
  created_at: string;
}

export interface BlameResult {
  path: string;
  revision: string;
  lines: BlameLine[];
  commits: BlameCommit[];
  truncated: boolean;
}

export interface BlameEvent {
  progress?: { done: number; total: number };
  result?: BlameResult;
  failure?: OpFailure;
}

export interface ConflictFile {
  path: string;
  kind: string;
  binary: boolean;
  resolved: boolean;
  viewer: string;
}

export interface ConflictRevision {
  id: string;
  label: string;
}

export interface ConflictsResponse {
  op: string;
  base: ConflictRevision | null;
  ours: ConflictRevision | null;
  theirs: ConflictRevision | null;
  files: ConflictFile[];
}

export interface Branch {
  id: string;
  name: string;
  commit_id?: string | null;
  is_default: boolean;
  is_protected: boolean;
  updated_at?: string | null;
}

export interface Tag {
  id: string;
  name: string;
  commit_id: string;
  message: string;
  created_at?: string | null;
}

export interface MaintenanceWindow {
  mode: string;
  active: boolean;
  message: string;
  starts_at?: string | null;
  ends_at?: string | null;
  org: string;
  project: string;
}

export interface RepoActivity {
  incoming: number;
  review_requests: number;
  error: string;
  maintenance: { server: MaintenanceWindow | null; org: MaintenanceWindow | null; project: MaintenanceWindow | null } | null;
}
