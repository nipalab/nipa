import { afterEach, describe, expect, it, vi } from 'vitest'
import type { BillingCapabilities } from '../api/models'
import { loadPaddle, PADDLE_SCRIPT_URL, previewPrices, resetPaddleForTests, subscribePaddleEvents, type Paddle } from './paddle'

const CONFIG: BillingCapabilities = {
  provider: 'paddle',
  environment: 'sandbox',
  client_token: 'test_token',
  prices: { seat: { monthly: 'pri_a', yearly: 'pri_b' }, storage: { monthly: 'pri_c', yearly: 'pri_d' } },
}

function fakePaddle() {
  return {
    Environment: { set: vi.fn() },
    Initialize: vi.fn(),
    Checkout: { open: vi.fn() },
    PricePreview: vi.fn(),
  }
}

function script(): HTMLScriptElement | null {
  return document.head.querySelector<HTMLScriptElement>(`script[src="${PADDLE_SCRIPT_URL}"]`)
}

afterEach(() => {
  resetPaddleForTests()
  delete window.Paddle
  document.head.innerHTML = ''
})

describe('loadPaddle', () => {
  it('injects Paddle.js once, selects the sandbox and fans out events', async () => {
    const fake = fakePaddle()
    const first = loadPaddle(CONFIG)
    const second = loadPaddle(CONFIG)
    expect(first).toBe(second)
    expect(document.head.querySelectorAll('script')).toHaveLength(1)
    window.Paddle = fake as unknown as Paddle
    script()?.dispatchEvent(new Event('load'))
    expect(await first).toBe(fake)

    expect(fake.Environment.set).toHaveBeenCalledWith('sandbox')
    expect(fake.Environment.set.mock.invocationCallOrder[0]).toBeLessThan(fake.Initialize.mock.invocationCallOrder[0])
    const options = fake.Initialize.mock.calls[0][0]
    expect(options.token).toBe('test_token')
    expect(options.checkout.settings).toEqual({ displayMode: 'overlay', theme: 'dark' })

    const listener = vi.fn()
    const unsubscribe = subscribePaddleEvents(listener)
    options.eventCallback({ name: 'checkout.completed' })
    expect(listener).toHaveBeenCalledWith({ name: 'checkout.completed' })
    unsubscribe()
    options.eventCallback({ name: 'checkout.closed' })
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('keeps production as the default and reuses an already loaded Paddle', async () => {
    const fake = fakePaddle()
    window.Paddle = fake as unknown as Paddle
    await loadPaddle({ ...CONFIG, environment: 'production' })
    expect(fake.Environment.set).not.toHaveBeenCalled()
    expect(script()).toBeNull()
  })

  it('retries after the script fails to load', async () => {
    const failed = loadPaddle(CONFIG)
    script()?.dispatchEvent(new Event('error'))
    await expect(failed).rejects.toThrow('could not be loaded')
    expect(script()).toBeNull()

    const empty = loadPaddle(CONFIG)
    script()?.dispatchEvent(new Event('load'))
    await expect(empty).rejects.toThrow('did not load')

    const fake = fakePaddle()
    const retry = loadPaddle(CONFIG)
    window.Paddle = fake as unknown as Paddle
    document.head.querySelectorAll('script')[1]?.dispatchEvent(new Event('load'))
    expect(await retry).toBe(fake)
  })
})

describe('previewPrices', () => {
  it('maps line items to unit and line totals by price id', async () => {
    const fake = fakePaddle()
    fake.PricePreview.mockResolvedValue({
      data: {
        currencyCode: 'EUR',
        details: {
          lineItems: [
            { price: { id: 'pri_a' }, quantity: 3, formattedTotals: { total: '€30.00' }, formattedUnitTotals: { total: '€10.00' } },
          ],
        },
      },
    })
    const lines = await previewPrices(fake as unknown as Paddle, [{ priceId: 'pri_a', quantity: 3 }])
    expect(fake.PricePreview).toHaveBeenCalledWith({ items: [{ priceId: 'pri_a', quantity: 3 }] })
    expect(lines).toEqual({ pri_a: { unit: '€10.00', total: '€30.00' } })
  })
})
