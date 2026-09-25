export interface LoginRequest {
  email: string
  password: string
}

export interface TokenResponse {
  access_token: string
  token_type: string
  expires_in: number
}

export interface UserResponse {
  id: string
  name: string
  email: string
  photo_url: string
  is_admin: boolean
  is_super_admin: boolean
  deleted: boolean
}

export interface OrgResponse {
  id: string
  slug: string
  name: string
  role: string
}

export interface OrgMemberResponse {
  user_id: string
  name: string
  email: string
  photo_url: string
  is_admin: boolean
  is_super_admin: boolean
  role: string
  joined_at?: string
}

export interface ProjectResponse {
  id: string
  org_id: string
  slug: string
  name: string
  description: string
}

export interface TreeEntryResponse {
  name: string
  path: string
  type: 'tree' | 'file'
  mode?: number
  size_bytes?: number
  is_binary?: boolean
  hash?: string
  last_commit?: CommitResponse
}

export interface TreeResponse {
  path: string
  entries: TreeEntryResponse[]
  latest_commit?: CommitResponse
}

export interface BlobResponse {
  blob: Blob
  contentType: string
  size: number
  isBinary: boolean
}

export interface BranchResponse {
  id: string
  name: string
  is_default: boolean
  is_protected: boolean
  commit_id?: string
  updated_at: string
}

export interface CommitResponse {
  id: string
  parent_1_id?: string
  parent_2_id?: string
  message: string
  author_name?: string
  author_email?: string
  created_at: string
}

export interface DiffFileResponse {
  path: string
  old_path?: string
  status: string
  binary: boolean
  additions: number
  deletions: number
  patch?: string[]
}

export interface CommitDiffResponse {
  commit_id: string
  base_id?: string
  files: DiffFileResponse[]
}

export interface MergeabilityResponse {
  status: string
  source_commit_id?: string
  target_commit_id?: string
  merge_base_commit_id?: string
}

export interface MergeRequestResponse {
  id: string
  number: number
  project_id: string
  source_branch: string
  target_branch: string
  title: string
  description: string
  status: string
  merge_commit_id?: string
  merge_base_commit_id?: string
  created_by: string
  created_at: string
  updated_at: string
  mergeability?: MergeabilityResponse
}

export interface MergeRequestDiffResponse {
  base_id?: string
  files: DiffFileResponse[]
}

export interface GroupResponse {
  id: string
  org_id: string
  name: string
  description: string
  member_ids?: string[]
}

export interface PermissionEntry {
  path_prefix: string
  permission: number
}

export interface ProjectPermissionResponse {
  project_permission: number
  rules: PermissionEntry[]
  defaults: PermissionEntry[]
}

export interface PBACRuleResponse {
  id: number
  user_id?: string
  group_id?: string
  path_prefix: string
  permission: number
}

export interface MessageResponse {
  message: string
}

export const PERMISSION_READ = 1
export const PERMISSION_WRITE = 2
export const PERMISSION_LOCK = 4
export const PERMISSION_ADMIN = 1 << 16

export function formatPermission(mask: number): string {
  const parts: string[] = []
  if (mask & PERMISSION_READ) parts.push('read')
  if (mask & PERMISSION_WRITE) parts.push('write')
  if (mask & PERMISSION_LOCK) parts.push('lock')
  if (mask & PERMISSION_ADMIN) parts.push('admin')
  return parts.length > 0 ? parts.join(',') : 'none'
}
