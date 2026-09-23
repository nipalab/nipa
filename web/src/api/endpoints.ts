import { apiFetch, apiJson } from './client'
import type {
  BranchResponse,
  CommitDiffResponse,
  CommitResponse,
  GroupResponse,
  MergeRequestDiffResponse,
  MergeRequestResponse,
  MessageResponse,
  OrgMemberResponse,
  OrgResponse,
  PBACRuleResponse,
  PermissionEntry,
  ProjectPermissionResponse,
  ProjectResponse,
  TreeResponse,
  UserResponse,
} from './models'

function projectBase(org: string, project: string): string {
  return `/api/v1/orgs/${encodeURIComponent(org)}/projects/${encodeURIComponent(project)}`
}

export function getMe(): Promise<UserResponse> {
  return apiJson('/api/v1/me')
}

export function updateMyProfile(name: string, photoUrl: string): Promise<UserResponse> {
  return apiJson('/api/v1/me', { method: 'PATCH', body: JSON.stringify({ name, photo_url: photoUrl }) })
}

export function changeMyPassword(oldPassword: string, newPassword: string): Promise<MessageResponse> {
  return apiJson('/api/v1/me/password', {
    method: 'POST',
    body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
  })
}

export function listOrgs(): Promise<OrgResponse[]> {
  return apiJson('/api/v1/orgs')
}

export function listProjects(org: string): Promise<ProjectResponse[]> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/projects`)
}

export function createProject(org: string, name: string, description: string, slug: string): Promise<ProjectResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/projects`, {
    method: 'POST',
    body: JSON.stringify({ name, description, slug }),
  })
}

export function updateProject(org: string, project: string, name: string, description: string): Promise<ProjectResponse> {
  return apiJson(projectBase(org, project), {
    method: 'PATCH',
    body: JSON.stringify({ name, description }),
  })
}

export function deleteProject(org: string, project: string): Promise<MessageResponse> {
  return apiJson(projectBase(org, project), { method: 'DELETE' })
}

export function getTree(org: string, project: string, rev: string, path: string): Promise<TreeResponse> {
  const params = new URLSearchParams()
  if (rev) params.set('rev', rev)
  if (path) params.set('path', path)
  const query = params.toString()
  return apiJson(`${projectBase(org, project)}/tree${query ? `?${query}` : ''}`)
}

export async function getBlob(org: string, project: string, rev: string, path: string): Promise<string> {
  const params = new URLSearchParams({ path })
  if (rev) params.set('rev', rev)
  const res = await apiFetch(`${projectBase(org, project)}/blob?${params.toString()}`)
  if (!res.ok) {
    throw new Error(`API ${res.status}: ${await res.text()}`)
  }
  return res.text()
}

export function listBranches(org: string, project: string): Promise<BranchResponse[]> {
  return apiJson(`${projectBase(org, project)}/branches`)
}

export function createBranch(org: string, project: string, name: string, from: string): Promise<BranchResponse> {
  return apiJson(`${projectBase(org, project)}/branches`, {
    method: 'POST',
    body: JSON.stringify({ name, from }),
  })
}

export function renameBranch(org: string, project: string, name: string, newName: string): Promise<BranchResponse> {
  return apiJson(`${projectBase(org, project)}/branches/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify({ name: newName }),
  })
}

export function deleteBranch(org: string, project: string, name: string): Promise<MessageResponse> {
  return apiJson(`${projectBase(org, project)}/branches/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export function setDefaultBranch(org: string, project: string, name: string): Promise<BranchResponse> {
  return apiJson(`${projectBase(org, project)}/branches/${encodeURIComponent(name)}/default`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function setBranchProtection(
  org: string,
  project: string,
  name: string,
  protectedBranch: boolean,
): Promise<BranchResponse> {
  return apiJson(`${projectBase(org, project)}/branches/${encodeURIComponent(name)}/protection`, {
    method: 'PUT',
    body: JSON.stringify({ protected: protectedBranch }),
  })
}

export function listCommits(org: string, project: string, branch: string): Promise<CommitResponse[]> {
  const params = new URLSearchParams()
  if (branch) params.set('branch', branch)
  const query = params.toString()
  return apiJson(`${projectBase(org, project)}/commits${query ? `?${query}` : ''}`)
}

export function getCommitDiff(org: string, project: string, commit: string): Promise<CommitDiffResponse> {
  return apiJson(`${projectBase(org, project)}/commits/${encodeURIComponent(commit)}/diff`)
}

export function listMergeRequests(org: string, project: string, status: string): Promise<MergeRequestResponse[]> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  const query = params.toString()
  return apiJson(`${projectBase(org, project)}/merge-requests${query ? `?${query}` : ''}`)
}

export function createMergeRequest(
  org: string,
  project: string,
  title: string,
  description: string,
  sourceBranch: string,
  targetBranch: string,
): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests`, {
    method: 'POST',
    body: JSON.stringify({ title, description, source_branch: sourceBranch, target_branch: targetBranch }),
  })
}

export function getMergeRequest(org: string, project: string, id: string): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}`)
}

export function updateMergeRequest(
  org: string,
  project: string,
  id: string,
  title: string,
  description: string,
): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify({ title, description }),
  })
}

