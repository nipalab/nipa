import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { UserResponse } from '../api/models'
import { UserMenu } from './UserMenu'

const user: UserResponse = {
  id: '1',
  name: 'Alice',
  email: 'alice@example.com',
  photo_url: '',
  is_admin: false,
  is_super_admin: false,
  notify_email: true,
  deleted: false,
}

const mounted: Array<ReturnType<typeof createRoot>> = []
let path = ''

function Where() {
  path = useLocation().pathname
  return null
}

async function render(me: UserResponse | null, onSignOut = () => {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  mounted.push(root)
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={['/start']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <UserMenu me={me} onSignOut={onSignOut} />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
  })
  return container
}

async function openMenu(container: HTMLElement) {
  await act(async () => {
    ;(container.querySelector('[aria-label="Open user navigation menu"]') as HTMLElement).click()
  })
}

function item(label: string): HTMLElement | undefined {
  return [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((entry) => entry.textContent?.includes(label))
}

afterEach(async () => {
  await act(async () => {
    for (const root of mounted.splice(0)) root.unmount()
  })
  document.body.innerHTML = ''
})

describe('UserMenu', () => {
  it('keeps the header to an avatar button and opens the account menu', async () => {
    const container = await render(user)
    expect(container.textContent).not.toContain('Sign out')
    await openMenu(container)
    expect(document.body.textContent).toContain('Alice')
    expect(document.body.textContent).toContain('alice@example.com')
    expect(item('Your profile')).toBeDefined()
    expect(item('Billing and plans')).toBeDefined()
    expect(item('Users')).toBeUndefined()
  })

  it('navigates to the chosen page', async () => {
    const container = await render(user)
    for (const [label, target] of [
      ['Your profile', '/settings/profile'],
      ['Billing and plans', '/settings/billing'],
      ['Dashboard', '/dashboard'],
    ]) {
      await openMenu(container)
      await act(async () => {
        item(label)?.click()
      })
      expect(path).toBe(target)
    }
  })

  it('shows site administration to admins and signs out', async () => {
    const onSignOut = vi.fn()
    const container = await render({ ...user, is_admin: true }, onSignOut)
    await openMenu(container)
    expect(document.body.textContent).toContain('Site administration')
    await act(async () => {
      item('Users')?.click()
    })
    expect(path).toBe('/admin/users')
    await openMenu(container)
    await act(async () => {
      item('Sign out')?.click()
    })
    expect(onSignOut).toHaveBeenCalled()
  })

  it('renders before the profile has loaded', async () => {
    const container = await render(null)
    await openMenu(container)
    expect(document.body.textContent).toContain('Signed in')
  })
})
