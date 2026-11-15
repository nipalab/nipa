import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearSession } from './api/client'
import {
  asWindow,
  closesApp,
  effectiveWindow,
  formatLocal,
  getMaintenanceState,
  modeDescription,
  modeLabel,
  noteMaintenance,
  noteSessionPaused,
  recheckMaintenance,
  resetMaintenance,
  scopeName,
  storeOrg,
  storeProject,
  storeServer,
  useMaintenancePolling,
  windowsFor,
  type ScopedWindow,
} from './maintenance'
import { flush, renderHook, waitFor } from './test/render'

function jsonResponse(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, text: async () => JSON.stringify(body) } as Response
}

const ACTIVE = { mode: 'read_only', active: true, message: 'backup' }

beforeEach(() => resetMaintenance())

afterEach(() => {
  clearSession()
  resetMaintenance()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('maintenance store', () => {
  it('accepts only well-formed windows', () => {
    expect(asWindow(null)).toBeNull()
    expect(asWindow('x')).toBeNull()
    expect(asWindow({ mode: 'later' })).toBeNull()
    expect(asWindow({ mode: 'full' })).toEqual({ mode: 'full', active: false, message: '' })
  })

  it('notes refusals per scope', () => {
    noteMaintenance(undefined)
    expect(getMaintenanceState().server).toBeNull()
    noteMaintenance({ scope: 'server', mode: 'full', message: 'm' })
    noteMaintenance({ scope: 'org', mode: 'read_only', message: 'o', org: 'acme' })
    noteMaintenance({ scope: 'project', mode: 'full', message: 'p', org: 'acme', project: 'acme/game' })
    const state = getMaintenanceState()
    expect(state.server).toMatchObject({ mode: 'full', active: true })
    expect(state.orgs.acme).toMatchObject({ mode: 'read_only' })
    expect(state.projects['acme/game']).toMatchObject({ message: 'p' })
  })

  it('tracks a paused session', () => {
    noteSessionPaused(true)
    expect(getMaintenanceState().sessionPaused).toBe(true)
    noteSessionPaused(false)
    expect(getMaintenanceState().sessionPaused).toBe(false)
  })

  it('picks the strictest active window, broadest scope on ties', () => {
    storeServer({ mode: 'read_only', active: true, message: '' }, 'file')
    storeOrg('acme', { mode: 'full', active: false, message: '' })
    storeProject('acme/game', { mode: 'read_only', active: true, message: '' })
    const state = getMaintenanceState()
    expect(state.source).toBe('file')
    const windows = windowsFor(state, 'acme', 'game')
    expect(windows.map((item) => item.scope)).toEqual(['server', 'org', 'project'])
    expect(effectiveWindow(windows)?.scope).toBe('server')

    storeProject('acme/game', { mode: 'full', active: true, message: '' })
    expect(effectiveWindow(windowsFor(getMaintenanceState(), 'acme', 'game'))?.scope).toBe('project')
    expect(windowsFor(getMaintenanceState())).toHaveLength(1)
    expect(effectiveWindow([])).toBeNull()
  })

  it('closes the app for full and for frozen once the session is paused', () => {
    const item = (mode: 'read_only' | 'full' | 'frozen'): ScopedWindow => ({
      scope: 'server',
      target: '',
      window: { mode, active: true, message: '' },
    })
    expect(closesApp(null, true)).toBe(false)
    expect(closesApp(item('read_only'), true)).toBe(false)
    expect(closesApp(item('full'), false)).toBe(true)
    expect(closesApp(item('frozen'), false)).toBe(false)
    expect(closesApp(item('frozen'), true)).toBe(true)
  })

  it('describes modes, scopes and times', () => {
    expect(modeLabel('read_only')).toBe('read-only')
    expect(modeLabel('full')).toBe('closed')
    expect(modeLabel('frozen')).toBe('frozen')
    expect(modeDescription('read_only')).toContain('browse')
    expect(modeDescription('full')).toContain('cannot')
    expect(modeDescription('frozen')).toContain('Sign-in')
    expect(scopeName({ scope: 'server', target: '' })).toBe('The server')
    expect(scopeName({ scope: 'org', target: 'acme' })).toBe('Organization acme')
    expect(scopeName({ scope: 'project', target: 'acme/game' })).toBe('Project acme/game')
    expect(formatLocal(undefined)).toBe('')
    expect(formatLocal('nope')).toBe('')
    expect(formatLocal('2026-11-15T10:00:00Z')).not.toBe('')
  })
})

describe('maintenance polling', () => {
  it('polls the public status and the project status when signed in', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/maintenance') return jsonResponse({ server: ACTIVE, source: 'db' })
      if (url.endsWith('/projects/game/maintenance')) {
        return jsonResponse({ server: ACTIVE, org: null, project: { mode: 'full', active: false, message: 'p' } })
      }
      if (url.endsWith('/orgs/acme/maintenance')) return jsonResponse({ server: ACTIVE, org: ACTIVE })
      return jsonResponse({}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)

    const project = await renderHook(() => useMaintenancePolling({ org: 'acme', project: 'game', authenticated: true }))
    await waitFor(() => getMaintenanceState().projects['acme/game'] !== undefined)
    expect(getMaintenanceState().server).toMatchObject({ message: 'backup' })
    expect(getMaintenanceState().orgs.acme).toBeNull()
    await project.unmount()

    const org = await renderHook(() => useMaintenancePolling({ org: 'acme', authenticated: true }))
    await waitFor(() => getMaintenanceState().orgs.acme !== null)
    await org.unmount()

    fetchMock.mockClear()
    await recheckMaintenance()
    expect(fetchMock.mock.calls.map((call) => String(call[0]))).toEqual(['/api/maintenance', '/api/v1/orgs/acme/maintenance'])
  })

  it('keeps the last state when a request fails and skips scoped calls when anonymous', async () => {
    storeServer(ACTIVE)
    const fetchMock = vi.fn(async (_input?: RequestInfo | URL): Promise<Response> => {
      throw new Error('offline')
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = await renderHook(() => useMaintenancePolling({ org: 'acme', authenticated: true }))
    await flush()
    expect(getMaintenanceState().server).toMatchObject({ message: 'backup' })
    await view.unmount()

    fetchMock.mockImplementation(async (input?: RequestInfo | URL) => {
      if (String(input) === '/api/maintenance') return jsonResponse({ server: null, source: 'db' })
      throw new Error('offline')
    })
    const anonymous = await renderHook(() => useMaintenancePolling({ org: 'acme', authenticated: false }))
    await waitFor(() => getMaintenanceState().server === null)
    expect(fetchMock.mock.calls.filter((call) => String(call[0]).includes('/orgs/'))).toHaveLength(0)
    await anonymous.unmount()

    fetchMock.mockClear()
    const scoped = await renderHook(() => useMaintenancePolling({ org: 'acme', authenticated: true }))
    await waitFor(() => fetchMock.mock.calls.length >= 2)
    expect(getMaintenanceState().orgs.acme).toBeUndefined()
    await scoped.unmount()
  })

  it('polls again on the interval', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn(async () => jsonResponse({ server: null, source: 'db' }))
    vi.stubGlobal('fetch', fetchMock)
    const view = await renderHook(() => useMaintenancePolling({ authenticated: false }, 1000))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await view.unmount()
    await vi.advanceTimersByTimeAsync(5000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
