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
