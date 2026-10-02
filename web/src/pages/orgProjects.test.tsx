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

async function waitForText(text: string, timeoutMs = 2000) {
  await waitFor(() => document.body.textContent?.includes(text) ?? false, timeoutMs)
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

const ORGS = [{ id: '1', slug: 'default', name: 'Default', role: 'owner' }]

const PROJECTS = [{ id: 'p1', org_id: '1', slug: 'engine', name: 'Engine', description: 'Core engine' }]

type FetchCall = { url: string; method: string; body: unknown }

function stubOrgRoutes(requests: FetchCall[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      requests.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      const path = url.split('?')[0]
      if (path.endsWith('/orgs/default/projects/engine')) return jsonResponse({ message: 'project deleted' })
      if (path.endsWith('/orgs/default/projects')) {
        if (method === 'POST') {
          return jsonResponse({ id: 'p2', org_id: '1', slug: 'game-client', name: 'Game Client', description: '' })
        }
        return jsonResponse(PROJECTS)
      }
      if (path.endsWith('/api/v1/orgs')) return jsonResponse(ORGS)
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

function setInputValue(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

function findButton(text: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === text)
  if (!button) throw new Error(`button ${text} not found`)
  return button as HTMLButtonElement
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
  document.body.innerHTML = ''
})

describe('OrgProjectsPage', () => {
  it('deletes a project only after typing its slug', async () => {
    const requests: FetchCall[] = []
    stubOrgRoutes(requests)
    const { root } = await renderApp('/default')
    await waitForText('Engine')

    await act(async () => {
      findButton('Delete').click()
    })
    await waitFor(() => document.querySelector('[role="dialog"]') !== null)
    await waitForText('Type engine to confirm')

    expect(findButton('Delete project').disabled).toBe(true)

    const dialog = document.querySelector('[role="dialog"]') as HTMLElement
    const input = dialog.querySelector('input') as HTMLInputElement
    await act(async () => {
      setInputValue(input, 'engin')
    })
    expect(findButton('Delete project').disabled).toBe(true)

    await act(async () => {
      setInputValue(input, 'engine')
    })
    expect(findButton('Delete project').disabled).toBe(false)

    await act(async () => {
      findButton('Delete project').click()
    })
    await waitFor(() =>
      requests.some((call) => call.method === 'DELETE' && call.url.includes('/projects/engine')),
    )
    await waitFor(() => document.querySelector('[role="dialog"]') === null)
    act(() => root.unmount())
  })

  it('creates a project from the dedicated new repository page', async () => {
    const requests: FetchCall[] = []
    stubOrgRoutes(requests)
    const { container, root } = await renderApp('/default')
    await waitForText('Engine')

    await act(async () => {
      findButton('New repository').click()
    })
    await waitFor(() => window.location.pathname === '/default/new')
    await waitForText('New repository in default')

    const inputs = container.querySelectorAll('input')
    expect(inputs).toHaveLength(2)
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Game Client')
    })
    expect((inputs[1] as HTMLInputElement).value).toBe('game-client')

    await act(async () => {
      findButton('Create repository').click()
    })
    await waitFor(() =>
      requests.some((call) => call.method === 'POST' && call.url.includes('/orgs/default/projects')),
    )
    const post = requests.find((call) => call.method === 'POST' && call.url.includes('/orgs/default/projects'))
    expect(post?.url).toContain('/api/v1/orgs/default/projects')
    expect(post?.body).toEqual({ name: 'Game Client', description: '', slug: '' })
    await waitFor(() => window.location.pathname === '/default/game-client')
    act(() => root.unmount())
  })
})

describe('HomePage', () => {
  it('creates an organization from the home page', async () => {
    const requests: FetchCall[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = init?.method ?? 'GET'
        requests.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        if (url.includes('/auth/refresh')) {
          return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
        }
        if (url.includes('/api/v1/me')) return jsonResponse(ME)
        const path = url.split('?')[0]
        if (path.endsWith('/api/v1/orgs')) {
          if (method === 'POST') return jsonResponse({ id: '2', slug: 'acme', name: 'Acme Corp', role: 'owner' })
          return jsonResponse(ORGS)
        }
        if (path.endsWith('/orgs/acme/projects')) return jsonResponse([])
        return jsonResponse({ error: 'not found' }, 404)
      }),
    )
    const { root } = await renderApp('/')
    await waitForText('Default')

    await act(async () => {
      findButton('New organization').click()
    })
    await waitFor(() => document.querySelector('[role="dialog"]') !== null)

    const dialog = document.querySelector('[role="dialog"]') as HTMLElement
    const input = dialog.querySelector('input') as HTMLInputElement
    await act(async () => {
      setInputValue(input, 'Acme Corp')
    })

    await act(async () => {
      findButton('Create organization').click()
    })
    await waitFor(() => requests.some((call) => call.method === 'POST' && call.url.endsWith('/api/v1/orgs')))
    const post = requests.find((call) => call.method === 'POST' && call.url.endsWith('/api/v1/orgs'))
    expect(post?.body).toEqual({ name: 'Acme Corp', slug: '' })
    await waitFor(() => window.location.pathname === '/acme')
    act(() => root.unmount())
  })
})
