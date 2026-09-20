import type { LoginRequest, TokenResponse } from './models'

let accessToken: string | null = null
let refreshInFlight: Promise<TokenResponse> | null = null

interface ApiRequestInit extends RequestInit {
  retry?: boolean
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
  return tokens
}

export async function logout(): Promise<void> {
  try {
    await apiFetch('/api/v1/auth/logout', { method: 'POST' })
  } finally {
    clearSession()
  }
}

async function refresh(): Promise<TokenResponse> {
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

export async function bootstrap(): Promise<boolean> {
  try {
    await refresh()
    return true
  } catch {
    clearSession()
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
      clearSession()
    }
  }
  return res
}

async function requestJson<T>(path: string, init: ApiRequestInit): Promise<T> {
  const res = await apiFetch(path, init)
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`API ${res.status}: ${body}`)
  }
  return (await res.json()) as T
}
