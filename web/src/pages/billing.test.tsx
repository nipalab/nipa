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

async function waitForText(text: string, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (document.body.textContent?.includes(text)) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error(`timed out waiting for ${text}`)
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

const BILLING = {
  user_id: '1',
  used_bytes: 2048,
  quota_bytes: null,
  orgs: [{ id: 'o1', slug: 'sticker', name: 'Sticker', used_bytes: 2048 }],
}

function stubBilling() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      if (url.split('?')[0].endsWith('/api/v1/billing/me')) return jsonResponse(BILLING)
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

// stubBillingDisabled simulates a free server: the ledger routes are not
// registered and answer 404.
function stubBillingDisabled() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
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

describe('BillingPage', () => {
  it('shows the owner aggregate usage and per-org breakdown', async () => {
    stubBilling()
    window.history.pushState({}, '', '/settings/billing')
    const container = document.createElement('div')
    container.id = 'root'
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<App />)
    })
    await waitForText('Organizations')

    expect(document.body.textContent).toContain('2.0 KiB used · unlimited quota')
    const row = Array.from(document.querySelectorAll('tr')).find((item) => item.textContent?.includes('Sticker'))
    expect(row?.textContent).toContain('2.0 KiB')
    expect(document.body.textContent).not.toContain('Set owner quota')
    const navLink = Array.from(document.querySelectorAll('a')).find((a) => a.textContent?.trim() === 'Billing')
    expect(navLink).toBeDefined()
    act(() => root.unmount())
  })

  it('keeps the nav link and explains when the ledger is disabled', async () => {
    stubBillingDisabled()
    window.history.pushState({}, '', '/settings/billing')
    const container = document.createElement('div')
    container.id = 'root'
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<App />)
    })
    await waitForText('Storage billing is not enabled on this server.')

    const navLink = Array.from(document.querySelectorAll('a')).find((a) => a.textContent?.trim() === 'Billing')
    expect(navLink).toBeDefined()
    expect(document.body.textContent).not.toContain('Billing owner')
    act(() => root.unmount())
  })
})
