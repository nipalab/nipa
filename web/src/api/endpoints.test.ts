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

describe('lockFile', () => {
  it('sends ttl_seconds only when given', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const { lockFile } = await import('./endpoints')

    await lockFile('acme', 'game', 'a.png', '')
    await lockFile('acme', 'game', 'a.png', 'feature', 600)

    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ path: 'a.png', branch: '' })
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ path: 'a.png', branch: 'feature', ttl_seconds: 600 })
  })
})

describe('listProjectMembers', () => {
  it('sends the query and limit only when set', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [] } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const { listProjectMembers } = await import('./endpoints')

    await listProjectMembers('acme', 'game')
    await listProjectMembers('acme', 'game', 'ad a', 20)

    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/orgs/acme/projects/game/members',
      '/api/v1/orgs/acme/projects/game/members?q=ad+a&limit=20',
    ])
  })
})

describe('createProject', () => {
  it('sends storage_server_id only when given', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const { createProject } = await import('./endpoints')

    await createProject('acme', 'Game', '', 'game')
    await createProject('acme', 'Own', 'x', 'own', 's1')

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/orgs/acme/projects')
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ name: 'Game', description: '', slug: 'game' })
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({
      name: 'Own',
      description: 'x',
      slug: 'own',
      storage_server_id: 's1',
    })
  })
})

describe('storage servers', () => {
  it('calls the storage server routes', async () => {
    const fetchMock = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === 'DELETE') return { ok: true, status: 204 } as Response
      const body = url === '/api/v1/me/storage-servers' && !init?.method ? { servers: [{ id: 's1' }] } : { id: 's1' }
      return { ok: true, status: 200, json: async () => body } as Response
    })
    vi.stubGlobal('fetch', fetchMock)
    const endpoints = await import('./endpoints')

    expect(await endpoints.listStorageServers()).toEqual([{ id: 's1' }])
    await endpoints.getStorageServer('s 1')
    await endpoints.createStorageServer({
      name: 'R2',
      endpoint: 'https://acct.r2.cloudflarestorage.com',
      bucket: 'assets',
      access_key_id: 'a',
      secret_access_key: 's',
    })
    await endpoints.updateStorageServer('s1', { name: 'Renamed' })
    expect(await endpoints.deleteStorageServer('s1')).toBeUndefined()
    await endpoints.testStorageServer('s1')
    await endpoints.getOrgStorageServer('acme')
    await endpoints.setOrgStorageServer('acme', 's1')
    await endpoints.clearOrgStorageServer('acme')
    await endpoints.getProjectStorage('acme', 'game')

    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method ?? 'GET'])).toEqual([
      ['/api/v1/me/storage-servers', 'GET'],
      ['/api/v1/me/storage-servers/s%201', 'GET'],
      ['/api/v1/me/storage-servers', 'POST'],
      ['/api/v1/me/storage-servers/s1', 'PATCH'],
      ['/api/v1/me/storage-servers/s1', 'DELETE'],
      ['/api/v1/me/storage-servers/s1/test', 'POST'],
      ['/api/v1/orgs/acme/storage-server', 'GET'],
      ['/api/v1/orgs/acme/storage-server', 'PUT'],
      ['/api/v1/orgs/acme/storage-server', 'DELETE'],
      ['/api/v1/orgs/acme/projects/game/storage', 'GET'],
    ])
    expect(JSON.parse(fetchMock.mock.calls[7][1].body)).toEqual({ storage_server_id: 's1' })
  })
})

describe('issue tracker', () => {
  it('calls the org and project routes', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const {
      deleteIssueTracker,
      deleteProjectIssueTracker,
      getIssueTracker,
      getProjectIssueTracker,
      putIssueTracker,
      putProjectIssueTracker,
      testIssueTracker,
    } = await import('./endpoints')

    await getIssueTracker('acme')
    await putIssueTracker('acme', { base_url: 'https://acme.atlassian.net', account: 'bot@acme.com', token: 't' })
    await testIssueTracker('acme')
    await deleteIssueTracker('acme')
    await getProjectIssueTracker('acme', 'game')
    await putProjectIssueTracker('acme', 'game', { keys: ['GAME'], link_commits: 'all' })
    await deleteProjectIssueTracker('acme', 'game')

    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method ?? 'GET'])).toEqual([
      ['/api/v1/orgs/acme/issue-tracker', 'GET'],
      ['/api/v1/orgs/acme/issue-tracker', 'PUT'],
      ['/api/v1/orgs/acme/issue-tracker/test', 'POST'],
      ['/api/v1/orgs/acme/issue-tracker', 'DELETE'],
      ['/api/v1/orgs/acme/projects/game/issue-tracker', 'GET'],
      ['/api/v1/orgs/acme/projects/game/issue-tracker', 'PUT'],
      ['/api/v1/orgs/acme/projects/game/issue-tracker', 'DELETE'],
    ])
    expect(JSON.parse(fetchMock.mock.calls[5][1].body)).toEqual({ keys: ['GAME'], link_commits: 'all' })
  })
})

describe('issues', () => {
  it('calls the resolve, search and links routes', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) } as Response)
    vi.stubGlobal('fetch', fetchMock)
    const { listIssueLinks, resolveIssues, searchIssues } = await import('./endpoints')

    await resolveIssues('acme', 'game', ['GAME-1', 'GAME-2'])
    await searchIssues('acme', 'game', 'rig')
    await searchIssues('acme', 'game', 'rig', 5)
    await listIssueLinks('acme', 'game', 'GAME-1')
    await listIssueLinks('acme', 'game', 'GAME-1', 'abc')

    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/orgs/acme/projects/game/issues?keys=GAME-1%2CGAME-2',
      '/api/v1/orgs/acme/projects/game/issues/search?q=rig',
      '/api/v1/orgs/acme/projects/game/issues/search?q=rig&limit=5',
      '/api/v1/orgs/acme/projects/game/issues/GAME-1/links',
      '/api/v1/orgs/acme/projects/game/issues/GAME-1/links?after=abc',
    ])
  })
})
