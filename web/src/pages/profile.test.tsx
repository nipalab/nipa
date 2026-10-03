import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { clearSession } from '../api/client'

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

async function waitFor(condition: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (condition()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for condition')
}

async function waitForText(container: HTMLElement, text: string, timeoutMs = 2000) {
  await waitFor(() => container.textContent?.includes(text) ?? false, timeoutMs)
}

const ME = {
  id: '1',
  name: 'Alice',
  email: 'alice@example.com',
  photo_url: '',
  is_admin: false,
  is_super_admin: false,
  deleted: false,
}

function stubRoutes(extra: (url: string, init?: RequestInit) => Response | null = () => null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const custom = extra(url, init)
      if (custom) return custom
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me/password') && method === 'POST') {
        return jsonResponse({ message: 'password updated' })
      }
      if (url.endsWith('/api/v1/me') && method === 'GET') return jsonResponse(ME)
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

function findButton(text: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === text)
  if (!button) throw new Error(`button ${text} not found`)
  return button as HTMLButtonElement
}

function setInputValue(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

async function renderApp(url: string) {
  window.history.pushState({}, '', url)
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<App />)
  })
  return { container, root }
}

beforeEach(() => {
  clearSession()
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('ProfilePage', () => {
  it('updates the public profile with a live avatar preview', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/me') && method === 'PATCH') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ ...ME, name: 'Alice Smith', photo_url: 'https://example.com/a.png' })
      }
      return null
    })

    const { container, root } = await renderApp('/settings/profile')
    await waitFor(() => (container.querySelectorAll('input')[1] as HTMLInputElement)?.value === 'Alice')
    expect(container.textContent).toContain('Public profile')
    expect(container.textContent).toContain('Change password')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'https://example.com/a.png')
      setInputValue(inputs[1] as HTMLInputElement, 'Alice Smith')
    })
    await act(async () => {
      findButton('Update profile').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.body).toEqual({ name: 'Alice Smith', photo_url: 'https://example.com/a.png' })
    await waitForText(container, 'Profile updated.')
    expect(container.querySelector('img[src="https://example.com/a.png"]')).not.toBeNull()
    act(() => root.unmount())
  })

  it('validates the new password and submits a matching change', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/api/v1/me/password') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ message: 'password updated' })
      }
      return null
    })

    const { container, root } = await renderApp('/settings/profile')
    await waitFor(() => container.querySelectorAll('input').length === 5)
    const inputs = container.querySelectorAll('input')

    await act(async () => {
      setInputValue(inputs[2] as HTMLInputElement, 'oldpass')
      setInputValue(inputs[3] as HTMLInputElement, 'short')
    })
    await waitForText(container, 'at least 8 characters')

    await act(async () => {
      setInputValue(inputs[3] as HTMLInputElement, 'secret123')
      setInputValue(inputs[4] as HTMLInputElement, 'different')
    })
    await waitForText(container, 'Passwords do not match.')

    await act(async () => {
      setInputValue(inputs[4] as HTMLInputElement, 'secret123')
    })
    await act(async () => {
      findButton('Update password').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.body).toEqual({ old_password: 'oldpass', new_password: 'secret123' })
    await waitForText(container, 'Password updated.')
    act(() => root.unmount())
  })
})
