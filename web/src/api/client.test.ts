import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  apiFetch,
  apiJson,
  bootstrap,
  clearSession,
  errorFromResponse,
  isAuthenticated,
  isMaintenanceError,
  login,
  logout,
  setSession,
  subscribeSession,
} from './client'

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

const maintenance = { scope: 'server', mode: 'frozen', message: 'moving the database' }

function maintenanceResponse(header = 'server;frozen'): Response {
  const body = { error: 'server is in maintenance (frozen): moving the database', maintenance }
  return {
    ok: false,
    status: 503,
    headers: new Headers(header ? { 'X-Nipa-Maintenance': header } : {}),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

const tokenBody = { access_token: 'tok', token_type: 'Bearer', expires_in: 1800 }

beforeEach(() => {
  clearSession()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('auth client', () => {
  it('retries refresh once to survive a rotation race', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: 'record not found' }, 404))
      .mockResolvedValueOnce(jsonResponse(tokenBody))
    vi.stubGlobal('fetch', fetchMock)

    expect(await bootstrap()).toBe(true)
    expect(isAuthenticated()).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('gives up and notifies listeners when refresh keeps failing', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: 'record not found' }, 404))
    vi.stubGlobal('fetch', fetchMock)

    const events: string[] = []
    const unsubscribe = subscribeSession((event) => events.push(event))

    expect(await bootstrap()).toBe(false)
    unsubscribe()

    expect(isAuthenticated()).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(events).toEqual(['logout'])
  })

  it('login stores the access token', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(tokenBody))
    vi.stubGlobal('fetch', fetchMock)

    await login({ email: 'alice@example.com', password: 'secret' })

    expect(isAuthenticated()).toBe(true)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('logout clears the in-memory token', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(tokenBody)))
    await login({ email: 'alice@example.com', password: 'secret' })
    expect(isAuthenticated()).toBe(true)

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ message: 'logged out' })))
    await logout()
    expect(isAuthenticated()).toBe(false)
  })

  it('keeps the session when refresh is refused for maintenance', async () => {
    const fetchMock = vi.fn().mockResolvedValue(maintenanceResponse())
    vi.stubGlobal('fetch', fetchMock)
    const events: string[] = []
    const unsubscribe = subscribeSession((event) => events.push(event))

    expect(await bootstrap()).toBe(false)
    unsubscribe()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(events).toEqual([])
  })

  it('surfaces a typed maintenance error and keeps the access token on a 401', async () => {
    setSession({ access_token: 'expired', token_type: 'Bearer', expires_in: 1800 })
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: 'token expired' }, 401))
      .mockResolvedValueOnce(maintenanceResponse())
    vi.stubGlobal('fetch', fetchMock)
    const events: string[] = []
    const unsubscribe = subscribeSession((event) => events.push(event))

    const err = await apiFetch('/api/v1/me').catch((e: unknown) => e)
    unsubscribe()

    expect(isMaintenanceError(err)).toBe(true)
    expect((err as ApiError).status).toBe(503)
    expect((err as ApiError).maintenance).toEqual(maintenance)
    expect(isAuthenticated()).toBe(true)
    expect(events).toEqual([])
  })

  it('types maintenance refusals of ordinary requests', async () => {
    setSession(tokenBody)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(maintenanceResponse()))
    const err = await apiJson('/api/v1/orgs/acme/projects/game/locks', { method: 'POST' }).catch((e: unknown) => e)
    expect(isMaintenanceError(err)).toBe(true)
    expect((err as ApiError).message).toBe('server is in maintenance (frozen): moving the database')
  })

  it('recognises the maintenance body without the header', async () => {
    const err = await errorFromResponse(maintenanceResponse(''))
    expect(err.maintenance).toEqual(maintenance)
    const other = await errorFromResponse(jsonResponse({ error: 'boom', maintenance }, 500))
    expect(other.maintenance).toBeUndefined()
    expect(isMaintenanceError(new Error('x'))).toBe(false)
  })

  it('still drops the session when the refresh is rejected after a 401', async () => {
    setSession(tokenBody)
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: 'token expired' }, 401))
      .mockResolvedValue(jsonResponse({ error: 'refresh token revoked' }, 401))
    vi.stubGlobal('fetch', fetchMock)
    const events: string[] = []
    const unsubscribe = subscribeSession((event) => events.push(event))

    const res = await apiFetch('/api/v1/me')
    unsubscribe()

    expect(res.status).toBe(401)
    expect(isAuthenticated()).toBe(false)
    expect(events).toEqual(['logout'])
  })

  it('retries the request once the refresh succeeds', async () => {
    setSession(tokenBody)
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ error: 'token expired' }, 401))
      .mockResolvedValueOnce(jsonResponse(tokenBody))
      .mockResolvedValueOnce(jsonResponse({ id: '1' }))
    vi.stubGlobal('fetch', fetchMock)

    const res = await apiFetch('/api/v1/me')
    expect(res.status).toBe(200)
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })
})
