import { afterEach, describe, expect, it, vi } from 'vitest'

const apiJson = vi.hoisted(() => vi.fn(async () => []))
vi.mock('../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/client')>()),
  apiJson,
}))

const { clearOwnerQuota, getCommitDiff, getTag, listBranches, listCommits, listTags, requestLockRelease } = await import('../api/endpoints')

afterEach(() => apiJson.mockClear())

const lastPath = () => (apiJson.mock.calls[apiJson.mock.calls.length - 1] as unknown as [string])[0]

describe('paged endpoint helpers', () => {
  it('lists branches with and without a cursor', async () => {
    await listBranches('acme', 'game')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/branches')
    await listBranches('acme', 'game', { limit: 51, after: { id: 'b9', updated_at: '2026-01-01T00:00:00Z' } })
    expect(lastPath()).toContain('limit=51')
    expect(lastPath()).toContain('last_id=b9')
    expect(lastPath()).toContain('last_updated_at=2026-01-01T00%3A00%3A00Z')
  })

  it('lists tags with and without a cursor and fetches one tag', async () => {
    await listTags('acme', 'game')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/tags')
    await listTags('acme', 'game', { limit: 10, after: { id: 't3', created_at: '2026-02-02T00:00:00Z' } })
    expect(lastPath()).toContain('limit=10')
    expect(lastPath()).toContain('last_id=t3')
    expect(lastPath()).toContain('last_created_at=')
    await getTag('acme', 'game', 'v1/rc 1')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/tags/v1%2Frc%201')
  })

  it('passes commit paging and diff base parameters', async () => {
    await listCommits('acme', 'game', 'main', 'src', { start: 'c9', limit: 51 })
    expect(lastPath()).toContain('branch=main')
    expect(lastPath()).toContain('path=src')
    expect(lastPath()).toContain('start=c9')
    expect(lastPath()).toContain('limit=51')
    await listCommits('acme', 'game', '')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/commits')
    await getCommitDiff('acme', 'game', 'c2')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/commits/c2/diff')
    await getCommitDiff('acme', 'game', 'c2', 'c1')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/commits/c2/diff?base=c1')
  })
})

describe('billing endpoint helpers', () => {
  it('clears an owner quota override with DELETE', async () => {
    await clearOwnerQuota('7 3')
    expect(apiJson).toHaveBeenLastCalledWith('/api/v1/admin/billing/owners/7%203/quota', { method: 'DELETE' })
  })
})

describe('lock release requests', () => {
  it('posts the message to the lock id', async () => {
    await requestLockRelease('acme', 'game', 'l/1', 'soon?')
    expect(lastPath()).toBe('/api/v1/orgs/acme/projects/game/locks/l%2F1/requests')
    expect(apiJson.mock.calls[apiJson.mock.calls.length - 1]).toEqual([lastPath(), { method: 'POST', body: '{"message":"soon?"}' }])
  })
})
