import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearSession } from './client'
import { listAuditEntries } from './endpoints'

afterEach(() => {
  clearSession()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('listAuditEntries', () => {
  it('sends only the filters that are set', async () => {
    const page = { entries: [], next_cursor: 'abc' }
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => page,
    } as Response)
    vi.stubGlobal('fetch', fetchMock)

    expect(await listAuditEntries({ action: 'org.member', actor: '', limit: 20, after: 'x1' })).toEqual(page)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/admin/audit?action=org.member&limit=20&after=x1')

    await listAuditEntries()
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/admin/audit')
  })
})

describe('service tokens and commit ancestry', () => {
  it('calls the project routes', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const { createServiceToken, getCommitContains, listServiceTokens, revokeServiceToken } = await import('./endpoints')

    await listServiceTokens('acme', 'game')
    await createServiceToken('acme', 'game', { name: 'bugbase', scopes: ['read'] })
    await revokeServiceToken('acme', 'game', 't 1')
    await getCommitContains('acme', 'game', 'c1', 'v0.2')

    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/orgs/acme/projects/game/service-tokens',
      '/api/v1/orgs/acme/projects/game/service-tokens',
      '/api/v1/orgs/acme/projects/game/service-tokens/t%201',
      '/api/v1/orgs/acme/projects/game/commits/c1/contains?ref=v0.2',
    ])
    expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'POST' })
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: 'DELETE' })
  })
})