export function mergeMergeRequest(org: string, project: string, id: string): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}/merge`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function closeMergeRequest(org: string, project: string, id: string): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}/close`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function reopenMergeRequest(org: string, project: string, id: string): Promise<MergeRequestResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}/reopen`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
}

export function getMergeRequestDiff(org: string, project: string, id: string): Promise<MergeRequestDiffResponse> {
  return apiJson(`${projectBase(org, project)}/merge-requests/${encodeURIComponent(id)}/diff`)
}

export function getMyProjectPermissions(org: string, project: string): Promise<ProjectPermissionResponse> {
  return apiJson(`${projectBase(org, project)}/permissions/me`)
}

export function listProjectRules(org: string, project: string): Promise<PBACRuleResponse[]> {
  return apiJson(`${projectBase(org, project)}/permissions/rules`)
}

export function createProjectRule(
  org: string,
  project: string,
  userID: string,
  groupID: string,
  pathPrefix: string,
  permission: number,
): Promise<PBACRuleResponse> {
  return apiJson(`${projectBase(org, project)}/permissions/rules`, {
    method: 'POST',
    body: JSON.stringify({ user_id: userID, group_id: groupID, path_prefix: pathPrefix, permission }),
  })
}

export function deleteProjectRule(org: string, project: string, id: number): Promise<MessageResponse> {
  return apiJson(`${projectBase(org, project)}/permissions/rules/${id}`, { method: 'DELETE' })
}

export function listProjectDefaults(org: string, project: string): Promise<PermissionEntry[]> {
  return apiJson(`${projectBase(org, project)}/permissions/defaults`)
}

export function setProjectDefault(
  org: string,
  project: string,
  pathPrefix: string,
  permission: number,
): Promise<PermissionEntry> {
  return apiJson(`${projectBase(org, project)}/permissions/defaults`, {
    method: 'PUT',
    body: JSON.stringify({ path_prefix: pathPrefix, permission }),
  })
}

export function deleteProjectDefault(org: string, project: string, pathPrefix: string): Promise<MessageResponse> {
  const params = new URLSearchParams({ path: pathPrefix })
  return apiJson(`${projectBase(org, project)}/permissions/defaults?${params.toString()}`, { method: 'DELETE' })
}

export function listOrgMembers(org: string): Promise<OrgMemberResponse[]> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/members`)
}

export function addOrgMember(
  org: string,
  identifier: string,
  role: string,
): Promise<OrgMemberResponse> {
  const body = identifier.includes('@') ? { email: identifier, role } : { user_id: identifier, role }
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/members`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function updateOrgMember(org: string, userID: string, role: string): Promise<OrgMemberResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/members/${encodeURIComponent(userID)}`, {
    method: 'PATCH',
    body: JSON.stringify({ role }),
  })
}

export function removeOrgMember(org: string, userID: string): Promise<MessageResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/members/${encodeURIComponent(userID)}`, {
    method: 'DELETE',
  })
}

export function listGroups(org: string): Promise<GroupResponse[]> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/groups`)
}

export function createGroup(org: string, name: string, description: string): Promise<GroupResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/groups`, {
    method: 'POST',
    body: JSON.stringify({ name, description }),
  })
}

export function getGroup(org: string, id: string): Promise<GroupResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/groups/${encodeURIComponent(id)}`)
}

export function addGroupMember(org: string, groupID: string, userID: string): Promise<MessageResponse> {
  return apiJson(`/api/v1/orgs/${encodeURIComponent(org)}/groups/${encodeURIComponent(groupID)}/members`, {
    method: 'POST',
    body: JSON.stringify({ user_id: userID }),
  })
}

export function removeGroupMember(org: string, groupID: string, userID: string): Promise<MessageResponse> {
  return apiJson(
    `/api/v1/orgs/${encodeURIComponent(org)}/groups/${encodeURIComponent(groupID)}/members/${encodeURIComponent(userID)}`,
    { method: 'DELETE' },
  )
}

export function listUsers(): Promise<UserResponse[]> {
  return apiJson('/api/v1/users')
}

export function createUser(name: string, email: string, password: string): Promise<UserResponse> {
  return apiJson('/api/v1/users', { method: 'POST', body: JSON.stringify({ name, email, password }) })
}

export function updateUserEmail(userID: string, email: string): Promise<UserResponse> {
  return apiJson(`/api/v1/users/${encodeURIComponent(userID)}`, {
    method: 'PATCH',
    body: JSON.stringify({ email }),
  })
}

export function deactivateUser(userID: string): Promise<MessageResponse> {
  return apiJson(`/api/v1/users/${encodeURIComponent(userID)}`, { method: 'DELETE' })
}

export function resetUserPassword(userID: string, password: string): Promise<MessageResponse> {
  return apiJson(`/api/v1/users/${encodeURIComponent(userID)}/password`, {
    method: 'POST',
    body: JSON.stringify({ password }),
  })
}

export function updateUserFlags(
  userID: string,
  isAdmin: boolean,
  isSuperAdmin: boolean,
): Promise<UserResponse> {
  return apiJson(`/api/v1/users/${encodeURIComponent(userID)}/admin`, {
    method: 'PATCH',
    body: JSON.stringify({ is_admin: isAdmin, is_super_admin: isSuperAdmin }),
  })
}
