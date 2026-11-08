import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ServiceTokenSettings } from './ServiceTokenSettings'
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

  it('creates a token and reveals it once', async () => {
    vi.mocked(endpoints.listServiceTokens).mockResolvedValueOnce([]).mockResolvedValue([token()])
    vi.mocked(endpoints.createServiceToken).mockResolvedValue(token({ token: 'nipa_st_secretvalue' }))

    const { container, root } = await render(<ServiceTokenSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('No service tokens') ?? false)

    click(button(container, 'Create token'))
    await waitFor(() => document.body.textContent?.includes('Manage webhooks') ?? false)
    const inputs = document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input:not([type="checkbox"])')
    type(inputs[0], 'bugbase')
    type(inputs[1], '2030-01-02')
    const boxes = document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input[type="checkbox"]')
    click(boxes[1])
    click(boxes[0])
    click(boxes[0])
    click(button(document.body, 'Create'))

    await waitFor(() => container.textContent?.includes('nipa_st_secretvalue') ?? false)
    expect(endpoints.createServiceToken).toHaveBeenCalledWith('acme', 'game', {
      name: 'bugbase',
      scopes: ['checks:write', 'read'],
      expires_at: '2030-01-02T00:00:00.000Z',
    })
    click(container.querySelector('button[class*="BannerDismiss"]'))
    await waitFor(() => !(container.textContent?.includes('nipa_st_secretvalue') ?? false))
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
