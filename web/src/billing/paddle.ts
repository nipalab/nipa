import type { BillingCapabilities } from '../api/models'

export const PADDLE_SCRIPT_URL = 'https://cdn.paddle.com/paddle/v2/paddle.js'

export interface PaddleEvent {
  name?: string
  data?: unknown
}

export interface PaddlePreviewItem {
  priceId: string
  quantity: number
}

interface PaddleMoney {
  total: string
}

export interface PaddlePricePreview {
  data: {
    currencyCode: string
    details: {
      lineItems: Array<{
        price: { id: string }
        quantity: number
        formattedTotals: PaddleMoney
        formattedUnitTotals: PaddleMoney
      }>
    }
  }
}

export interface Paddle {
  Environment: { set(environment: string): void }
  Initialize(options: {
    token: string
    eventCallback?: (event: PaddleEvent) => void
    checkout?: { settings?: Record<string, unknown> }
  }): void
  Checkout: { open(options: { transactionId: string; settings?: Record<string, unknown> }): void }
  PricePreview(request: { items: PaddlePreviewItem[] }): Promise<PaddlePricePreview>
}

declare global {
  interface Window {
    Paddle?: Paddle
  }
}

const listeners = new Set<(event: PaddleEvent) => void>()
let loading: Promise<Paddle> | null = null

function injectScript(): Promise<Paddle> {
  return new Promise((resolve, reject) => {
    if (window.Paddle) {
      resolve(window.Paddle)
      return
    }
    const script = document.createElement('script')
    script.src = PADDLE_SCRIPT_URL
    script.async = true
    script.onload = () => (window.Paddle ? resolve(window.Paddle) : reject(new Error('Paddle.js did not load')))
    script.onerror = () => {
      script.remove()
      reject(new Error('Paddle.js could not be loaded'))
    }
    document.head.appendChild(script)
  })
}

// loadPaddle injects Paddle.js once per page and initializes it; Paddle only
// allows one Initialize call, so events fan out to subscribePaddleEvents.
export function loadPaddle(config: BillingCapabilities): Promise<Paddle> {
  if (!loading) {
    loading = injectScript()
      .then((paddle) => {
        if (config.environment === 'sandbox') paddle.Environment.set('sandbox')
        paddle.Initialize({
          token: config.client_token,
          checkout: { settings: { displayMode: 'overlay', theme: 'dark' } },
          eventCallback: (event) => listeners.forEach((listener) => listener(event)),
        })
        return paddle
      })
      .catch((err: unknown) => {
        loading = null
        throw err
      })
  }
  return loading
}

export function subscribePaddleEvents(listener: (event: PaddleEvent) => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export interface PreviewLine {
  unit: string
  total: string
}

export async function previewPrices(paddle: Paddle, items: PaddlePreviewItem[]): Promise<Record<string, PreviewLine>> {
  const response = await paddle.PricePreview({ items })
  const lines: Record<string, PreviewLine> = {}
  for (const item of response.data.details.lineItems) {
    lines[item.price.id] = { unit: item.formattedUnitTotals.total, total: item.formattedTotals.total }
  }
  return lines
}

export function resetPaddleForTests() {
  loading = null
  listeners.clear()
}
