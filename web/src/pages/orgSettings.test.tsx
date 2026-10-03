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

const ORGS = [{ id: 'o1', slug: 'sticker', name: 'Sticker', role: 'owner' }]

const ORG_MEMBERS = [
  {
    user_id: '1',
    name: 'Alice',
    email: 'alice@example.com',
    photo_url: '',
    is_admin: true,
    is_super_admin: false,
    role: 'owner',
  },
]

const GROUPS = [{ id: 'g1', org_id: 'o1', name: 'Coder', description: 'coders' }]

const GROUP_DETAIL = {
  id: 'g1',
  org_id: 'o1',
  name: 'Coder',
  description: 'coders',
  member_ids: ['u9'],
  members: [{ user_id: 'u9', name: 'user1', email: 'user1@gmail.com' }],
}

function stubOrgSettings() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      const path = url.split('?')[0]
      if (path.endsWith('/orgs/sticker/groups/g1')) return jsonResponse(GROUP_DETAIL)
      if (path.endsWith('/orgs/sticker/groups')) return jsonResponse(GROUPS)
      if (path.endsWith('/orgs/sticker/members')) return jsonResponse(ORG_MEMBERS)
      if (path.endsWith('/api/v1/orgs')) return jsonResponse(ORGS)
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

function findButton(text: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === text)
  if (!button) throw new Error(`button ${text} not found`)
  return button as HTMLButtonElement
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

describe('OrgSettingsPage groups', () => {
  it('shows the email of a group member that is not an organization member', async () => {
    stubOrgSettings()
    window.history.pushState({}, '', '/sticker/settings')
    const container = document.createElement('div')
    container.id = 'root'
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<App />)
    })
    await waitForText('Coder')

    expect(document.body.textContent).not.toContain('user1@gmail.com')

    await act(async () => {
      findButton('View').click()
    })
    await waitForText('user1@gmail.com')
    await waitForText('Coder members')

    expect(document.body.textContent).not.toContain('u9')
    act(() => root.unmount())
  })
})