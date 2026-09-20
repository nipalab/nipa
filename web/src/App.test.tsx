import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { clearSession } from './api/client'

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

beforeEach(() => {
  clearSession()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function renderApp() {
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<App />)
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  return { container, root }
}

describe('App', () => {
  it('shows the login form when the refresh cookie is missing', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'refresh token required' }, 401)))

    const { container, root } = await renderApp()
    expect(container.textContent).toContain('Sign in')
    expect(container.querySelectorAll('input')).toHaveLength(2)
    act(() => root.unmount())
  })

  it('shows the home page after a silent refresh', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 }),
      ),
    )

    const { container, root } = await renderApp()
    expect(container.textContent).toContain('Signed in')
    act(() => root.unmount())
  })
})
