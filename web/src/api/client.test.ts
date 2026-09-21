import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  bootstrap,
  clearSession,
  isAuthenticated,
  login,
  logout,
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
})
