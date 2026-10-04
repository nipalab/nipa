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
  is_admin: true,
  is_super_admin: false,
  deleted: false,
}

const BRANCHES = [
  { id: '1', name: 'main', is_default: true, is_protected: false, commit_id: 'c2', updated_at: new Date().toISOString() },
]

const RULES = [
  {
    id: 1,
    user_id: 'u9',
    user_name: 'user1',
    user_email: 'user1@gmail.com',
    path_prefix: '',
    permission: 7,
  },
  {
    id: 2,
    group_id: 'g1',
    group_name: 'Coder',
    path_prefix: 'src',
    permission: 3,
  },
]

function stubSettingsRoutes(initialProtected = false) {
  let isProtected = initialProtected
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const path = url.split('?')[0]
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      if (path.endsWith('/permissions/rules')) return jsonResponse(RULES)
      if (path.endsWith('/permissions/defaults')) return jsonResponse([])
      if (path.endsWith('/permissions/me')) {
        return jsonResponse({ project_permission: 3, rules: [], defaults: [] })
      }
      if (path.endsWith('/protection')) {
        isProtected = Boolean(JSON.parse(String(init?.body ?? '{}')).protected)
        return jsonResponse({ ...BRANCHES[0], is_protected: isProtected })
      }
      if (path.endsWith('/branches')) {
        return jsonResponse(BRANCHES.map((branch) => ({ ...branch, is_protected: isProtected })))
      }
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
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

describe('ProjectSettingsPage access rules', () => {
  it('shows the subject name and email instead of the raw ids', async () => {
    stubSettingsRoutes()
    window.history.pushState({}, '', '/sticker/backend/settings')
    const container = document.createElement('div')
    container.id = 'root'
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<App />)
    })
    await waitForText('Access rules')
    await waitForText('user1@gmail.com')
    await waitForText('Coder')

    expect(document.body.textContent).not.toContain('u9')
    expect(document.body.textContent).not.toContain('g1')
    act(() => root.unmount())
  })
})

async function renderSettings(url: string) {
  stubSettingsRoutes()
  window.history.pushState({}, '', url)
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<App />)
  })
  return root
}

function settingsNav() {
  return document.querySelector('nav[aria-label="Repository settings"]') as HTMLElement | null
}

describe('ProjectSettingsPage github-style navigation', () => {
  it('renders a settings sidebar with access, branches and webhooks', async () => {
    const root = await renderSettings('/sticker/backend/settings')
    await waitForText('Access rules')

    const nav = settingsNav()
    expect(nav).not.toBeNull()
    const labels = Array.from(nav?.querySelectorAll('a') ?? []).map((link) => link.textContent)
    expect(labels).toEqual(['Access', 'Branches', 'Webhooks'])
    expect(nav?.querySelector('a[aria-current="page"]')?.textContent).toBe('Access')
    expect(document.body.textContent).not.toContain('Protected branches')

    act(() => root.unmount())
  })

  it('shows only the selected section for ?tab=branches', async () => {
    const root = await renderSettings('/sticker/backend/settings?tab=branches')
    await waitForText('Protected branches')

    expect(settingsNav()?.querySelector('a[aria-current="page"]')?.textContent).toBe('Branches')
    expect(document.body.textContent).toContain('main')
    expect(document.body.textContent).not.toContain('Access rules')
    expect(document.body.textContent).not.toContain('Create webhook')

    act(() => root.unmount())
  })

  it('shows only the webhooks section for ?tab=webhooks', async () => {
    const root = await renderSettings('/sticker/backend/settings?tab=webhooks')
    await waitForText('Webhooks')

    expect(settingsNav()?.querySelector('a[aria-current="page"]')?.textContent).toBe('Webhooks')
    expect(document.body.textContent).not.toContain('Protected branches')

    act(() => root.unmount())
  })

  it('protects a branch from the branch protection toggle', async () => {
    const root = await renderSettings('/sticker/backend/settings?tab=branches')
    await waitForText('Protected branches')

    const toggle = document.querySelector('button[aria-labelledby="branch-protection-0"]')
    expect(toggle).not.toBeNull()
    expect(toggle?.getAttribute('aria-pressed')).toBe('false')
    await act(async () => {
      ;(toggle as HTMLButtonElement).click()
    })

    await waitFor(() => {
      const calls = (globalThis.fetch as unknown as ReturnType<typeof vi.fn>).mock.calls
      const protectedCall = calls.find((call) => String(call[0]).endsWith('/branches/main/protection'))
      expect(protectedCall).toBeDefined()
      expect((protectedCall?.[1] as RequestInit | undefined)?.method).toBe('PUT')
      return true
    })
    await waitForText('Protected — pushes and deletions are restricted.')

    act(() => root.unmount())
  })
})