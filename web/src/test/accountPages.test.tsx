import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import type { OrgStorageResponse, UserResponse } from '../api/models'
import { IdentitiesCard } from '../components/IdentitiesCard'
import StorageCard from '../components/StorageCard'
import AdminUsersPage from '../pages/AdminUsersPage'
import BillingPage from '../pages/BillingPage'
import NewUserPage from '../pages/NewUserPage'
import { buttonByText, click, confirmDialog, flush, render, renderRoute, type, waitFor } from './render'

vi.mock('../api/endpoints')
vi.mock('../components/UserGroupPicker', () => ({
  UserGroupPicker: ({ onSelect }: { onSelect: (entry: { id: string; label: string; sublabel: string; isGroup: boolean }) => void }) => (
    <button type="button" onClick={() => onSelect({ id: '9', label: 'Nina', sublabel: 'nina@example.com', isGroup: false })}>
      pick user
    </button>
  ),
}))

const auth = vi.hoisted(() => ({ me: null as Record<string, unknown> | null }))
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me, refreshMe: vi.fn() }) }))

const api = vi.mocked(endpoints)

function user(overrides: Partial<UserResponse> = {}): UserResponse {
  return {
    id: '2',
    name: 'Dana',
    email: 'dana@example.com',
    photo_url: '',
    is_admin: false,
    is_super_admin: false,
    notify_email: true,
    deleted: false,
    ...overrides,
  }
}

beforeEach(() => {
  auth.me = { id: '1', name: 'Root', email: 'root@example.com', is_admin: true, is_super_admin: true }
  api.getOnboardingCapabilities.mockResolvedValue(null)
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

function byName(name: string): HTMLElement | null {
  return (
    [...document.querySelectorAll<HTMLElement>('[aria-label], [aria-labelledby]')].find(
      (element) =>
        element.getAttribute('aria-label') === name ||
        (element.getAttribute('aria-labelledby') ?? '')
          .split(' ')
          .some((id) => document.getElementById(id)?.textContent === name),
    ) ?? null
  )
}

async function menuItems(userName: string): Promise<HTMLElement[]> {
  await click(byName(`Manage ${userName}`))
  await waitFor(() => document.querySelector('[role="menuitem"]') !== null)
  return [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')]
}

async function chooseMenu(userName: string, label: string) {
  const item = (await menuItems(userName)).find((entry) => entry.textContent?.includes(label))
  if (!item) throw new Error(`menu item ${label} not found`)
  await click(item)
}

describe('AdminUsersPage', () => {
  it('toggles admin, resets a password through a dialog and deactivates after confirmation', async () => {
    api.listUsers.mockResolvedValue([
      user({ id: '1', name: 'Root', email: 'root@example.com', is_admin: true, is_super_admin: true }),
      user(),
      user({ id: '3', name: 'Bob', email: 'bob@example.com', is_admin: true }),
    ])
    api.updateUserFlags.mockResolvedValue(user())
    api.resetUserPassword.mockResolvedValue({ message: 'ok' })
    api.deactivateUser.mockResolvedValue({ message: 'ok' })
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))
    expect(container.textContent).toContain('You')
    expect(container.textContent).toContain('Super admin')

    const own = (await menuItems('Root')).map((item) => item.textContent)
    expect(own.some((text) => text?.includes('Deactivate'))).toBe(false)
    expect(own.some((text) => text?.includes('admin'))).toBe(false)
    await act(async () => {
      ;(document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })

    await chooseMenu('Dana', 'Make admin')
    await confirmDialog('Make admin')
    expect(api.updateUserFlags).toHaveBeenCalledWith('2', true, false)
    await chooseMenu('Bob', 'Remove admin')
    await confirmDialog('Remove admin')
    expect(api.updateUserFlags).toHaveBeenCalledWith('3', false, false)

    await chooseMenu('Dana', 'Reset password')
    await waitFor(() => document.body.textContent?.includes('New password') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    const passwords = () => document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input[autocomplete="new-password"]')
    expect(submit()?.disabled).toBe(true)
    await type(passwords()[0], 'short')
    expect(document.body.textContent).toContain('Use at least 8 characters.')
    await type(passwords()[0], 'new-secret')
    await type(passwords()[1], 'new-secrex')
    expect(document.body.textContent).toContain('The passwords do not match.')
    expect(submit()?.disabled).toBe(true)
    await type(passwords()[1], 'new-secret')
    await click(submit())
    await flush()
    expect(api.resetUserPassword).toHaveBeenCalledWith('2', 'new-secret')

    await chooseMenu('Dana', 'Deactivate')
    await confirmDialog('Deactivate')
    expect(api.deactivateUser).toHaveBeenCalledWith('2')
  })

  it('generates a password to hand over', async () => {
    api.listUsers.mockResolvedValue([user()])
    api.resetUserPassword.mockResolvedValue({ message: 'ok' })
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))
    await chooseMenu('Dana', 'Reset password')
    await waitFor(() => buttonByText(document.body, 'Generate') !== null)
    await click(buttonByText(document.body, 'Generate'))
    const [first, second] = [...document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input[autocomplete="new-password"]')]
    expect(first.value).toHaveLength(16)
    expect(second.value).toBe(first.value)
    expect(first.type).toBe('text')
    await click(byName('Copy password'))
    await flush()
    expect(writeText).toHaveBeenCalledWith(first.value)
    await click(byName('Hide password'))
    expect(first.type).toBe('password')
    await click(document.body.querySelector('button[type="submit"]'))
    await flush()
    expect(api.resetUserPassword).toHaveBeenCalledWith('2', first.value)
  })

  it('filters by role, shows an error and hides the admin toggle for plain admins', async () => {
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    api.listUsers.mockResolvedValue([user(), user({ id: '3', name: 'Bob', email: 'bob@example.com', is_admin: true })])
    api.deactivateUser.mockRejectedValue(new Error('cannot deactivate'))
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))
    const tab = (label: string) =>
      [...container.querySelectorAll<HTMLButtonElement>('nav[aria-label="User role"] button')].find((button) =>
        button.textContent?.startsWith(label),
      ) as HTMLButtonElement
    expect(tab('All').textContent).toBe('All2')
    await click(tab('Admins'))
    expect(container.textContent).not.toContain('Dana')
    await click(tab('Members'))
    expect(container.textContent).not.toContain('Bob')
    expect(tab('Members').getAttribute('aria-pressed')).toBe('true')

    const items = await menuItems('Dana')
    expect(items.some((item) => item.textContent?.includes('Make admin'))).toBe(false)
    await click(items.find((item) => item.textContent?.includes('Deactivate')))
    await confirmDialog('Deactivate')
    await waitFor(() => (container.textContent ?? '').includes('cannot deactivate'))

    await type(container.querySelector<HTMLInputElement>('[aria-label="Filter users"]'), 'nobody')
    expect(container.textContent).toContain('No users match these filters.')
  })

  it('refuses users without admin rights', async () => {
    auth.me = { id: '1', is_admin: false, is_super_admin: false }
    api.listUsers.mockResolvedValue([])
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    expect(container.textContent).toContain('You need global admin permission to manage users.')
  })
})

