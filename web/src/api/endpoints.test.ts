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
