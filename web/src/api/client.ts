import type { LoginRequest, TokenResponse } from './models'

export type SessionEvent = 'login' | 'logout'

const REFRESH_RETRY_DELAY_MS = 150
const REFRESH_ATTEMPTS = 3

let accessToken: string | null = null
let refreshInFlight: Promise<TokenResponse> | null = null
const sessionListeners = new Set<(event: SessionEvent) => void>()

const channel: BroadcastChannel | null =
  typeof window !== 'undefined' && typeof window.BroadcastChannel !== 'undefined'
    ? new window.BroadcastChannel('nipa.auth')
    : null

channel?.addEventListener('message', (message) => {
  const event = message.data as SessionEvent
  if (event === 'logout') {
    clearSession()
  }
  notify(event)
})

interface ApiRequestInit extends RequestInit {
  retry?: boolean
}

function notify(event: SessionEvent): void {
  sessionListeners.forEach((listener) => listener(event))
}

function broadcast(event: SessionEvent): void {
  channel?.postMessage(event)
}

function handleSessionLost(): void {
  // Only clear this tab: another tab may still hold a valid session (for
  // example when it won a concurrent refresh rotation).
  clearSession()
  notify('logout')
}

export function subscribeSession(listener: (event: SessionEvent) => void): () => void {
  sessionListeners.add(listener)
  return () => {
    sessionListeners.delete(listener)
  }
}

export function getAccessToken(): string | null {
  return accessToken
}

export function setSession(tokens: TokenResponse): void {
  accessToken = tokens.access_token
}

export function clearSession(): void {
  accessToken = null
}

export function isAuthenticated(): boolean {
  return accessToken !== null
}

export async function login(req: LoginRequest): Promise<TokenResponse> {
  const tokens = await requestJson<TokenResponse>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify(req),
  })
  setSession(tokens)
  broadcast('login')
  return tokens
}

export async function logout(): Promise<void> {
  try {
    await apiFetch('/api/v1/auth/logout', { method: 'POST' })
  } finally {
    clearSession()
    broadcast('logout')
  }
}

function refreshOnce(): Promise<TokenResponse> {
  if (!refreshInFlight) {
    refreshInFlight = requestJson<TokenResponse>('/api/v1/auth/refresh', {
      method: 'POST',
      credentials: 'same-origin',
    })
      .then((tokens) => {
        setSession(tokens)
        return tokens
      })
      .finally(() => {
        refreshInFlight = null
      })
  }
  return refreshInFlight
}

async function refresh(): Promise<TokenResponse> {
  let lastError: unknown
  for (let attempt = 0; attempt < REFRESH_ATTEMPTS; attempt++) {
    if (attempt > 0) {
      // Another tab may have rotated the cookie between our request and its
      // response; wait briefly so the retry sends the newer cookie value.
      await new Promise((resolve) => setTimeout(resolve, REFRESH_RETRY_DELAY_MS))
    }
    try {
      return await refreshOnce()
    } catch (err) {
      lastError = err
    }
  }
  throw lastError
}

export async function bootstrap(): Promise<boolean> {
  try {
    await refresh()
    return true
  } catch {
    handleSessionLost()
    return false
  }
}

export async function apiFetch(path: string, init: ApiRequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers)
  if (accessToken) {
    headers.set('Authorization', `Bearer ${accessToken}`)
  }
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })

  if (res.status === 401 && accessToken && init.retry !== true) {
    try {
      await refresh()
      return await apiFetch(path, { ...init, retry: true })
    } catch {
      handleSessionLost()
    }
  }
  return res
}

export async function errorFromResponse(res: Response): Promise<Error> {
  const body = await res.text()
  try {
    const parsed = JSON.parse(body) as { error?: string; message?: string }
    const message = parsed.error ?? parsed.message
    if (message) {
      return new Error(message)
    }
  } catch {
    // fall through to the raw body
  }
  return new Error(`API ${res.status}: ${body}`)
}

export async function apiJson<T>(path: string, init: ApiRequestInit = {}): Promise<T> {
  const res = await apiFetch(path, init)
  if (!res.ok) {
    throw await errorFromResponse(res)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

async function requestJson<T>(path: string, init: ApiRequestInit): Promise<T> {
  const res = await apiFetch(path, init)
  if (!res.ok) {
    throw await errorFromResponse(res)
  }
  return (await res.json()) as T
}
