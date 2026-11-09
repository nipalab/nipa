import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AppShell from './AppShell'
import { AuthLayout } from './AuthLayout'
import { NipaLogo } from './NipaLogo'

vi.mock('../auth', () => ({ useAuth: () => ({ me: null, signOut: () => {} }) }))

const mounted: Array<ReturnType<typeof createRoot>> = []

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  mounted.push(root)
  await act(async () => {
    root.render(<MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>{node}</MemoryRouter>)
  })
  return container
}

afterEach(async () => {
  await act(async () => {
    for (const root of mounted.splice(0)) root.unmount()
  })
  document.body.innerHTML = ''
})

describe('NipaLogo', () => {
  it('gives every copy its own gradient and is decorative without a title', async () => {
    const container = await render(
      <>
        <NipaLogo />
        <NipaLogo size={48} title="Nipa" />
      </>,
    )
    const [first, second] = [...container.querySelectorAll('svg')]
    const firstId = first.querySelector('linearGradient')?.id
    const secondId = second.querySelector('linearGradient')?.id
    expect(firstId).toBeTruthy()
    expect(firstId).not.toBe(secondId)
    expect(first.querySelector('rect')?.getAttribute('fill')).toBe(`url(#${firstId})`)
    expect(first.getAttribute('aria-hidden')).toBe('true')
    expect(first.getAttribute('width')).toBe('32')
    expect(second.getAttribute('role')).toBe('img')
    expect(second.getAttribute('aria-label')).toBe('Nipa')
    expect(second.getAttribute('width')).toBe('48')
  })

  it('shows the logo in the app header and above the sign-in title', async () => {
    const header = await render(
      <Routes>
        <Route element={<AppShell />}>
          <Route path="*" element={<p>page</p>} />
        </Route>
      </Routes>,
    )
    const home = header.querySelector('a[aria-label="Nipa home"]') as HTMLAnchorElement
    expect(home.getAttribute('href')).toBe('/dashboard')
    expect(home.querySelector('svg')).not.toBeNull()
    expect(home.textContent).toBe('Nipa')

    const auth = await render(<AuthLayout title="Sign in to Nipa">form</AuthLayout>)
    expect(auth.querySelector('a[aria-label="Nipa home"] svg')).not.toBeNull()
    expect(auth.querySelector('h1')?.textContent).toBe('Sign in to Nipa')
  })
})
