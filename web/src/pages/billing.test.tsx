import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { clearSession } from '../api/client'
import { billingTiming } from '../components/BillingPlan'

const paddle = vi.hoisted(() => ({
  listeners: new Set<(event: { name?: string }) => void>(),
  open: vi.fn(),
  load: vi.fn(),
  preview: vi.fn(),
}))

vi.mock('../billing/paddle', () => ({
  loadPaddle: paddle.load,
  previewPrices: paddle.preview,
  subscribePaddleEvents: (listener: (event: { name?: string }) => void) => {
    paddle.listeners.add(listener)
    return () => paddle.listeners.delete(listener)
  },
}))

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

async function waitUntil(predicate: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for the UI to settle')
}

async function click(element: Element | null | undefined) {
  if (!element) throw new Error('click target not found')
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
  })
}

function button(text: string): HTMLButtonElement | null {
  return (
    Array.from(document.querySelectorAll('button')).find(
      (item) =>
        item.textContent?.trim() === text || item.querySelector('[data-component="text"]')?.textContent === text,
    ) ?? null
  )
}

function iconButton(label: string): HTMLButtonElement | null {
  return (
    Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find(
      (item) =>
        item.getAttribute('aria-label') === label ||
        document.getElementById(item.getAttribute('aria-labelledby') ?? '')?.textContent === label,
    ) ?? null
  )
}

function disabled(element: HTMLButtonElement | null): boolean | undefined {
  return element ? element.disabled || element.getAttribute('aria-disabled') === 'true' : undefined
}

function input(label: string): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)
}

async function typeValue(element: HTMLInputElement | null, value: string) {
  if (!element) throw new Error('input not found')
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
  })
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

const CAPABILITIES = {
  registration: true,
  providers: [],
  billing: {
    provider: 'paddle',
    environment: 'sandbox',
    client_token: 'test_token',
    prices: {
      seat: { monthly: 'pri_seat_m', yearly: 'pri_seat_y' },
      storage: { monthly: 'pri_storage_m', yearly: 'pri_storage_y' },
    },
  },
}

const FREE = {
  ...BILLING,
  quota_bytes: 1024 * 1024,
  plan: 'free',
  status: '',
  interval: '',
  seats: 0,
  seats_used: 2,
  seat_limit: 3,
  storage_packs: 0,
  current_period_end: null,
  cancel_at_period_end: false,
}

const TEAM = {
  ...FREE,
  plan: 'team',
  status: 'active',
  interval: 'month',
  seats: 4,
  seat_limit: 7,
  storage_packs: 1,
  current_period_end: '2026-12-01T00:00:00Z',
}

interface Call {
  method: string
  url: string
  body: unknown
}

type Handler = (call: Call) => Response | undefined

function stubServer(options: { capabilities?: unknown; billing: () => unknown; handle?: Handler }) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input).split('?')[0]
      const call = { method: init?.method ?? 'GET', url, body: init?.body ? JSON.parse(String(init.body)) : undefined }
      calls.push(call)
      if (url.endsWith('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.endsWith('/api/v1/me')) return jsonResponse(ME)
      if (url.endsWith('/api/v1/auth/capabilities')) {
        return options.capabilities ? jsonResponse(options.capabilities) : jsonResponse({ error: 'not found' }, 404)
      }
      const handled = options.handle?.(call)
      if (handled) return handled
      if (url.endsWith('/api/v1/billing/me')) {
        const body = options.billing()
        return body ? jsonResponse(body) : jsonResponse({ error: 'not found' }, 404)
      }
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
  return calls
}

let root: Root | null = null

async function renderBilling() {
  window.history.pushState({}, '', '/settings/billing')
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(<App />)
  })
}

const paddleInstance = { Checkout: { open: paddle.open } }

beforeEach(() => {
  clearSession()
  window.history.pushState({}, '', '/')
  paddle.listeners.clear()
  paddle.load.mockResolvedValue(paddleInstance)
  paddle.preview.mockResolvedValue({
    pri_seat_m: { unit: '$10.00', total: '$20.00' },
    pri_seat_y: { unit: '$100.00', total: '$300.00' },
    pri_storage_m: { unit: '$5.00', total: '$5.00' },
    pri_storage_y: { unit: '$50.00', total: '$50.00' },
  })
  billingTiming.pollMs = 10
  billingTiming.previewDebounceMs = 0
})

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = null
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  paddle.open.mockReset()
  paddle.load.mockReset()
  paddle.preview.mockReset()
  document.body.innerHTML = ''
})

