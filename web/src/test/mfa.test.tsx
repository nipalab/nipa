import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { clearSession, getAccessToken, startSession } from '../api/client'
import { recoveryCodesText } from '../components/RecoveryCodes'
import { normalizeCode } from '../components/MFACodeStep'

vi.mock('qrcode', () => ({
  default: { toDataURL: vi.fn(async (uri: string) => `data:image/png;base64,${btoa(uri)}`) },
}))

type Handler = (url: string, method: string, body: Record<string, unknown> | undefined) => Response | null

interface Call {
  url: string
  method: string
  body: Record<string, unknown> | undefined
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

const TOKEN = { access_token: 'token', token_type: 'Bearer', expires_in: 1800 }
const CHALLENGE = { mfa_required: true, mfa_challenge: 'chal-1', expires_in: 300 }
const CODES = ['AAAAA-BBBBB', 'CCCCC-DDDDD', 'EEEEE-FFFFF']

const ME = {
  id: '1',
  name: 'Alice',
  email: 'alice@example.com',
  photo_url: '',
  is_admin: true,
  is_super_admin: false,
  notify_email: true,
  deleted: false,
}

let calls: Call[] = []
let mounted: Root | null = null

function stub(handler: Handler, { signedIn = true } = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined
      calls.push({ url, method, body })
      const custom = handler(url, method, body)
      if (custom) return custom
      if (url.includes('/auth/refresh')) {
        return signedIn ? jsonResponse(TOKEN) : jsonResponse({ error: 'refresh token required' }, 401)
      }
      if (url.endsWith('/api/v1/me')) return jsonResponse(ME)
      if (url.includes('/api/v1/orgs')) return jsonResponse([])
      if (url.includes('/auth/capabilities')) return jsonResponse({ registration: false, providers: [] })
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

async function waitFor(condition: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (condition()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error(`timed out; page: ${document.body.textContent}`)
}

const text = () => document.body.textContent ?? ''

async function waitForText(value: string) {
  await waitFor(() => text().includes(value))
}

function button(label: string): HTMLButtonElement {
  const found = Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === label)
  if (!found) throw new Error(`button ${label} not found`)
  return found as HTMLButtonElement
}

function maybeButton(label: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === label)
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.click()
  })
}

