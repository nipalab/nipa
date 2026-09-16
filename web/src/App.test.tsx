import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { clearSession } from './api/client'

beforeEach(() => {
  localStorage.clear()
  clearSession()
})

afterEach(() => {
  vi.restoreAllMocks()
})

function renderApp() {
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<App />)
  })
  return { container, root }
}

describe('App', () => {
  it('shows the login form when not authenticated', () => {
    const { container, root } = renderApp()
    expect(container.textContent).toContain('Sign in')
    expect(container.querySelectorAll('input')).toHaveLength(2)
    act(() => root.unmount())
  })

  it('shows the home page when already authenticated', () => {
    localStorage.setItem('nipa.accessToken', 'token')
    localStorage.setItem('nipa.refreshToken', 'refresh')
    const { container, root } = renderApp()
    expect(container.textContent).toContain('Signed in')
    act(() => root.unmount())
  })
})