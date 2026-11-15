import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getMaintenanceState, resetMaintenance } from '../maintenance'
import { bootstrap, clearSession, errorFromResponse } from './client'
import {
  endOrgMaintenance,
  endProjectMaintenance,
  endServerMaintenance,
  getAdminMaintenance,
  getMaintenance,
  getOrgMaintenance,
  getProjectMaintenance,
  setOrgMaintenance,
  setProjectMaintenance,
  setServerMaintenance,
} from './endpoints'

function jsonResponse(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, text: async () => JSON.stringify(body) } as Response
}

function refusal(mode: string, scope = 'server'): Response {
  const body = { error: `refused ${mode}`, maintenance: { scope, mode, message: 'm', org: 'acme', project: 'acme/game' } }
  return {
    ok: false,
    status: 503,
    headers: new Headers({ 'X-Nipa-Maintenance': `${scope};${mode}` }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

beforeEach(() => {
  clearSession()
  resetMaintenance()
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetMaintenance()
})

describe('maintenance endpoints', () => {
  it('calls the public, admin, org and project routes', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ server: null, source: 'db' }))
    vi.stubGlobal('fetch', fetchMock)
    const input = { mode: 'read_only' as const, message: 'm', notify: true }

    await getMaintenance()
    await getAdminMaintenance()
    await setServerMaintenance(input)
    await endServerMaintenance()
    await getOrgMaintenance('acme')
    await setOrgMaintenance('acme', input)
    await endOrgMaintenance('acme')
    await getProjectMaintenance('acme', 'my game')
    await setProjectMaintenance('acme', 'my game', input)
    await endProjectMaintenance('acme', 'my game')

    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][]
    expect(calls.map((call) => [call[0], call[1]?.method ?? 'GET'])).toEqual([
      ['/api/maintenance', 'GET'],
      ['/api/v1/admin/maintenance', 'GET'],
      ['/api/v1/admin/maintenance', 'PUT'],
      ['/api/v1/admin/maintenance', 'DELETE'],
      ['/api/v1/orgs/acme/maintenance', 'GET'],
      ['/api/v1/orgs/acme/maintenance', 'PUT'],
      ['/api/v1/orgs/acme/maintenance', 'DELETE'],
      ['/api/v1/orgs/acme/projects/my%20game/maintenance', 'GET'],
      ['/api/v1/orgs/acme/projects/my%20game/maintenance', 'PUT'],
      ['/api/v1/orgs/acme/projects/my%20game/maintenance', 'DELETE'],
    ])
    expect(JSON.parse(String(calls[2][1].body))).toEqual(input)
  })

  it('throws when the public status fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'boom' }, 500)))
    await expect(getMaintenance()).rejects.toThrow('boom')
  })
})

describe('client maintenance notes', () => {
  it('records the window of a refusal', async () => {
    await errorFromResponse(refusal('full', 'project'))
    expect(getMaintenanceState().projects['acme/game']).toMatchObject({ mode: 'full', active: true })
  })

  it('marks the session paused when the refresh is refused and clears it after a sign-in', async () => {
    const fetchMock = vi.fn(async () => refusal('frozen'))
    vi.stubGlobal('fetch', fetchMock)
    expect(await bootstrap()).toBe(false)
    expect(getMaintenanceState().sessionPaused).toBe(true)
    expect(getMaintenanceState().server).toMatchObject({ mode: 'frozen' })

    fetchMock.mockImplementation(async () => jsonResponse({ access_token: 't', token_type: 'Bearer', expires_in: 1 }))
    expect(await bootstrap()).toBe(true)
    expect(getMaintenanceState().sessionPaused).toBe(false)
  })

  it('clears the pause when the refresh fails for another reason', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => refusal('frozen')))
    await bootstrap()
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'expired' }, 401)))
    expect(await bootstrap()).toBe(false)
    expect(getMaintenanceState().sessionPaused).toBe(false)
  })
})
