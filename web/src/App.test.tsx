import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { clearSession } from './api/client'

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

async function waitForText(container: HTMLElement, text: string, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (container.textContent?.includes(text)) {
      return
    }
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error(`timed out waiting for ${text}`)
}

beforeEach(() => {
  clearSession()
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function setInputValue(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

async function renderApp() {
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<App />)
  })
  return { container, root }
}

describe('App', () => {
  it('shows the login form when the refresh cookie is missing', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'refresh token required' }, 401)))

    const { container, root } = await renderApp()
    await waitForText(container, 'Sign in')
    expect(container.querySelectorAll('input')).toHaveLength(2)
    act(() => root.unmount())
  })

  it('shows repositories after a silent refresh', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/auth/refresh')) {
          return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
        }
        if (url.includes('/api/v1/me')) {
          return jsonResponse({
            id: '1',
            name: 'Alice',
            email: 'alice@example.com',
            photo_url: '',
            is_admin: true,
            is_super_admin: false,
            deleted: false,
          })
        }
        if (url.includes('/api/v1/orgs')) {
          return jsonResponse([])
        }
        return jsonResponse({ error: 'not found' }, 404)
      }),
    )

    const { container, root } = await renderApp()
    await waitForText(container, 'Repositories')
    act(() => root.unmount())
  })
})

describe('LoginPage', () => {
  function mockAuthRoutes(login: (url: string) => Response | null) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/auth/refresh')) {
          return jsonResponse({ error: 'refresh token required' }, 401)
        }
        const loginResponse = login(url)
        if (loginResponse) {
          return loginResponse
        }
        return jsonResponse({ error: 'not found' }, 404)
      }),
    )
  }

  it('accepts usernames that are not email addresses', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/auth/refresh')) return jsonResponse({ error: 'refresh token required' }, 401)
      if (url.includes('/auth/login')) {
        calls.push(url)
        return jsonResponse({ access_token: 't', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) {
        return jsonResponse({ id: '1', name: 'A', email: 'supernipa', photo_url: '', is_admin: true, is_super_admin: true, deleted: false })
      }
      if (url.includes('/api/v1/orgs')) return jsonResponse([])
      return jsonResponse({ error: 'not found' }, 404)
    }))

    const { container, root } = await renderApp()
    await waitForText(container, 'Sign in')
    const inputs = container.querySelectorAll('input')
    expect(inputs[0].getAttribute('type')).toBe('text')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'supernipa')
      setInputValue(inputs[1] as HTMLInputElement, 'supernipa')
    })
    await act(async () => {
      ;(container.querySelector('button[type="submit"]') as HTMLButtonElement).click()
    })
    await waitForText(container, 'Repositories')
    expect(calls.some((url) => url.includes('/auth/login'))).toBe(true)
    act(() => root.unmount())
  })

  it('shows the server error message when login fails', async () => {
    mockAuthRoutes((url) =>
      url.includes('/auth/login')
        ? jsonResponse({ error: 'invalid email or password' }, 401)
        : null,
    )
    const { container, root } = await renderApp()
    await waitForText(container, 'Sign in')
    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'supernipa')
      setInputValue(inputs[1] as HTMLInputElement, 'wrong')
    })
    await act(async () => {
      ;(container.querySelector('button[type="submit"]') as HTMLButtonElement).click()
    })
    await waitForText(container, 'invalid email or password')
    act(() => root.unmount())
  })
})
