import { listOrgMembers, listOrgs } from './api/endpoints'
import type { OrgMemberResponse } from './api/models'
import { useAuth } from './auth'
import { useAsync } from './hooks'

export function useOrgRole(org: string) {
  const { me } = useAuth()
  const { data: orgs, loading, error } = useAsync(listOrgs, [])
  const role = orgs?.find((item) => item.slug === org)?.role
  const canManage = Boolean(me?.is_admin || me?.is_super_admin || role === 'owner')
  return { role, canManage, loading, error }
}

const MEMBERS_TTL_MS = 30_000
const memberCache = new Map<string, { at: number; request: Promise<OrgMemberResponse[]> }>()

export function fetchOrgMembers(org: string, force = false): Promise<OrgMemberResponse[]> {
  const cached = memberCache.get(org)
  if (!force && cached && Date.now() - cached.at < MEMBERS_TTL_MS) return cached.request
  const request = listOrgMembers(org)
  memberCache.set(org, { at: Date.now(), request })
  request.catch(() => {
    if (memberCache.get(org)?.request === request) memberCache.delete(org)
  })
  return request
}

export function clearOrgMembersCache() {
  memberCache.clear()
}

export function useOrgMembers(org: string) {
  const state = useAsync(() => fetchOrgMembers(org), [org])
  const reload = () => {
    memberCache.delete(org)
    state.reload()
  }
  return { ...state, reload }
}
