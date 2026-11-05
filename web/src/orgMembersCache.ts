import type { OrgMemberResponse } from './api/models'

export const MEMBERS_TTL_MS = 30_000
export const memberCache = new Map<string, { at: number; request: Promise<OrgMemberResponse[]> }>()

export function clearOrgMembersCache() {
  memberCache.clear()
}
