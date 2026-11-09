import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ServiceTokenSettings, endOfLocalDay, expiryDay } from './ServiceTokenSettings'
import type { ServiceTokenResponse } from '../../api/models'

vi.mock('../../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/endpoints')>()
  return {
    ...actual,
    listServiceTokens: vi.fn(async () => []),
    createServiceToken: vi.fn(),
    revokeServiceToken: vi.fn(),
  }
})

const endpoints = await import('../../api/endpoints')

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(node)
  })
  return { container, root }
}

function click(el: Element | null | undefined) {
  act(() => {
    ;(el as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

function type(el: Element | null | undefined, value: string) {
  act(() => {
    const target = el as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(target, value)
    target.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function waitFor(predicate: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for the UI to settle')
}

function button(container: HTMLElement, text: string): HTMLElement {
  const found = [...container.querySelectorAll('button')].find((el) => el.textContent?.trim() === text)
  if (!found) throw new Error(`button ${text} not found`)
  return found
}

function token(overrides: Partial<ServiceTokenResponse> = {}): ServiceTokenResponse {
  return {
    id: 't1',
    project_id: 'p1',
    name: 'bugbase',
    hint: 'nipa_st_abcd',
    scopes: ['read', 'checks:write'],
    created_by: 'u1',
    created_at: '2026-11-08T10:00:00Z',
    ...overrides,
  }
}

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('expiryDay', () => {
  const now = new Date(2030, 0, 30, 15, 0, 0)

  it('counts days from today in local time', () => {
    expect(expiryDay('7', '', now)).toBe('2030-02-06')
    expect(expiryDay('30', '', now)).toBe('2030-03-01')
    expect(expiryDay('unknown', '', now)).toBe('2030-03-01')
  })

  it('uses the custom day or none', () => {
    expect(expiryDay('custom', '2031-05-04', now)).toBe('2031-05-04')
    expect(expiryDay('custom', '', now)).toBe('')
    expect(expiryDay('never', '2031-05-04', now)).toBe('')
  })
})

describe('endOfLocalDay', () => {
  it('ends the picked day in local time', () => {
    const end = new Date(endOfLocalDay('2030-01-02'))
    expect([end.getFullYear(), end.getMonth(), end.getDate(), end.getHours(), end.getMinutes()]).toEqual([2030, 0, 2, 23, 59])
  })

  it('keeps today in the future', () => {
    const now = new Date()
    const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    expect(new Date(endOfLocalDay(today)).getTime()).toBeGreaterThan(now.getTime() - 1000)
  })
})

describe('ServiceTokenSettings', () => {
  it('lists tokens with their state', async () => {
    vi.mocked(endpoints.listServiceTokens).mockResolvedValue([
      token({ last_used_at: '2026-11-08T11:00:00Z', expires_at: '2099-01-01T00:00:00Z' }),
      token({ id: 't2', name: 'old', revoked_at: '2026-11-08T12:00:00Z' }),
      token({ id: 't3', name: 'stale', expires_at: '2020-01-01T00:00:00Z' }),
    ])
    const { container, root } = await render(<ServiceTokenSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('nipa_st_abcd') ?? false)
    expect(container.textContent).toContain('read, checks:write')
    expect(container.textContent).toContain('Active')
    expect(container.textContent).toContain('Revoked')
    expect(container.textContent).toContain('Expired')
    expect([...container.querySelectorAll('button')].filter((b) => b.textContent === 'Revoke')).toHaveLength(2)
    act(() => root.unmount())
  })

  async function openDialog() {
    const view = await render(<ServiceTokenSettings org="acme" project="game" />)
    await waitFor(() => view.container.textContent?.includes('No service tokens') ?? false)
    click(button(view.container, 'Create token'))
    await waitFor(() => document.body.textContent?.includes('Status checks') ?? false)
    return view
  }

  async function chooseExpiration(label: string) {
    click(document.body.querySelector('[aria-label="Token expiration"]'))
    await waitFor(() => document.querySelector('[role="menuitemradio"]') !== null)
    click([...document.querySelectorAll('[role="menuitemradio"]')].find((item) => item.textContent === label))
  }

  const nameInput = () => document.body.querySelector<HTMLInputElement>('[role="dialog"] input[placeholder^="e.g."]')
  const scopeBoxes = () => document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input[type="checkbox"]')
  const generate = () => button(document.body, 'Generate token') as HTMLButtonElement

  it('creates a token that expires in 30 days by default and reveals it once', async () => {
    vi.mocked(endpoints.listServiceTokens).mockResolvedValueOnce([]).mockResolvedValue([token()])
    vi.mocked(endpoints.createServiceToken).mockResolvedValue(token({ token: 'nipa_st_secretvalue' }))
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    const { container, root } = await openDialog()
    expect(document.body.textContent).toContain('The token will expire on')
    expect(generate().disabled).toBe(true)
    type(nameInput(), ' bugbase ')
    click(scopeBoxes()[0])
    expect(document.body.textContent).toContain('Choose at least one scope.')
    expect(generate().disabled).toBe(true)
    click(scopeBoxes()[1])
    click(scopeBoxes()[0])
    click(generate())

    await waitFor(() => container.textContent?.includes('nipa_st_secretvalue') ?? false)
    expect(container.textContent).toContain("You won't be able to see it again.")
    const day = expiryDay('30', '')
    expect(endpoints.createServiceToken).toHaveBeenCalledWith('acme', 'game', {
      name: 'bugbase',
      scopes: ['read', 'checks:write'],
      expires_at: endOfLocalDay(day),
    })

    const copyButton = [...container.querySelectorAll<HTMLElement>('[aria-labelledby]')].find((element) =>
      (element.getAttribute('aria-labelledby') ?? '').split(' ').some((id) => document.getElementById(id)?.textContent === 'Copy token'),
    )
    await act(async () => {
      copyButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(writeText).toHaveBeenCalledWith('nipa_st_secretvalue')
    expect(document.body.textContent).toContain('Copied')

    click(container.querySelector('button[class*="BannerDismiss"]'))
    await waitFor(() => !(container.textContent?.includes('nipa_st_secretvalue') ?? false))
    act(() => root.unmount())
  })

  it('creates a token with a custom expiration day', async () => {
    vi.mocked(endpoints.createServiceToken).mockResolvedValue(token())
    const { root } = await openDialog()
    type(nameInput(), 'ci')
    await chooseExpiration('Custom…')
    expect(document.body.textContent).toContain('Pick the last day the token works.')
    expect(generate().disabled).toBe(true)
    const dateInput = document.body.querySelector<HTMLInputElement>('[aria-label="Expiration date"]')
    type(dateInput, '2000-01-01')
    expect(generate().disabled).toBe(true)
    type(dateInput, '2099-01-02')
    expect(document.body.textContent).toContain('The token will expire on')
    click(generate())
    await waitFor(() => vi.mocked(endpoints.createServiceToken).mock.calls.length === 1)
    expect(endpoints.createServiceToken).toHaveBeenCalledWith('acme', 'game', {
      name: 'ci',
      scopes: ['read'],
      expires_at: new Date(2099, 0, 2, 23, 59, 59).toISOString(),
    })
    act(() => root.unmount())
  })

  it('creates a token without expiration after warning about it', async () => {
    vi.mocked(endpoints.createServiceToken).mockResolvedValue(token())
    const { root } = await openDialog()
    type(nameInput(), 'forever')
    await chooseExpiration('No expiration')
    expect(document.body.textContent).toContain('an expiration date is recommended')
    click(generate())
    await waitFor(() => vi.mocked(endpoints.createServiceToken).mock.calls.length === 1)
    expect(endpoints.createServiceToken).toHaveBeenCalledWith('acme', 'game', { name: 'forever', scopes: ['read'] })
    act(() => root.unmount())
  })

  it('revokes a token after confirmation', async () => {
    vi.mocked(endpoints.listServiceTokens).mockResolvedValue([token()])
    vi.mocked(endpoints.revokeServiceToken).mockResolvedValue(token({ revoked_at: '2026-11-08T12:00:00Z' }))

    const { container, root } = await render(<ServiceTokenSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('Revoke') ?? false)
    click(button(container, 'Revoke'))
    await waitFor(() => document.querySelector('[role="alertdialog"]') !== null)
    click(button(document.querySelector('[role="alertdialog"]') as HTMLElement, 'Revoke'))
    await waitFor(() => vi.mocked(endpoints.revokeServiceToken).mock.calls.length > 0)
    expect(endpoints.revokeServiceToken).toHaveBeenCalledWith('acme', 'game', 't1')
    act(() => root.unmount())
  })
})
