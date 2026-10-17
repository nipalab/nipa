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
  notify_email: true,
  deleted: false,
}

const CAPABILITIES = { registration: true, providers: ['github'] }

type RouteStub = (url: string, init?: RequestInit) => Response | null

function stubRoutes(extra: RouteStub = () => null) {
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
      if (url.includes('/api/v1/auth/capabilities')) return jsonResponse(CAPABILITIES)
      if (url.endsWith('/api/v1/me') && method === 'GET') return jsonResponse(ME)
      if (url.endsWith('/api/v1/orgs') && method === 'GET') return jsonResponse([])
      if (url.endsWith('/api/v1/keys') && method === 'GET') return jsonResponse([])
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

function findButtonIn(root: HTMLElement, text: string): HTMLButtonElement {
  const button = Array.from(root.querySelectorAll('button')).find((item) => item.textContent?.trim() === text)
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
})

describe('RegisterPage', () => {
  it('registers an account and offers resending the confirmation email', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/auth/register') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ status: 'pending_verification', email: 'alice@example.com' }, 202)
      }
      if (url.endsWith('/api/v1/auth/register/resend') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ message: 'accepted' })
      }
      return null
    })

    const { container } = await renderApp('/register')
    await waitForText(container, 'Create account')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Alice')
      setInputValue(inputs[1] as HTMLInputElement, 'alice@example.com')
      setInputValue(inputs[2] as HTMLInputElement, 'password123')
    })
    await act(async () => {
      findButtonIn(container, 'Create account').click()
    })

    await waitForText(container, 'confirmation link')
    expect(calls[0]).toEqual({
      url: expect.stringContaining('/api/v1/auth/register'),
      method: 'POST',
      body: { name: 'Alice', email: 'alice@example.com', password: 'password123' },
    })

    await act(async () => {
      findButtonIn(container, 'Resend confirmation email').click()
    })
    await waitFor(() => calls.length > 1)
    expect(calls[1]?.body).toEqual({ email: 'alice@example.com' })
  })

  it('explains that registration is disabled on servers without SaaS mode', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/auth/refresh')) return jsonResponse({ error: 'unauthorized' }, 401)
        return jsonResponse({ error: 'not found' }, 404)
      }),
    )

    const { container } = await renderApp('/register')
    await waitForText(container, 'Registration is not enabled')
  })
})

describe('VerifyPage', () => {
  it('confirms the email, signs in and lands on onboarding', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/auth/verify') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      return null
    })

    const { container } = await renderApp('/verify?token=abc')
    await waitForText(container, 'Name your workspace')

    expect(calls[0]?.body).toEqual({ token: 'abc' })
    expect(container.textContent).toContain('Welcome to Nipa')
  })

  it('offers a resend when the link is invalid', async () => {
    stubRoutes((url, init) => {
      if (url.endsWith('/api/v1/auth/verify') && (init?.method ?? 'GET') === 'POST') {
        return jsonResponse({ error: 'invalid or expired verification link' }, 404)
      }
      return null
    })

    const { container } = await renderApp('/verify?token=stale')
    await waitForText(container, 'Confirmation failed')
    expect(container.textContent).toContain('Resend confirmation email')
  })
})

describe('OnboardingPage', () => {
  it('creates the first workspace and navigates to it', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/orgs') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ id: '9', slug: 'acme', name: 'Acme', role: 'owner' })
      }
      if (url.endsWith('/api/v1/orgs/acme/projects') && method === 'GET') return jsonResponse([])
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Name your workspace')

    const input = container.querySelector('input') as HTMLInputElement
    await act(async () => {
      setInputValue(input, 'Acme')
    })
    await act(async () => {
      findButtonIn(container, 'Create workspace').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.body).toEqual({ name: 'Acme', slug: '' })
    await waitFor(() => window.location.pathname === '/acme')
  })
})

describe('OAuthCallbackPage', () => {
  it('exchanges the code and lands on onboarding for new accounts', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/auth/oauth/complete') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      return null
    })

    const { container } = await renderApp('/oauth/callback?code=xyz&new=1')
    await waitForText(container, 'Name your workspace')
    expect(calls[0]?.body).toEqual({ code: 'xyz' })
  })

  it('surfaces provider linking errors', async () => {
    stubRoutes()

    const { container } = await renderApp('/oauth/callback?oauth_error=email_conflict')
    await waitForText(container, 'Sign-in failed')
    expect(container.textContent).toContain('already exists')
  })
})

describe('IdentitiesCard', () => {
  it('lists connected accounts and disconnects them', async () => {
    const calls: { method: string; url: string }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/me/identities') && method === 'GET') {
        return jsonResponse([
          { id: '9', provider: 'github', email: 'alice@example.com', created_at: '2026-07-01T12:00:00Z' },
        ])
      }
      if (url.endsWith('/api/v1/me/identities/9') && method === 'DELETE') {
        calls.push({ method, url })
        return jsonResponse({ message: 'identity unlinked' })
      }
      return null
    })

    const { container } = await renderApp('/settings/profile')
    await waitForText(container, 'Connected accounts')
    await waitForText(container, 'GitHub')

    await act(async () => {
      findButton('Disconnect').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.method).toBe('DELETE')
  })
})
