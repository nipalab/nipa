import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { oauthStartURL } from '../api/endpoints'
import { clearSession } from '../api/client'
import { socialRedirect } from '../components/SocialButtons'

vi.mock('../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/endpoints')>()
  return { ...actual, oauthStartURL: vi.fn(actual.oauthStartURL) }
})

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

function stubAnonymous(extra: RouteStub = () => null) {
  stubRoutes((url, init) => {
    if (url.includes('/auth/refresh')) return jsonResponse({ error: 'refresh token required' }, 401)
    return extra(url, init)
  })
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

async function renderApp(url: string, state: unknown = {}) {
  // react-router reads history.state.usr (see @remix-run/router getLocation).
  window.history.pushState({ usr: state, key: 'test', idx: 0 }, '', url)
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
  vi.mocked(oauthStartURL).mockClear()
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('RegisterPage', () => {
  it('registers an account and offers resending the confirmation email', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubAnonymous((url, init) => {
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
    expect(inputs).toHaveLength(2)
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Alice')
      setInputValue(inputs[1] as HTMLInputElement, 'alice@example.com')
    })
    await act(async () => {
      findButtonIn(container, 'Create account').click()
    })

    await waitForText(container, 'confirmation link')
    expect(calls[0]).toEqual({
      url: expect.stringContaining('/api/v1/auth/register'),
      method: 'POST',
      body: { name: 'Alice', email: 'alice@example.com', accept_terms: false },
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
  it('lets the link holder choose a password, signs in and lands on onboarding', async () => {
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
    await waitForText(container, 'choose a password')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'short')
      setInputValue(inputs[1] as HTMLInputElement, 'short')
    })
    await act(async () => {
      findButtonIn(container, 'Confirm and continue').click()
    })
    await waitForText(container, 'at least 8 characters.')
    expect(calls).toHaveLength(0)

    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, '😀'.repeat(19))
      setInputValue(inputs[1] as HTMLInputElement, '😀'.repeat(19))
    })
    await act(async () => {
      findButtonIn(container, 'Confirm and continue').click()
    })
    await waitForText(container, 'at most 72 bytes')
    expect(calls).toHaveLength(0)

    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'password123')
      setInputValue(inputs[1] as HTMLInputElement, 'different123')
    })
    await act(async () => {
      findButtonIn(container, 'Confirm and continue').click()
    })
    await waitForText(container, 'do not match')
    expect(calls).toHaveLength(0)

    await act(async () => {
      setInputValue(inputs[1] as HTMLInputElement, 'password123')
    })
    await act(async () => {
      findButtonIn(container, 'Confirm and continue').click()
    })
    await waitForText(container, 'Name your workspace')

    expect(calls[0]?.body).toEqual({ token: 'abc', password: 'password123' })
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
    await waitForText(container, 'choose a password')
    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'password123')
      setInputValue(inputs[1] as HTMLInputElement, 'password123')
    })
    await act(async () => {
      findButtonIn(container, 'Confirm and continue').click()
    })
    await waitForText(container, 'Confirmation failed')
    expect(container.textContent).toContain('Resend confirmation email')
  })

  it('offers a resend when the link has no token', async () => {
    stubRoutes(() => null)

    const { container } = await renderApp('/verify')
    await waitForText(container, 'missing its token')
    expect(container.textContent).toContain('Resend confirmation email')
  })
})