describe('NewUserPage', () => {
  async function fill(container: HTMLElement, email = 'carol@example.com') {
    await type(container.querySelector<HTMLInputElement>('input[placeholder="e.g. Dana Smith"]'), 'Carol')
    await type(container.querySelector<HTMLInputElement>('input[type="email"]'), email)
    const passwords = container.querySelectorAll<HTMLInputElement>('input[autocomplete="new-password"]')
    await type(passwords[0], 'secret123')
    await type(passwords[1], 'secret123')
  }

  it('creates an admin user and returns to the list', async () => {
    api.createUser.mockResolvedValue(user({ id: '9' }))
    api.updateUserFlags.mockResolvedValue(user({ id: '9', is_admin: true }))
    const { container, location } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    await fill(container, 'carol@')
    expect(container.textContent).toContain('Enter a valid email address.')
    expect(buttonByText(container, 'Create user')?.disabled).toBe(true)
    await type(container.querySelector<HTMLInputElement>('input[type="email"]'), 'carol@example.com')
    await click(container.querySelector('input[type="checkbox"]'))
    await click(buttonByText(container, 'Create user'))
    await waitFor(() => location() === '/admin/users')
    expect(api.createUser).toHaveBeenCalledWith('Carol', 'carol@example.com', 'secret123')
    expect(api.updateUserFlags).toHaveBeenCalledWith('9', true, false)
  })

  it('shows the server error and cancels back to the list', async () => {
    api.createUser.mockRejectedValue(new Error('email taken'))
    const { container, location } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    await fill(container)
    await click(buttonByText(container, 'Create user'))
    await waitFor(() => (container.textContent ?? '').includes('email taken'))
    expect(api.updateUserFlags).not.toHaveBeenCalled()
    await click(buttonByText(container, 'Cancel'))
    await waitFor(() => location() === '/admin/users')
  })

  it('only lets super admins grant admin', async () => {
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    const { container } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    expect(container.textContent).not.toContain('Make this user an administrator')
  })

  it('refuses non-admins', async () => {
    auth.me = { id: '1', is_admin: false, is_super_admin: false }
    const { container } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    expect(container.textContent).toContain('You need global admin permission')
  })
})

