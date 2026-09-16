import type { LoginRequest, LoginResponse } from './models'

const ACCESS_KEY = 'nipa.accessToken'
const REFRESH_KEY = 'nipa.refreshToken'

interface ApiRequestInit extends RequestInit {
  retry?: boolean
}

export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_KEY)
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY)
}

export function setSession(tokens: LoginResponse): void {
  localStorage.setItem(ACCESS_KEY, tokens.access_token)
  localStorage.setItem(REFRESH_KEY, tokens.refresh_token)
}

export function clearSession(): void {
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
}

export function isAuthenticated(): boolean {
  return getAccessToken() !== null
}

export async function login(req: LoginRequest): Promise<LoginResponse> {
  return requestJson<LoginResponse>('/auth/', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

async function refresh(): Promise<LoginResponse> {
  const token = getRefreshToken()
  if (!token) {
    throw new Error('no refresh token')
  }
  return requestJson<LoginResponse>('/auth/refresh', {
    method: 'POST',
    body: JSON.stringify({ refresh_token: token }),
  })
}

export async function apiFetch(path: string, init: ApiRequestInit = {}): Promise<Response> {
  const token = getAccessToken()
  const headers = new Headers(init.headers)
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  let res = await fetch(path, { ...init, headers })

  if (res.status === 401 && token && init.retry !== true) {
    try {
      const next = await refresh()
      setSession(next)
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