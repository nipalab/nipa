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
  name: 'Root',
  email: 'root@example.com',
  photo_url: '',
  is_admin: true,
  is_super_admin: true,
  deleted: false,
}

const USERS = [
  { id: '1', name: 'Root', email: 'root@example.com', photo_url: '', is_admin: true, is_super_admin: true, deleted: false },
  { id: '2', name: 'Alice', email: 'alice@example.com', photo_url: '', is_admin: true, is_super_admin: false, deleted: false },
  { id: '3', name: 'Bob', email: 'bob@example.com', photo_url: '', is_admin: false, is_super_admin: false, deleted: false },
]

function stubRoutes(extra: (url: string, init?: RequestInit) => Response | null = () => null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const custom = extra(url, init)
      if (custom) return custom
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      if (url.includes('/api/v1/users')) return jsonResponse(USERS)
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

describe('AdminUsersPage', () => {
  it('lists users with roles and filters them', async () => {
    stubRoutes()
    const { container, root } = await renderApp('/admin/users')
    await waitForText(container, 'Alice')
    expect(container.textContent).toContain('Super admin')
    expect(container.textContent).toContain('Admin')
    expect(container.textContent).toContain('Member')
    expect(container.textContent).toContain('(you)')

    await act(async () => {
      setInputValue(container.querySelector('[aria-label="Filter users"]') as HTMLInputElement, 'bob')
    })
    await waitFor(() => !(container.textContent ?? '').includes('Alice'))
    expect(container.textContent).toContain('Bob')
    act(() => root.unmount())
  })

  it('creates a user from the dedicated page', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url.includes('/api/v1/users') && method === 'POST') {
        return jsonResponse({
          id: '4',
          name: 'Carol',
          email: 'carol@example.com',
          photo_url: '',
          is_admin: false,
          is_super_admin: false,
          deleted: false,
        })
      }
      return null
    })

    const { container, root } = await renderApp('/admin/users')
    await waitForText(container, 'New user')
    await act(async () => {
      findButton('New user').click()
    })
    await waitFor(() => window.location.pathname === '/admin/users/new')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Carol')
      setInputValue(inputs[1] as HTMLInputElement, 'carol@example.com')
      setInputValue(inputs[2] as HTMLInputElement, 'secret123')
    })
    await act(async () => {
      findButton('Create user').click()
    })
    await waitFor(() => calls.some((call) => call.method === 'POST' && call.url.includes('/api/v1/users')))
    const post = calls.find((call) => call.method === 'POST' && call.url.includes('/api/v1/users'))
    expect(post?.body).toEqual({ name: 'Carol', email: 'carol@example.com', password: 'secret123' })
    await waitFor(() => window.location.pathname === '/admin/users')
    act(() => root.unmount())
  })
})