describe('BillingPage', () => {
  const billing = {
    user_id: '1',
    used_bytes: 1536,
    quota_bytes: 4096,
    orgs: [{ id: 'o1', slug: 'sticker', name: 'Sticker', used_bytes: 1024 }],
  }

  it('shows quota usage and the per-organization breakdown', async () => {
    api.getMyBilling.mockResolvedValue(billing)
    const { container } = await renderRoute('/settings/billing', '/settings/billing', <BillingPage />)
    await waitFor(() => (container.textContent ?? '').includes('Sticker'))
    expect(container.textContent).toContain('1.5 KiB of 4 KiB used')
    expect(container.querySelector('[role="progressbar"], [class*="ProgressBar"]')).not.toBeNull()
  })

  it('says so when the account owns no organizations', async () => {
    api.getMyBilling.mockResolvedValue({ ...billing, quota_bytes: null, orgs: [] })
    const { container } = await renderRoute('/settings/billing', '/settings/billing', <BillingPage />)
    await waitFor(() => (container.textContent ?? '').includes('unlimited quota'))
    expect(container.textContent).toContain('not the billing owner')
  })

  it('lets a super admin set and clear an owner quota', async () => {
    api.getMyBilling.mockResolvedValue(billing)
    api.setOwnerQuota.mockResolvedValue(undefined as never)
    const { container } = await renderRoute('/settings/billing', '/settings/billing', <BillingPage />)
    await waitFor(() => (container.textContent ?? '').includes('Set owner quota'))
    const [userId, quota] = Array.from(container.querySelectorAll<HTMLInputElement>('form input'))

    await click(buttonByText(container, 'Set quota'))
    expect(api.setOwnerQuota).not.toHaveBeenCalled()

    await type(userId, '73')
    await click(buttonByText(container, 'Set quota'))
    await waitFor(() => (container.textContent ?? '').includes('positive number'))

    await type(quota, '2')
    await click(buttonByText(container, 'Set quota'))
    await flush()
    expect(api.setOwnerQuota).toHaveBeenCalledWith('73', 2 * 1024 * 1024)

    await click(buttonByText(container, 'Clear (unlimited)'))
    await flush()
    expect(api.setOwnerQuota).toHaveBeenCalledWith('73', null)

    api.clearOwnerQuota.mockResolvedValue(billing)
    await click(buttonByText(container, 'Remove override'))
    await flush()
    expect(api.clearOwnerQuota).toHaveBeenCalledWith('73')
  })

  it('reports quota failures and explains a disabled ledger', async () => {
    api.getMyBilling.mockResolvedValue(billing)
    api.setOwnerQuota.mockRejectedValue(new Error('forbidden'))
    const first = await renderRoute('/settings/billing', '/settings/billing', <BillingPage />)
    await waitFor(() => (first.container.textContent ?? '').includes('Set owner quota'))
    await type(first.container.querySelector<HTMLInputElement>('form input'), '73')
    await click(buttonByText(first.container, 'Clear (unlimited)'))
    await waitFor(() => (first.container.textContent ?? '').includes('forbidden'))
    await click(first.container.querySelector('button[class*="BannerDismiss"]'))
    expect(first.container.textContent).not.toContain('forbidden')

    api.getMyBilling.mockResolvedValue(null as never)
    auth.me = { id: '1', is_admin: false, is_super_admin: false }
    const second = await renderRoute('/settings/billing', '/settings/billing', <BillingPage />)
    await waitFor(() => (second.container.textContent ?? '').includes('not enabled on this server'))
  })
})

