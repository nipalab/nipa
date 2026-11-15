import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { clearSession } from '../api/client'
import { resetMaintenance } from '../maintenance'

function jsonResponse(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, text: async () => JSON.stringify(body) } as Response
}

function refusal(mode: string, scope = 'server', extra: Record<string, string> = {}): Response {
  const body = { error: `refused (${mode})`, maintenance: { scope, mode, message: 'moving the database', ...extra } }
  return {
    ok: false,
    status: 503,
    headers: new Headers({ 'X-Nipa-Maintenance': `${scope};${mode}` }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

const ME = { id: '1', name: 'Root', email: 'root@example.com', photo_url: '', is_admin: true, is_super_admin: true, deleted: false }
const TOKENS = { access_token: 'token', token_type: 'Bearer', expires_in: 1800 }

const text = () => document.body.textContent ?? ''

async function waitFor(condition: () => boolean, timeoutMs = 3000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (condition()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error(`timed out; page shows: ${text().slice(0, 300)}`)
}

let unmount: (() => void) | null = null

async function renderApp(path: string) {
  window.history.pushState({}, '', path)
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => root.render(<App />))
  unmount = () => act(() => root.unmount())
  return container
}

beforeEach(() => {
  clearSession()
  resetMaintenance()
})

afterEach(() => {
  unmount?.()
  unmount = null
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
})

describe('maintenance in the app', () => {
  it('shows the maintenance page instead of the login page while the server is frozen', async () => {
    const state = { frozen: true }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/maintenance') {
          return jsonResponse({ server: state.frozen ? { mode: 'frozen', active: true, message: 'moving the database' } : null, source: 'db' })
        }
        if (url.includes('/auth/refresh')) return state.frozen ? refusal('frozen') : jsonResponse(TOKENS)
        if (url.includes('/api/v1/me/terms')) return jsonResponse({}, 404)
        if (url.endsWith('/api/v1/me')) return jsonResponse(ME)
        if (url.endsWith('/api/v1/orgs')) return jsonResponse([])
        return jsonResponse({}, 404)
      }),
    )

    await renderApp('/dashboard')
    await waitFor(() => text().includes('The server is frozen for maintenance'))
    expect(window.location.pathname).toBe('/dashboard')
    expect(text()).toContain('moving the database')

    state.frozen = false
    const button = [...document.querySelectorAll('button')].find((item) => item.textContent?.includes('Check again'))
    await act(async () => button?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await waitFor(() => !text().includes('frozen for maintenance') && document.querySelector('header') !== null)
    expect(window.location.pathname).toBe('/dashboard')
  })

  it('shows the closed page after a refused read and keeps the admin page reachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/maintenance') return jsonResponse({ server: { mode: 'full', active: true, message: 'racks' }, source: 'db' })
        if (url.includes('/auth/refresh')) return jsonResponse(TOKENS)
        if (url.endsWith('/api/v1/me')) return jsonResponse(ME)
        if (url.includes('/api/v1/admin/maintenance')) {
          return jsonResponse({ server: { mode: 'full', active: true, message: 'racks' }, source: 'db', orgs: [], projects: [] })
        }
        return refusal('full')
      }),
    )

    await renderApp('/dashboard')
    await waitFor(() => text().includes('The server is closed for maintenance'))
    expect(text()).toContain('Manage maintenance')

    unmount?.()
    document.body.innerHTML = ''
    resetMaintenance()
    await renderApp('/admin/maintenance')
    await waitFor(() => text().includes('Organization windows'))
    expect(text()).toContain('Update maintenance')
  })

  it('explains a refused sign-in on the login page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/maintenance') {
          return jsonResponse({ server: { mode: 'full', active: false, message: 'upgrade', starts_at: '2099-01-01T10:00:00Z' }, source: 'db' })
        }
        if (url.includes('/auth/refresh')) return jsonResponse({ error: 'refresh token required' }, 401)
        if (url.includes('/auth/login')) return refusal('frozen')
        return jsonResponse({}, 404)
      }),
    )

    await renderApp('/login')
    await waitFor(() => text().includes('has maintenance scheduled'))
    const inputs = document.querySelectorAll<HTMLInputElement>('input')
    for (const [input, value] of [
      [inputs[0], 'root'],
      [inputs[1], 'pw'],
    ] as const) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }
    const form = document.querySelector('form')
    await act(async () => form?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    await waitFor(() => text().includes('Sign-in is paused for maintenance'))
    expect(text()).not.toContain('has maintenance scheduled')
  })
})
