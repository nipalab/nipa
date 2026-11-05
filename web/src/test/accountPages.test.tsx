import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import type { OrgStorageResponse, UserResponse } from '../api/models'
import { IdentitiesCard } from '../components/IdentitiesCard'
import StorageCard from '../components/StorageCard'
import AdminUsersPage from '../pages/AdminUsersPage'
import BillingPage from '../pages/BillingPage'
import NewUserPage from '../pages/NewUserPage'
import { buttonByText, buttonsByText, click, confirmDialog, flush, render, renderRoute, type, waitFor } from './render'

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
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('AdminUsersPage', () => {
  it('toggles admin, resets a password through a dialog and deactivates after confirmation', async () => {
    api.listUsers.mockResolvedValue([user(), user({ id: '3', name: 'Bob', email: 'bob@example.com', is_admin: true })])
    api.updateUserFlags.mockResolvedValue(user())
    api.resetUserPassword.mockResolvedValue({ message: 'ok' })
    api.deactivateUser.mockResolvedValue({ message: 'ok' })
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))

    await click(buttonByText(container, 'Make admin'))
    await flush()
    expect(api.updateUserFlags).toHaveBeenCalledWith('2', true, false)
    await click(buttonByText(container, 'Remove admin'))
    await flush()
    expect(api.updateUserFlags).toHaveBeenCalledWith('3', false, false)

    await click(buttonsByText(container, 'Reset password')[0])
    await waitFor(() => document.body.textContent?.includes('New password') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'new-secret')
    await click(submit())
    await flush()
    expect(api.resetUserPassword).toHaveBeenCalledWith('2', 'new-secret')

    await click(buttonsByText(container, 'Deactivate')[0])
    await confirmDialog('Deactivate')
    expect(api.deactivateUser).toHaveBeenCalledWith('2')
  })

  it('shows an error and hides the admin toggle for plain admins', async () => {
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    api.listUsers.mockResolvedValue([user()])
    api.deactivateUser.mockRejectedValue(new Error('cannot deactivate'))
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))
    expect(buttonByText(container, 'Make admin')).toBeNull()
    await click(buttonByText(container, 'Deactivate'))
    await confirmDialog('Deactivate')
    await waitFor(() => (container.textContent ?? '').includes('cannot deactivate'))
  })
})

describe('NewUserPage', () => {
  it('creates a user and returns to the list', async () => {
    api.createUser.mockResolvedValue(user())
    const { container, location } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    const inputs = container.querySelectorAll('input')
    await type(inputs[0], 'Carol')
    await type(inputs[1], 'carol@example.com')
    await type(inputs[2], 'secret123')
    await click(buttonByText(container, 'Create user'))
    await waitFor(() => location() === '/admin/users')
    expect(api.createUser).toHaveBeenCalledWith('Carol', 'carol@example.com', 'secret123')
  })

  it('shows the server error', async () => {
    api.createUser.mockRejectedValue(new Error('email taken'))
    const { container } = await renderRoute('/admin/users/new', '/admin/users/new', <NewUserPage />)
    const inputs = container.querySelectorAll('input')
    await type(inputs[0], 'Carol')
    await type(inputs[1], 'carol@example.com')
    await type(inputs[2], 'secret123')
    await click(buttonByText(container, 'Create user'))
    await waitFor(() => (container.textContent ?? '').includes('email taken'))
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