describe('StorageCard', () => {
  const data: OrgStorageResponse = {
    used_bytes: 1536,
    quota_bytes: 4096,
    owner_id: '1',
    projects: [
      { id: 'p1', slug: 'demo', name: 'Demo', deleted: false, used_bytes: 1024 },
      { id: 'p2', slug: 'old', name: 'Old', deleted: true, used_bytes: 512 },
    ],
  }

  it('shows usage, projects and deleted markers; hides admin actions for others', async () => {
    auth.me = { id: '5', is_super_admin: false }
    const { container } = await render(<StorageCard org="sticker" data={data} reload={() => undefined} />)
    expect(container.textContent).toContain('1.5 KiB of 4 KiB used')
    expect(container.textContent).toContain('(deleted)')
    expect(buttonByText(container, 'Recount')).toBeNull()
  })

  it('shows unlimited quota without a progress bar', async () => {
    const { container } = await render(
      <StorageCard org="sticker" data={{ ...data, quota_bytes: null, projects: [] }} reload={() => undefined} />,
    )
    expect(container.textContent).toContain('unlimited quota')
    expect(container.textContent).toContain('No projects yet.')
  })

  it('recounts, reloads and reports failures', async () => {
    const reload = vi.fn()
    api.recountOrgStorage.mockResolvedValueOnce(undefined as never).mockRejectedValueOnce(new Error('recount failed'))
    const { container } = await render(<StorageCard org="sticker" data={data} reload={reload} />)
    await click(buttonByText(container, 'Recount'))
    await flush()
    expect(api.recountOrgStorage).toHaveBeenCalledWith('sticker')
    expect(reload).toHaveBeenCalled()

    await click(buttonByText(container, 'Recount'))
    await waitFor(() => (container.textContent ?? '').includes('recount failed'))
  })

  it('transfers the billing owner once a user is picked', async () => {
    const reload = vi.fn()
    api.setOrgBillingOwner.mockResolvedValue(undefined as never)
    const { container } = await render(<StorageCard org="sticker" data={data} reload={reload} />)
    await click(buttonByText(container, 'Transfer billing owner'))
    await waitFor(() => document.body.textContent?.includes('New owner') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await click(buttonByText(document.body, 'pick user'))
    await click(submit())
    await flush()
    expect(api.setOrgBillingOwner).toHaveBeenCalledWith('sticker', '9')
    expect(reload).toHaveBeenCalled()
  })
})

describe('IdentitiesCard', () => {
  const identity = { id: '9', provider: 'github', email: 'alice@example.com', created_at: '2026-07-01T12:00:00Z' }

  beforeEach(() => {
    api.getOnboardingCapabilities.mockResolvedValue({ registration: true, providers: ['github', 'google'] })
  })

  it('lists identities, offers unlinked providers and disconnects after confirmation', async () => {
    api.listIdentities.mockResolvedValueOnce([identity]).mockResolvedValue([])
    api.disconnectIdentity.mockResolvedValue({ message: 'ok' })
    const { container } = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await waitFor(() => (container.textContent ?? '').includes('alice@example.com'))
    expect(buttonByText(container, 'Connect Google')).not.toBeNull()
    expect(buttonByText(container, 'Connect GitHub')).toBeNull()

    await click(buttonByText(container, 'Disconnect'))
    await confirmDialog('Disconnect')
    expect(api.disconnectIdentity).toHaveBeenCalledWith('9')
    await waitFor(() => (container.textContent ?? '').includes('No connected accounts.'))
  })

  it('starts linking a provider and reports a start failure', async () => {
    api.listIdentities.mockResolvedValue([])
    api.startIdentityLink.mockRejectedValueOnce(new Error('cannot start'))
    const { container } = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await waitFor(() => buttonByText(container, 'Connect GitHub') !== null)
    await click(buttonByText(container, 'Connect GitHub'))
    await waitFor(() => (container.textContent ?? '').includes('cannot start'))
    expect(api.startIdentityLink).toHaveBeenCalledWith('github', '/settings/profile?tab=accounts')
  })

  it('redirects to the provider once the link URL is known', async () => {
    api.listIdentities.mockResolvedValue([])
    api.startIdentityLink.mockResolvedValue({ url: 'about:blank#provider' })
    const { container } = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await waitFor(() => buttonByText(container, 'Connect GitHub') !== null)
    await click(buttonByText(container, 'Connect GitHub'))
    await flush()
    expect(api.startIdentityLink).toHaveBeenCalled()
  })

  it('shows link results from the query string and dismisses them', async () => {
    api.listIdentities.mockResolvedValue([])
    const linked = await renderRoute('/settings/profile', '/settings/profile?oauth=linked', <IdentitiesCard />)
    await waitFor(() => (linked.container.textContent ?? '').includes('Account connected.'))
    await click(linked.container.querySelector('button[class*="BannerDismiss"]'))
    await waitFor(() => !(linked.container.textContent ?? '').includes('Account connected.'))

    const failed = await renderRoute(
      '/settings/profile',
      '/settings/profile?oauth_error=email_conflict',
      <IdentitiesCard />,
    )
    await waitFor(() => (failed.container.textContent ?? '').includes('An account with this email already exists.'))
    const unknown = await renderRoute('/settings/profile', '/settings/profile?oauth_error=weird', <IdentitiesCard />)
    await waitFor(() => (unknown.container.textContent ?? '').includes('Connecting failed (weird).'))
  })

  it('reports disconnect failures and load failures', async () => {
    api.listIdentities.mockResolvedValue([identity])
    api.disconnectIdentity.mockRejectedValue(new Error('last login method'))
    const { container } = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await waitFor(() => buttonByText(container, 'Disconnect') !== null)
    await click(buttonByText(container, 'Disconnect'))
    await confirmDialog('Disconnect')
    await waitFor(() => (container.textContent ?? '').includes('last login method'))

    api.listIdentities.mockRejectedValue(new Error('load failed'))
    const failed = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await waitFor(() => (failed.container.textContent ?? '').includes('load failed'))
  })

  it('renders nothing when the server has no social login support', async () => {
    api.listIdentities.mockResolvedValue(null)
    const { container } = await renderRoute('/settings/profile', '/settings/profile', <IdentitiesCard />)
    await flush()
    expect(container.textContent).toBe('')
  })
})
