import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { WEBHOOK_EVENTS, WebhookSettings, isPayloadURL } from './WebhookSettings'
import type { WebhookDeliveryResponse, WebhookResponse } from '../../api/models'

vi.mock('../../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/endpoints')>()
  return {
    ...actual,
    listWebhooks: vi.fn(async () => []),
    createWebhook: vi.fn(),
    updateWebhook: vi.fn(),
    deleteWebhook: vi.fn(),
    rotateWebhookSecret: vi.fn(),
    testWebhook: vi.fn(),
    listWebhookDeliveries: vi.fn(async () => []),
    redeliverWebhookDelivery: vi.fn(),
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

async function confirmDialog(label: string) {
  await waitFor(() => document.querySelector('[role="alertdialog"]') !== null)
  const dialog = document.querySelector('[role="alertdialog"]') as HTMLElement
  click(button(dialog, label))
}

function hook(overrides: Partial<WebhookResponse> = {}): WebhookResponse {
  return {
    id: 'h1',
    project_id: 'p1',
    name: 'ci',
    url: 'https://ci.example.com/hooks/nipa',
    events: ['push', 'mr.merged'],
    path_prefix: '',
    is_active: true,
    insecure_tls: false,
    created_at: '2026-09-27T10:00:00Z',
    updated_at: '2026-09-27T10:00:00Z',
    ...overrides,
  }
}

function delivery(overrides: Partial<WebhookDeliveryResponse> = {}): WebhookDeliveryResponse {
  return {
    id: 'd1',
    webhook_id: 'h1',
    event_type: 'push',
    state: 'delivered',
    attempt: 1,
    response_status: 200,
    created_at: '2026-09-27T10:00:00Z',
    ...overrides,
  }
}

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('WebhookSettings', () => {
  it('lists webhooks and toggles the active flag', async () => {
    vi.mocked(endpoints.listWebhooks)
      .mockResolvedValueOnce([hook()])
      .mockResolvedValue([hook({ is_active: false })])
    vi.mocked(endpoints.updateWebhook).mockResolvedValue(hook({ is_active: false }))

    const { container, root } = await render(<WebhookSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('https://ci.example.com/hooks/nipa') ?? false)
    expect(container.textContent).toContain('Push, Merge request merged')

    const active = container.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(active.checked).toBe(true)
    click(active)
    await waitFor(() => vi.mocked(endpoints.updateWebhook).mock.calls.length > 0)
    expect(endpoints.updateWebhook).toHaveBeenCalledWith('acme', 'game', 'h1', { is_active: false })
    await waitFor(() => !(container.querySelector('input[type="checkbox"]') as HTMLInputElement).checked)
    act(() => root.unmount())
  })

  async function openDialog() {
    const view = await render(<WebhookSettings org="acme" project="game" />)
    await waitFor(() => view.container.textContent?.includes('No webhooks') ?? false)
    click(button(view.container, 'Create webhook'))
    await waitFor(() => document.body.textContent?.includes('Which events would you like') ?? false)
    return view
  }

  const dialog = () => document.body.querySelector('[role="dialog"]') as HTMLElement
  const urlInput = () => dialog().querySelector<HTMLInputElement>('input[type="url"]')
  const radio = (value: string) => dialog().querySelector<HTMLInputElement>(`input[type="radio"][value="${value}"]`)
  const submit = () => button(dialog(), 'Add webhook') as HTMLButtonElement

  it('validates the payload url', () => {
    expect(isPayloadURL('https://ci.example.com/hook')).toBe(true)
    expect(isPayloadURL(' http://10.0.0.1:8080/x ')).toBe(true)
    expect(isPayloadURL('ftp://ci.example.com')).toBe(false)
    expect(isPayloadURL('ci.example.com')).toBe(false)
  })

  it('creates a push webhook and reveals the secret once', async () => {
    vi.mocked(endpoints.listWebhooks)
      .mockResolvedValueOnce([])
      .mockResolvedValue([hook({ secret: undefined })])
    vi.mocked(endpoints.createWebhook).mockResolvedValue(hook({ secret: 'deadbeef' }))
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    const { container, root } = await openDialog()
    expect(submit().disabled).toBe(true)
    type(urlInput(), 'not a url')
    expect(document.body.textContent).toContain('Enter a full http:// or https:// URL.')
    expect(submit().disabled).toBe(true)
    type(urlInput(), 'https://ci.example.com/hooks/nipa')
    type(dialog().querySelector('input[placeholder="e.g. CI notifications"]'), ' ci ')
    type(dialog().querySelector('input[placeholder="e.g. assets"]'), 'art/')
    click(radio('disable'))
    click(submit())

    await waitFor(() => container.textContent?.includes('deadbeef') ?? false)
    expect(endpoints.createWebhook).toHaveBeenCalledWith('acme', 'game', {
      name: 'ci',
      url: 'https://ci.example.com/hooks/nipa',
      events: ['push'],
      path_prefix: 'art/',
      is_active: true,
      insecure_tls: true,
    })
    expect(container.textContent).toContain('Signing secret for ci')
    const copy = [...container.querySelectorAll<HTMLElement>('[aria-labelledby]')].find((element) =>
      (element.getAttribute('aria-labelledby') ?? '').split(' ').some((id) => document.getElementById(id)?.textContent === 'Copy secret'),
    )
    await act(async () => {
      copy?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(writeText).toHaveBeenCalledWith('deadbeef')

    click(container.querySelector('button[class*="BannerDismiss"]'))
    await waitFor(() => !(container.textContent?.includes('deadbeef') ?? false))
    act(() => root.unmount())
  })

  it('subscribes to everything or to picked events', async () => {
    vi.mocked(endpoints.createWebhook).mockResolvedValue(hook())
    const { root } = await openDialog()
    type(urlInput(), 'https://ci.example.com/all')
    click(radio('all'))
    expect(dialog().querySelector('input[placeholder="e.g. assets"]')).not.toBeNull()
    click(submit())
    await waitFor(() => vi.mocked(endpoints.createWebhook).mock.calls.length === 1)
    expect(vi.mocked(endpoints.createWebhook).mock.calls[0][2].events).toHaveLength(WEBHOOK_EVENTS.length)
    act(() => root.unmount())
    document.body.innerHTML = ''

    const second = await openDialog()
    type(urlInput(), 'https://ci.example.com/mr')
    type(dialog().querySelector('input[placeholder="e.g. assets"]'), 'docs')
    click(radio('custom'))
    expect(document.body.textContent).toContain('Merge requests')
    const box = (value: string) => dialog().querySelector<HTMLInputElement>(`input[type="checkbox"][value="${value}"]`)
    click(box('push'))
    expect(document.body.textContent).toContain('Choose at least one event.')
    expect(submit().disabled).toBe(true)
    expect(dialog().querySelector('input[placeholder="e.g. assets"]')).toBeNull()
    click(box('mr.merged'))
    click(box('mr.created'))
    const activeLabel = [...dialog().querySelectorAll('label')].find((label) => label.textContent === 'Active') as HTMLLabelElement
    click(document.getElementById(activeLabel.htmlFor))
    click(submit())
    await waitFor(() => vi.mocked(endpoints.createWebhook).mock.calls.length === 2)
    expect(vi.mocked(endpoints.createWebhook).mock.calls[1][2]).toEqual({
      name: '',
      url: 'https://ci.example.com/mr',
      events: ['mr.created', 'mr.merged'],
      path_prefix: '',
      is_active: false,
      insecure_tls: false,
    })
    act(() => second.root.unmount())
  })

  it('shows deliveries and redelivers one', async () => {
    vi.mocked(endpoints.listWebhooks).mockResolvedValue([hook()])
    vi.mocked(endpoints.listWebhookDeliveries).mockResolvedValue([
      delivery(),
      delivery({ id: 'd2', state: 'failed', attempt: 5, response_status: 500, last_error: 'receiver exploded' }),
    ])
    vi.mocked(endpoints.redeliverWebhookDelivery).mockResolvedValue(delivery())

    const { container, root } = await render(<WebhookSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('Deliveries') ?? false)

    click(button(container, 'Deliveries'))
    await waitFor(() => container.textContent?.includes('receiver exploded') ?? false)
    expect(container.textContent).toContain('Delivered')
    expect(container.textContent).toContain('Failed')

    const failedRow = [...container.querySelectorAll('tr')].find(
      (row) => row.textContent?.includes('receiver exploded') && !row.querySelector('tr'),
    )
    const redeliver = failedRow?.querySelector('button')
    click(redeliver)
    await waitFor(() => vi.mocked(endpoints.redeliverWebhookDelivery).mock.calls.length > 0)
    expect(endpoints.redeliverWebhookDelivery).toHaveBeenCalledWith('acme', 'game', 'h1', 'd2')
    act(() => root.unmount())
  })

  it('sends a test delivery and expands the log', async () => {
    vi.mocked(endpoints.listWebhooks).mockResolvedValue([hook()])
    vi.mocked(endpoints.testWebhook).mockResolvedValue(delivery({ id: 'd9', state: 'pending', response_status: undefined }))
    vi.mocked(endpoints.listWebhookDeliveries).mockResolvedValue([
      delivery({ id: 'd9', state: 'pending', response_status: undefined }),
    ])

    const { container, root } = await render(<WebhookSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('Send test') ?? false)

    click(button(container, 'Send test'))
    await waitFor(() => container.textContent?.includes('Pending') ?? false)
    expect(endpoints.testWebhook).toHaveBeenCalledWith('acme', 'game', 'h1')
    expect(endpoints.listWebhookDeliveries).toHaveBeenCalledWith('acme', 'game', 'h1')
    act(() => root.unmount())
  })

  it('rotates the secret and deletes the webhook', async () => {
    vi.mocked(endpoints.listWebhooks).mockResolvedValueOnce([hook()]).mockResolvedValue([])
    vi.mocked(endpoints.rotateWebhookSecret).mockResolvedValue(hook({ secret: 'rotated-secret' }))
    vi.mocked(endpoints.deleteWebhook).mockResolvedValue({ message: 'webhook deleted' })

    const { container, root } = await render(<WebhookSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('Rotate secret') ?? false)

    click(button(container, 'Rotate secret'))
    await confirmDialog('Rotate secret')
    await waitFor(() => container.textContent?.includes('rotated-secret') ?? false)
    expect(endpoints.rotateWebhookSecret).toHaveBeenCalledWith('acme', 'game', 'h1')

    click(button(container, 'Delete'))
    await confirmDialog('Delete')
    await waitFor(() => vi.mocked(endpoints.deleteWebhook).mock.calls.length > 0)
    expect(endpoints.deleteWebhook).toHaveBeenCalledWith('acme', 'game', 'h1')
    await waitFor(() => container.textContent?.includes('No webhooks') ?? false)
    act(() => root.unmount())
  })
})