describe('BillingPage', () => {
  it('shows the owner aggregate usage and per-org breakdown', async () => {
    stubServer({ billing: () => BILLING })
    await renderBilling()
    await waitForText('Organizations')

    expect(document.body.textContent).toContain('2 KiB used · unlimited quota')
    const row = Array.from(document.querySelectorAll('tr')).find((item) => item.textContent?.includes('Sticker'))
    expect(row?.textContent).toContain('2 KiB')
    expect(document.body.textContent).not.toContain('Set owner quota')
    const navLink = Array.from(document.querySelectorAll('a')).find((a) => a.textContent?.trim() === 'Billing')
    expect(navLink).toBeDefined()
  })

  it('keeps the nav link and explains when the ledger is disabled', async () => {
    stubServer({ billing: () => null })
    await renderBilling()
    await waitForText('Storage billing is not enabled on this server.')

    const navLink = Array.from(document.querySelectorAll('a')).find((a) => a.textContent?.trim() === 'Billing')
    expect(navLink).toBeDefined()
    expect(document.body.textContent).not.toContain('Billing owner')
  })

  it('hides every billing control when the server has no billing provider', async () => {
    stubServer({ capabilities: { registration: true, providers: [] }, billing: () => FREE })
    await renderBilling()
    await waitForText('Organizations')
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30))
    })

    expect(button('Upgrade')).toBeNull()
    expect(button('Manage billing')).toBeNull()
    expect(document.body.textContent).not.toContain('seats used')
    expect(document.body.textContent).toContain('Contact your administrator')
    expect(paddle.load).not.toHaveBeenCalled()
  })

  it('upgrades a free plan through the Paddle checkout and waits for the webhook', async () => {
    let current: unknown = FREE
    const calls = stubServer({
      capabilities: CAPABILITIES,
      billing: () => current,
      handle: (call) =>
        call.url.endsWith('/api/v1/billing/checkout') ? jsonResponse({ transaction_id: 'txn_1' }) : undefined,
    })
    await renderBilling()
    await waitForText('2 of 3 seats used')
    expect(document.body.textContent).not.toContain('Contact your administrator')
    expect(paddle.load).toHaveBeenCalledWith(CAPABILITIES.billing)

    expect(input('seats')?.value).toBe('0')
    expect(document.body.textContent).toContain('3 free seats included; at least 0 paid seats needed')
    expect(disabled(iconButton('Decrease seats'))).toBe(true)
    expect(disabled(button('Upgrade'))).toBe(true)
    await waitForText('$10.00 per seat / month')
    expect(document.body.textContent).not.toContain('per seat / month · ')
    expect(paddle.preview).toHaveBeenLastCalledWith(paddleInstance, [
      { priceId: 'pri_seat_m', quantity: 1 },
      { priceId: 'pri_storage_m', quantity: 1 },
    ])
    expect(document.body.textContent).toContain('$5.00 per pack / month')

    await typeValue(input('seats'), '2')
    await waitForText('$10.00 per seat / month · $20.00')
    expect(disabled(button('Upgrade'))).toBe(false)
    await typeValue(input('seats'), '-1')
    expect(input('seats')?.value).toBe('0')
    await typeValue(input('seats'), '7')
    expect(input('seats')?.value).toBe('7')
    await click(iconButton('Decrease seats'))
    await typeValue(input('seats'), '2')
    await click(iconButton('Increase seats'))
    await click(iconButton('Increase storage packs'))
    await click(button('Yearly'))
    await waitForText('$100.00 per seat / year · $300.00')
    expect(document.body.textContent).toContain('$50.00 per pack / year · $50.00')

    await click(button('Upgrade'))
    await waitUntil(() => paddle.open.mock.calls.length > 0)
    const checkout = calls.find((call) => call.url.endsWith('/api/v1/billing/checkout'))
    expect(checkout).toMatchObject({ method: 'POST', body: { seats: 3, storage_packs: 1, interval: 'year' } })
    expect(paddle.open).toHaveBeenCalledWith({ transactionId: 'txn_1' })

    await act(async () => paddle.listeners.forEach((listener) => listener({ name: 'checkout.completed' })))
    await waitForText('Confirming your subscription')
    current = { ...TEAM, seats: 3, storage_packs: 1, interval: 'year' }
    await waitForText('Renews on')
    expect(document.body.textContent).not.toContain('Confirming your subscription')
    expect(document.body.textContent).toContain('Yearly')
    expect(button('Update plan')).not.toBeNull()
  })

  it('gives up polling with a notice and re-enables checkout after the overlay closes', async () => {
    billingTiming.pollAttempts = 2
    stubServer({
      capabilities: CAPABILITIES,
      billing: () => FREE,
      handle: (call) =>
        call.url.endsWith('/api/v1/billing/checkout') ? jsonResponse({ transaction_id: 'txn_2' }) : undefined,
    })
    await renderBilling()
    await waitForText('2 of 3 seats used')
    await click(iconButton('Increase seats'))
    await click(button('Upgrade'))
    await waitUntil(() => paddle.open.mock.calls.length > 0)
    expect(disabled(button('Upgrade'))).toBe(true)
    await act(async () => paddle.listeners.forEach((listener) => listener({ name: 'checkout.closed' })))
    await waitUntil(() => disabled(button('Upgrade')) === false)

    await act(async () => paddle.listeners.forEach((listener) => listener({ name: 'checkout.completed' })))
    await waitForText('reload this page in a minute')
    billingTiming.pollAttempts = 15
  })

  it('shows checkout conflicts and Paddle load failures inline', async () => {
    let conflict = true
    stubServer({
      capabilities: CAPABILITIES,
      billing: () => FREE,
      handle: (call) => {
        if (!call.url.endsWith('/api/v1/billing/checkout')) return undefined
        return conflict
          ? jsonResponse({ error: 'a subscription is already active; change it instead of checking out again' }, 409)
          : jsonResponse({ transaction_id: 'txn_3' })
      },
    })
    paddle.preview.mockRejectedValue(new Error('price not found'))
    await renderBilling()
    await waitForText('Prices could not be loaded')

    await click(iconButton('Increase seats'))
    await click(button('Upgrade'))
    await waitForText('a subscription is already active')
    expect(document.body.textContent).toContain('Could not start checkout')

    conflict = false
    paddle.load.mockRejectedValue(new Error('Paddle.js could not be loaded'))
    await click(button('Upgrade'))
    await waitForText('Paddle.js could not be loaded')
    expect(paddle.open).not.toHaveBeenCalled()
  })

  it('updates a paid plan, shows 409s inline and opens the billing portal', async () => {
    let current: unknown = TEAM
    let reject = true
    const calls = stubServer({
      capabilities: CAPABILITIES,
      billing: () => current,
      handle: (call) => {
        if (call.url.endsWith('/api/v1/billing/subscription')) {
          if (reject) return jsonResponse({ error: '9 seats are in use and 3 are free; buy at least 6' }, 409)
          current = { ...TEAM, storage_packs: 2 }
          return jsonResponse({ ...TEAM, storage_packs: 2 })
        }
        if (call.url.endsWith('/api/v1/billing/portal')) return jsonResponse({ url: 'https://portal.example/session' })
        return undefined
      },
    })
    await renderBilling()
    await waitForText('Renews on')
    expect(document.body.textContent).toContain('Team')
    expect(document.body.textContent).toContain('Active')
    expect(document.body.textContent).toContain('Monthly')
    expect(input('seats')?.value).toBe('4')
    expect(input('storage packs')?.value).toBe('1')
    expect(disabled(button('Update plan'))).toBe(true)

    await click(iconButton('Increase storage packs'))
    expect(disabled(button('Update plan'))).toBe(false)
    await click(button('Update plan'))
    await waitForText('9 seats are in use')
    expect(document.body.textContent).toContain('Could not update the plan')
    const patch = calls.find((call) => call.url.endsWith('/api/v1/billing/subscription'))
    expect(patch).toMatchObject({ method: 'PATCH', body: { seats: 4, storage_packs: 2, interval: 'month' } })

    reject = false
    await click(button('Update plan'))
    await waitUntil(() => input('storage packs')?.value === '2' && disabled(button('Update plan')) === true)
    expect(document.body.textContent).not.toContain('9 seats are in use')

    const tab = { opener: {} as unknown, location: { href: '' }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
    await click(button('Manage billing'))
    await waitUntil(() => tab.location.href !== '')
    expect(open).toHaveBeenCalledWith('', '_blank')
    expect(tab.location.href).toBe('https://portal.example/session')
    expect(tab.opener).toBeNull()
  })

  it('starts a paid plan at its paid seats and lets free seats lower the minimum', async () => {
    const calls = stubServer({ capabilities: CAPABILITIES, billing: () => ({ ...TEAM, seats: 2, seats_used: 4, seat_limit: 5 }) })
    await renderBilling()
    await waitForText('Renews on')
    expect(input('seats')?.value).toBe('2')
    expect(document.body.textContent).toContain('3 free seats included; at least 1 paid seats needed')
    expect(disabled(button('Update plan'))).toBe(true)
    await click(iconButton('Decrease seats'))
    expect(input('seats')?.value).toBe('1')
    expect(disabled(iconButton('Decrease seats'))).toBe(true)
    expect(disabled(button('Update plan'))).toBe(false)
    expect(calls.some((call) => call.url.endsWith('/api/v1/billing/subscription'))).toBe(false)
  })

  it('refuses to save a paid plan without seats or storage packs', async () => {
    stubServer({ capabilities: CAPABILITIES, billing: () => ({ ...TEAM, seats: 1, storage_packs: 1, seats_used: 2, seat_limit: 4 }) })
    await renderBilling()
    await waitForText('Renews on')
    await click(iconButton('Decrease seats'))
    expect(disabled(button('Update plan'))).toBe(false)
    await click(iconButton('Decrease storage packs'))
    expect(input('seats')?.value).toBe('0')
    expect(input('storage packs')?.value).toBe('0')
    expect(disabled(button('Update plan'))).toBe(true)
  })

  it('requires paid seats for the users above the free seats', async () => {
    stubServer({ capabilities: CAPABILITIES, billing: () => ({ ...FREE, seats_used: 5 }) })
    await renderBilling()
    await waitForText('5 of 3 seats used')
    expect(input('seats')?.value).toBe('2')
    expect(document.body.textContent).toContain('3 free seats included; at least 2 paid seats needed')
    expect(disabled(iconButton('Decrease seats'))).toBe(true)
    expect(disabled(button('Upgrade'))).toBe(false)
  })

  it('checks out storage packs without paid seats', async () => {
    const calls = stubServer({
      capabilities: CAPABILITIES,
      billing: () => FREE,
      handle: (call) =>
        call.url.endsWith('/api/v1/billing/checkout') ? jsonResponse({ transaction_id: 'txn_packs' }) : undefined,
    })
    await renderBilling()
    await waitForText('2 of 3 seats used')
    await click(iconButton('Increase storage packs'))
    await click(button('Upgrade'))
    await waitUntil(() => paddle.open.mock.calls.length > 0)
    const checkout = calls.find((call) => call.url.endsWith('/api/v1/billing/checkout'))
    expect(checkout).toMatchObject({ method: 'POST', body: { seats: 0, storage_packs: 1, interval: 'month' } })
    expect(paddle.open).toHaveBeenCalledWith({ transactionId: 'txn_packs' })
  })

  it('reports a failed portal request and closes the blank tab', async () => {
    stubServer({
      capabilities: CAPABILITIES,
      billing: () => TEAM,
      handle: (call) =>
        call.url.endsWith('/api/v1/billing/portal') ? jsonResponse({ error: 'no billing account' }, 404) : undefined,
    })
    await renderBilling()
    await waitForText('Renews on')
    const tab = { opener: null, location: { href: '' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
    await click(button('Manage billing'))
    await waitForText('no billing account')
    expect(tab.close).toHaveBeenCalled()
  })

  it('warns about a past-due payment and a scheduled cancellation', async () => {
    stubServer({
      capabilities: CAPABILITIES,
      billing: () => ({ ...TEAM, status: 'past_due', cancel_at_period_end: true }),
    })
    await renderBilling()
    await waitForText('Payment past due')
    expect(document.body.textContent).toContain('Past due')
    expect(document.body.textContent).toContain('Cancels on')
    expect(document.body.textContent).not.toContain('Renews on')
  })
})