describe('OnboardingPage', () => {
  it('creates the first workspace with the derived slug and navigates to it', async () => {
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

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Acme')
    })
    expect((inputs[1] as HTMLInputElement).value).toBe('acme')

    await act(async () => {
      findButtonIn(container, 'Create workspace').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.body).toEqual({ name: 'Acme', slug: 'acme' })
    await waitFor(() => window.location.pathname === '/acme')
  })

  it('lets the user override the derived slug', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/orgs') && method === 'POST') {
        calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
        return jsonResponse({ id: '9', slug: 'acme-studio', name: 'Acme Game Studio', role: 'owner' })
      }
      if (url.endsWith('/api/v1/orgs/acme-studio/projects') && method === 'GET') return jsonResponse([])
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Name your workspace')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Acme Game Studio')
    })
    expect((inputs[1] as HTMLInputElement).value).toBe('acme-game-studio')

    await act(async () => {
      setInputValue(inputs[1] as HTMLInputElement, 'acme-studio')
    })
    await act(async () => {
      findButtonIn(container, 'Create workspace').click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.body).toEqual({ name: 'Acme Game Studio', slug: 'acme-studio' })
    await waitFor(() => window.location.pathname === '/acme-studio')
  })

  it('reports a taken slug at the field and stays on the page', async () => {
    stubRoutes((url, init) => {
      if (url.endsWith('/api/v1/orgs') && (init?.method ?? 'GET') === 'POST') {
        return jsonResponse({ error: 'organization slug already in use' }, 409)
      }
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Name your workspace')

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Acme')
    })
    await act(async () => {
      findButtonIn(container, 'Create workspace').click()
    })

    await waitForText(container, 'already taken')
    expect(window.location.pathname).toBe('/onboarding')
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

  it('returns to the from destination after exchanging the code', async () => {
    stubRoutes((url, init) => {
      if (url.endsWith('/api/v1/auth/oauth/complete') && (init?.method ?? 'GET') === 'POST') {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      return null
    })

    await renderApp('/oauth/callback?code=xyz&from=%2Facme%2Fproj')
    await waitFor(() => window.location.pathname === '/acme/proj')
  })

  it('rejects an off-site from destination', async () => {
    stubRoutes((url, init) => {
      if (url.endsWith('/api/v1/auth/oauth/complete') && (init?.method ?? 'GET') === 'POST') {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      return null
    })

    await renderApp('/oauth/callback?code=xyz&from=%2F%2Fevil.example')
    await waitFor(() => window.location.pathname === '/dashboard')
  })
})

describe('SocialButtons', () => {
  it('builds callback redirects that carry the destination', () => {
    expect(socialRedirect('/')).toBe('/oauth/callback')
    expect(socialRedirect('/acme/proj')).toBe('/oauth/callback?from=%2Facme%2Fproj')
    expect(socialRedirect('//evil.example')).toBe('/oauth/callback?from=%2Fdashboard')
    expect(socialRedirect('https://evil.example')).toBe('/oauth/callback?from=%2Fdashboard')
  })

  it('routes social sign-in through the OAuth callback page', async () => {
    stubAnonymous()

    const { container } = await renderApp('/login')
    await waitForText(container, 'Continue with GitHub')
    await act(async () => {
      findButtonIn(container, 'Continue with GitHub').click()
    })

    expect(vi.mocked(oauthStartURL)).toHaveBeenCalledWith('github', '/oauth/callback?from=%2Fdashboard', false)
  })

  it('carries the post-login destination through the OAuth round-trip', async () => {
    stubAnonymous()

    const { container } = await renderApp('/login', { from: '/acme/proj' })
    await waitForText(container, 'Continue with GitHub')
    await act(async () => {
      findButtonIn(container, 'Continue with GitHub').click()
    })

    expect(vi.mocked(oauthStartURL)).toHaveBeenCalledWith('github', '/oauth/callback?from=%2Facme%2Fproj', false)
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

    const { container } = await renderApp('/settings/profile?tab=accounts')
    await waitForText(container, 'Connected accounts')
    await waitForText(container, 'GitHub')

    await act(async () => {
      findButton('Disconnect').click()
    })
    await waitFor(() => document.querySelector('[role="alertdialog"]') !== null)
    await act(async () => {
      const confirmButton = Array.from(
        (document.querySelector('[role="alertdialog"]') as HTMLElement).querySelectorAll('button'),
      ).find((item) => item.textContent?.trim() === 'Disconnect')
      confirmButton?.click()
    })
    await waitFor(() => calls.length > 0)
    expect(calls[0]?.method).toBe('DELETE')
  })
})

describe('Terms acceptance', () => {
  const TERMS_CAPABILITIES = { ...CAPABILITIES, terms: { version: '2026-11-10' } }

  it('states the terms on sign-up and records the acceptance', async () => {
    const calls: unknown[] = []
    stubAnonymous((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/api/v1/auth/capabilities')) return jsonResponse(TERMS_CAPABILITIES)
      if (url.endsWith('/api/v1/auth/register') && method === 'POST') {
        calls.push(JSON.parse(String(init?.body)))
        return jsonResponse({ status: 'pending_verification', email: 'alice@example.com' }, 202)
      }
      return null
    })

    const { container } = await renderApp('/register')
    await waitForText(container, 'By creating an account, you agree to the')
    expect(container.textContent).toContain('Sign up for Nipa')
    expect(container.querySelector('input[type="checkbox"]')).toBeNull()
    const links = Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'))
    expect(links).toEqual(expect.arrayContaining(['/terms', '/privacy', '/guides', '/contact']))

    await act(async () => {
      findButtonIn(container, 'Continue with GitHub').click()
    })
    expect(vi.mocked(oauthStartURL)).toHaveBeenCalledWith('github', '/oauth/callback', true)

    const inputs = container.querySelectorAll('input')
    await act(async () => {
      setInputValue(inputs[0] as HTMLInputElement, 'Alice')
      setInputValue(inputs[1] as HTMLInputElement, 'alice@example.com')
    })
    await act(async () => {
      findButtonIn(container, 'Create account').click()
    })
    await waitForText(container, 'confirmation link')
    expect(calls[0]).toEqual({ name: 'Alice', email: 'alice@example.com', accept_terms: true })
  })

  it('shows the legal footer on the login page only in SaaS mode', async () => {
    stubAnonymous()
    const { container, root } = await renderApp('/login')
    await waitForText(container, 'Create an account')
    expect(container.textContent).toContain('Sign in to Nipa')
    expect(container.textContent).toContain('New to Nipa?')
    const footer = container.querySelector('nav[aria-label="Legal and help"]') as HTMLElement
    expect(Array.from(footer.querySelectorAll('a')).map((a) => a.textContent)).toEqual([
      'Terms',
      'Privacy',
      'Docs',
      'Contact',
    ])
    act(() => root.unmount())
    container.remove()

    stubAnonymous((url) => (url.includes('/api/v1/auth/capabilities') ? jsonResponse({ error: 'not found' }, 404) : null))
    const selfHosted = await renderApp('/login')
    await waitForText(selfHosted.container, 'Sign in to Nipa')
    expect(selfHosted.container.querySelector('nav[aria-label="Legal and help"]')).toBeNull()
    act(() => selfHosted.root.unmount())
    selfHosted.container.remove()
  })

  it('asks a signed-in user who has not accepted the terms', async () => {
    const accepted: unknown[] = []
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/me/terms') && method === 'GET') {
        return jsonResponse({ version: '2026-11-10', required: true })
      }
      if (url.endsWith('/api/v1/me/terms') && method === 'POST') {
        accepted.push(JSON.parse(String(init?.body)))
        return jsonResponse({ version: '2026-11-10', accepted_version: '2026-11-10', required: false })
      }
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Review our terms')
    expect(container.textContent).not.toContain('Name your workspace')
    expect(findButtonIn(container, 'Accept and continue').disabled).toBe(true)

    await act(async () => {
      ;(container.querySelector('input[type="checkbox"]') as HTMLInputElement).click()
    })
    await act(async () => {
      findButtonIn(container, 'Accept and continue').click()
    })
    await waitForText(container, 'Name your workspace')
    expect(accepted).toEqual([{ version: '2026-11-10' }])
  })

  it('shows an error when saving the acceptance fails', async () => {
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/me/terms') && method === 'GET') {
        return jsonResponse({ version: '2026-11-10', required: true })
      }
      if (url.endsWith('/api/v1/me/terms') && method === 'POST') {
        return jsonResponse({ error: 'the terms were updated; reload the page' }, 409)
      }
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Review our terms')
    await act(async () => {
      ;(container.querySelector('input[type="checkbox"]') as HTMLInputElement).click()
    })
    await act(async () => {
      findButtonIn(container, 'Accept and continue').click()
    })
    await waitForText(container, 'Could not save your acceptance')
    expect(container.textContent).not.toContain('Name your workspace')
  })

  it('keeps the app blocked when the terms status cannot be loaded', async () => {
    let failing = true
    stubRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/v1/me/terms') && method === 'GET') {
        return failing
          ? jsonResponse({ error: 'unavailable' }, 500)
          : jsonResponse({ version: '2026-11-10', required: true })
      }
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Could not check the terms you accepted')
    expect(container.textContent).not.toContain('Name your workspace')

    failing = false
    await act(async () => {
      findButtonIn(container, 'Retry').click()
    })
    await waitForText(container, 'Review our terms')
    expect(container.textContent).not.toContain('Name your workspace')
  })

  it('lets users who already accepted straight through', async () => {
    stubRoutes((url) => {
      if (url.endsWith('/api/v1/me/terms')) {
        return jsonResponse({ version: '2026-11-10', accepted_version: '2026-11-10', required: false })
      }
      return null
    })

    const { container } = await renderApp('/onboarding')
    await waitForText(container, 'Name your workspace')
    expect(container.textContent).not.toContain('Review our terms')
  })
})