async function setValue(input: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function inputByLabel(label: string): HTMLInputElement {
  const found = Array.from(document.querySelectorAll('label')).find((item) => item.textContent?.trim().startsWith(label))
  const id = found?.getAttribute('for')
  const input = id ? (document.getElementById(id) as HTMLInputElement | null) : null
  if (!input) throw new Error(`input ${label} not found`)
  return input
}

async function renderApp(url: string) {
  window.history.pushState({}, '', url)
  const container = document.createElement('div')
  document.body.appendChild(container)
  mounted = createRoot(container)
  await act(async () => {
    mounted?.render(<App />)
  })
}

async function signInWithPassword() {
  await waitFor(() => document.querySelectorAll('input').length >= 2)
  const inputs = document.querySelectorAll('input')
  await setValue(inputs[0] as HTMLInputElement, 'alice@example.com')
  await setValue(inputs[1] as HTMLInputElement, 'secret')
  await click(button('Sign in'))
}

beforeEach(() => {
  clearSession()
  calls = []
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  if (mounted) act(() => mounted?.unmount())
  mounted = null
  document.body.innerHTML = ''
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('MFA helpers', () => {
  it('normalizes codes and formats the recovery file', () => {
    expect(normalizeCode(' 123 456 ')).toBe('123456')
    expect(recoveryCodesText(['A', 'B'], 'a@example.com')).toBe(
      'Nipa recovery codes for a@example.com\nEach code signs you in once when you cannot use your authenticator app.\n\nA\nB\n',
    )
    expect(recoveryCodesText(['A'])).toContain('Nipa recovery codes\n')
  })

  it('startSession returns the challenge without installing a session', async () => {
    stub((url) => (url.includes('/auth/login') ? jsonResponse(CHALLENGE) : null))
    expect(await startSession('/api/v1/auth/login', {})).toEqual(CHALLENGE)
    expect(getAccessToken()).toBeNull()
  })
})

describe('login code step', () => {
  it('asks for the code after the password and signs in', async () => {
    stub(
      (url, method) => {
        if (url.endsWith('/auth/login')) return jsonResponse(CHALLENGE)
        if (url.endsWith('/auth/mfa/verify') && method === 'POST') return jsonResponse(TOKEN)
        return null
      },
      { signedIn: false },
    )
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    expect(getAccessToken()).toBeNull()
    await setValue(inputByLabel('Authentication code'), '123 456')
    await click(button('Verify'))
    await waitFor(() => window.location.pathname === '/dashboard')
    const verify = calls.find((call) => call.url.endsWith('/auth/mfa/verify'))
    expect(verify?.body).toEqual({ challenge: 'chal-1', code: '123456' })
    expect(getAccessToken()).toBe('token')
  })

  it('shows an invalid code and lets the user retry', async () => {
    let attempts = 0
    stub(
      (url) => {
        if (url.endsWith('/auth/login')) return jsonResponse(CHALLENGE)
        if (url.endsWith('/auth/mfa/verify')) {
          attempts += 1
          return attempts === 1 ? jsonResponse({ error: 'invalid code' }, 400) : jsonResponse(TOKEN)
        }
        return null
      },
      { signedIn: false },
    )
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    await setValue(inputByLabel('Authentication code'), '000000')
    await click(button('Verify'))
    await waitForText('Invalid code. Try again.')
    await setValue(inputByLabel('Authentication code'), 'AAAAA-BBBBB')
    await click(button('Verify'))
    await waitFor(() => window.location.pathname === '/dashboard')
  })

  it('shows other errors verbatim', async () => {
    stub(
      (url) => {
        if (url.endsWith('/auth/login')) return jsonResponse(CHALLENGE)
        if (url.endsWith('/auth/mfa/verify')) return jsonResponse({ error: 'rate limit exceeded' }, 429)
        return null
      },
      { signedIn: false },
    )
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    await setValue(inputByLabel('Authentication code'), '000000')
    await click(button('Verify'))
    await waitForText('rate limit exceeded')
  })

  it('returns to the password step after five wrong codes', async () => {
    stub(
      (url) => {
        if (url.endsWith('/auth/login')) return jsonResponse(CHALLENGE)
        if (url.endsWith('/auth/mfa/verify')) return jsonResponse({ error: 'invalid code' }, 400)
        return null
      },
      { signedIn: false },
    )
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    const verifies = () => calls.filter((call) => call.url.endsWith('/auth/mfa/verify')).length
    for (let i = 1; i <= 5; i++) {
      await setValue(inputByLabel('Authentication code'), '000000')
      await click(button('Verify'))
      await waitFor(() => verifies() === i && (i === 5 || text().includes('Invalid code')))
    }
    await waitForText('Sign in again')
    expect(text()).toContain('too many wrong codes')
    expect(maybeButton('Sign in')).toBeDefined()
  })

  it('returns to the password step when the challenge expired', async () => {
    stub(
      (url) => (url.endsWith('/auth/login') ? jsonResponse({ ...CHALLENGE, expires_in: 0 }) : null),
      { signedIn: false },
    )
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    await setValue(inputByLabel('Authentication code'), '123456')
    await click(button('Verify'))
    await waitForText('Sign in again')
    expect(calls.some((call) => call.url.endsWith('/auth/mfa/verify'))).toBe(false)
  })

  it('goes back to the password step on request', async () => {
    stub((url) => (url.endsWith('/auth/login') ? jsonResponse(CHALLENGE) : null), { signedIn: false })
    await renderApp('/login')
    await signInWithPassword()
    await waitForText('Authentication code')
    await click(button('Back to sign in'))
    await waitFor(() => maybeButton('Sign in') !== undefined)
    expect(text()).not.toContain('Sign in again')
  })
})

describe('OAuth callback code step', () => {
  it('asks for the code when complete answers mfa_required', async () => {
    stub(
      (url) => {
        if (url.endsWith('/auth/oauth/complete')) return jsonResponse(CHALLENGE)
        if (url.endsWith('/auth/mfa/verify')) return jsonResponse(TOKEN)
        return null
      },
      { signedIn: false },
    )
    await renderApp('/oauth/callback?code=xyz&from=%2Facme%2Fproj')
    await waitForText('Authentication code')
    await setValue(inputByLabel('Authentication code'), '123456')
    await click(button('Verify'))
    await waitFor(() => window.location.pathname === '/acme/proj')
  })

  it('explains an expired challenge', async () => {
    stub(
      (url) => (url.endsWith('/auth/oauth/complete') ? jsonResponse({ ...CHALLENGE, expires_in: 0 }) : null),
      { signedIn: false },
    )
    await renderApp('/oauth/callback?code=xyz')
    await waitForText('Authentication code')
    await setValue(inputByLabel('Authentication code'), '123456')
    await click(button('Verify'))
    await waitForText('Sign-in failed')
    expect(text()).toContain('Sign in again')
  })

  it('goes back to the login page on request', async () => {
    stub((url) => (url.endsWith('/auth/oauth/complete') ? jsonResponse(CHALLENGE) : null), { signedIn: false })
    await renderApp('/oauth/callback?code=xyz')
    await waitForText('Authentication code')
    await click(button('Back to sign in'))
    await waitFor(() => window.location.pathname === '/login')
  })
})

describe('Security tab', () => {
  const disabled = { available: true, totp: { enabled: false }, recovery_codes_left: 0 }
  const enabled = {
    available: true,
    totp: { enabled: true, confirmed_at: '2026-11-01T10:00:00Z' },
    recovery_codes_left: 2,
  }

  it('is listed in the account settings navigation', async () => {
    stub((url) => (url.endsWith('/me/mfa') ? jsonResponse(disabled) : null))
    await renderApp('/settings/profile')
    await waitForText('Security')
    const link = Array.from(document.querySelectorAll('a')).find((item) => item.textContent?.includes('Security'))
    expect(link?.getAttribute('href')).toBe('/settings/profile?tab=security')
  })

  it('shows a note when two-factor authentication is unavailable', async () => {
    stub((url) => (url.endsWith('/me/mfa') ? jsonResponse({ ...disabled, available: false }) : null))
    await renderApp('/settings/profile?tab=security')
    await waitForText('not available on this server')
    expect(maybeButton('Enable two-factor authentication')).toBeUndefined()
  })

  it('shows a loading error', async () => {
    stub((url) => (url.endsWith('/me/mfa') ? jsonResponse({ error: 'boom' }, 500) : null))
    await renderApp('/settings/profile?tab=security')
    await waitForText('boom')
  })

  it('enrolls with the password, a code and the saved recovery codes', async () => {
    let status: object = disabled
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    const createObjectURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:codes')
    const anchorClick = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    stub((url, method) => {
      if (url.endsWith('/me/mfa') && method === 'GET') return jsonResponse(status)
      if (url.endsWith('/me/mfa/totp') && method === 'POST') {
        return jsonResponse({ secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Nipa:alice?secret=ABCDEFGHIJKLMNOP' })
      }
      if (url.endsWith('/me/mfa/totp/confirm')) {
        status = enabled
        return jsonResponse({ recovery_codes: CODES })
      }
      return null
    })
    await renderApp('/settings/profile?tab=security')
    await waitForText('Enable two-factor authentication')
    await click(button('Enable two-factor authentication'))
    await setValue(inputByLabel('Current password'), 'secret')
    await click(button('Continue'))
    await waitForText('Scan this QR code')
    await waitFor(() => document.querySelector('img[alt="QR code for your authenticator app"]') !== null)
    expect(document.querySelector('img[alt="QR code for your authenticator app"]')?.getAttribute('src')).toBe(
      `data:image/png;base64,${btoa('otpauth://totp/Nipa:alice?secret=ABCDEFGHIJKLMNOP')}`,
    )
    expect(text()).toContain('ABCD EFGH IJKL MNOP')
    await setValue(inputByLabel('Authentication code'), '123456')
    await click(button('Verify and enable'))
    await waitForText('Save your recovery codes')
    expect(text()).toContain('CCCCC-DDDDD')

    expect(button('Done').disabled).toBe(true)
    await click(button('Copy'))
    expect(writeText).toHaveBeenCalledWith(CODES.join('\n'))
    await click(button('Download .txt'))
    expect(createObjectURL).toHaveBeenCalled()
    expect(anchorClick).toHaveBeenCalled()

    const saved = inputByLabel('I saved these codes')
    await click(saved)
    expect(button('Done').disabled).toBe(false)
    await click(button('Done'))
    await waitForText('2 recovery codes left')
    expect(calls.find((call) => call.url.endsWith('/me/mfa/totp') && call.method === 'POST')?.body).toEqual({
      password: 'secret',
    })
    expect(calls.find((call) => call.url.endsWith('/totp/confirm'))?.body).toEqual({ code: '123456' })
  })

  it('shows enrollment errors', async () => {
    stub((url, method) => {
      if (url.endsWith('/me/mfa') && method === 'GET') return jsonResponse(disabled)
      if (url.endsWith('/me/mfa/totp') && method === 'POST') {
        return jsonResponse({ error: 'current password is incorrect' }, 400)
      }
      return null
    })
    await renderApp('/settings/profile?tab=security')
    await waitForText('Enable two-factor authentication')
    await click(button('Enable two-factor authentication'))
    await click(button('Continue'))
    await waitForText('current password is incorrect')
    await click(button('Cancel'))
    await waitFor(() => maybeButton('Continue') === undefined)
  })

  it('regenerates recovery codes behind the saved checkbox', async () => {
    stub((url, method) => {
      if (url.endsWith('/me/mfa') && method === 'GET') return jsonResponse(enabled)
      if (url.endsWith('/me/mfa/recovery-codes')) return jsonResponse({ recovery_codes: CODES })
      return null
    })
    await renderApp('/settings/profile?tab=security')
    await waitForText('2 recovery codes left')
    expect(text()).toContain('Enabled')
    await click(button('Regenerate recovery codes'))
    await setValue(inputByLabel('Authentication code'), '654321')
    await click(button('Regenerate'))
    await waitForText('EEEEE-FFFFF')
    expect(button('Done').disabled).toBe(true)
    await click(inputByLabel('I saved these codes'))
    await click(button('Done'))
    await waitFor(() => maybeButton('Done') === undefined)
    expect(calls.find((call) => call.url.endsWith('/recovery-codes'))?.body).toEqual({ code: '654321' })
  })

  it('disables with a code', async () => {
    let status: object = enabled
    stub((url, method) => {
      if (url.endsWith('/me/mfa') && method === 'GET') return jsonResponse(status)
      if (url.endsWith('/me/mfa/totp') && method === 'DELETE') {
        status = disabled
        return jsonResponse({ message: 'disabled' })
      }
      return null
    })
    await renderApp('/settings/profile?tab=security')
    await waitForText('2 recovery codes left')
    await click(button('Disable'))
    await setValue(inputByLabel('Authentication code'), 'AAAAA-BBBBB')
    const dialogDisable = Array.from(document.querySelectorAll('button')).filter(
      (item) => item.textContent?.trim() === 'Disable',
    )
    await click(dialogDisable[dialogDisable.length - 1])
    await waitForText('Two-factor authentication is off')
    expect(calls.find((call) => call.url.endsWith('/me/mfa/totp') && call.method === 'DELETE')?.body).toEqual({
      code: 'AAAAA-BBBBB',
    })
  })
})

describe('admin two-factor reset', () => {
  const users = [
    { ...ME, id: '1', name: 'Alice', email: 'alice@example.com' },
    { ...ME, id: '2', name: 'Bob', email: 'bob@example.com', is_admin: false },
    { ...ME, id: '3', name: 'Carol', email: 'carol@example.com', is_admin: true },
  ]

  async function openMenu(name: string) {
    await waitForText(name)
    const label = `Manage ${name}`
    const trigger = [...document.querySelectorAll<HTMLElement>('[aria-label], [aria-labelledby]')].find(
      (element) =>
        element.getAttribute('aria-label') === label ||
        (element.getAttribute('aria-labelledby') ?? '')
          .split(' ')
          .some((id) => document.getElementById(id)?.textContent === label),
    ) as HTMLElement
    await click(trigger)
    await waitFor(() => document.querySelectorAll('[role="menuitem"]').length > 0)
  }

  function menuItem(label: string): HTMLElement | undefined {
    return Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) =>
      item.textContent?.includes(label),
    )
  }

  async function confirmReset() {
    await click(menuItem('Reset two-factor') as HTMLElement)
    await waitForText('Reset two-factor authentication for')
    await click(button('Reset two-factor'))
  }

  it('resets a member after confirmation', async () => {
    stub((url, method) => {
      if (url.endsWith('/api/v1/users') && method === 'GET') return jsonResponse(users)
      if (url.endsWith('/users/2/mfa') && method === 'DELETE') return jsonResponse({ message: 'reset' })
      return null
    })
    await renderApp('/admin/users')
    await openMenu('Bob')
    await confirmReset()
    await waitForText('Two-factor authentication was reset for Bob.')
    expect(calls.some((call) => call.url.endsWith('/users/2/mfa') && call.method === 'DELETE')).toBe(true)
  })

  it('shows the server error', async () => {
    stub((url, method) => {
      if (url.endsWith('/api/v1/users') && method === 'GET') return jsonResponse(users)
      if (url.endsWith('/users/2/mfa')) return jsonResponse({ error: 'two-factor authentication is not enabled' }, 400)
      return null
    })
    await renderApp('/admin/users')
    await openMenu('Bob')
    await confirmReset()
    await waitForText('two-factor authentication is not enabled')
  })

  it('hides the action for other admins unless the viewer is a super admin', async () => {
    stub((url, method) => (url.endsWith('/api/v1/users') && method === 'GET' ? jsonResponse(users) : null))
    await renderApp('/admin/users')
    await openMenu('Carol')
    expect(menuItem('Reset password')).toBeDefined()
    expect(menuItem('Reset two-factor')).toBeUndefined()
  })
})
