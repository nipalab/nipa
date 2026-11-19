import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import * as endpoints from '../../api/endpoints'
import type { OrgStorageResponse, StorageServerResponse } from '../../api/models'
import NewProjectPage from '../../pages/NewProjectPage'
import OrgSettingsPage from '../../pages/OrgSettingsPage'
import ProfilePage from '../../pages/ProfilePage'
import StorageCard from '../StorageCard'
import {
  buttonByText,
  choose,
  click,
  confirmDialog,
  flush,
  render,
  renderRoute,
  type,
  waitFor,
} from '../../test/render'
import { OrgDefaultStorageCard } from './OrgDefaultStorageCard'
import { ProjectStorageCard } from './ProjectStorageCard'
import { StorageServersCard, StorageServersTab } from './StorageServersCard'
import { checkInfo, isOwnStorageError, probeCheckOf } from './storage'

vi.mock('../../api/endpoints')
const auth = vi.hoisted(() => ({ me: null as Record<string, unknown> | null }))
vi.mock('../../auth', () => ({ useAuth: () => ({ me: auth.me, refreshMe: vi.fn() }) }))

const api = vi.mocked(endpoints)

const OWN_STORAGE = new ApiError('own storage requires the Own storage add-on', 402)

function server(overrides: Partial<StorageServerResponse> = {}): StorageServerResponse {
  return {
    id: 's1',
    name: 'Studio bucket',
    endpoint: 'https://s3.example.com',
    region: 'eu-west-1',
    bucket: 'assets',
    prefix: 'nipa',
    access_key_id: 'AKIA123',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function byo(enabled = true) {
  api.getOnboardingCapabilities.mockResolvedValue(
    enabled ? { registration: true, providers: [], byo_storage: true } : { registration: true, providers: [] },
  )
}

function field(label: string): HTMLInputElement {
  const element = [...document.querySelectorAll('label')].find((item) => item.textContent?.trim().startsWith(label))
  const input = element && document.getElementById(element.htmlFor)
  if (!input) throw new Error(`field ${label} not found`)
  return input as HTMLInputElement
}

function dialog(): HTMLElement {
  return document.querySelector('[role="dialog"]') as HTMLElement
}

function text(): string {
  return document.body.textContent ?? ''
}

beforeEach(() => {
  auth.me = { id: '1', is_admin: false, is_super_admin: false }
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('storage helpers', () => {
  it('recognizes own storage and probe errors', () => {
    expect(isOwnStorageError(OWN_STORAGE)).toBe(true)
    expect(isOwnStorageError(new ApiError('seat limit reached', 402))).toBe(false)
    expect(probeCheckOf(new ApiError('storage server check conditional_create failed: 200', 400))).toBe('conditional_create')
    expect(probeCheckOf(new ApiError('bucket and prefix cannot be changed', 400))).toBeNull()
    expect(probeCheckOf(new Error('storage server check bucket failed'))).toBeNull()
    expect(checkInfo('bucket').label).toBe('Bucket access')
    expect(checkInfo('mystery')).toEqual({ label: 'mystery', explanation: 'The storage server failed this check.' })
  })
})

describe('StorageServersTab', () => {
  it('explains when the feature is off', async () => {
    byo(false)
    await render(<StorageServersTab />)
    await waitFor(() => text().includes('not available on this server'))
    expect(api.listStorageServers).not.toHaveBeenCalled()
  })

  it('shows the empty state when enabled', async () => {
    byo()
    api.listStorageServers.mockResolvedValue([])
    await render(<StorageServersTab />)
    await waitFor(() => text().includes('No storage servers yet.'))
    expect(text()).toContain('Own storage add-on')
  })
})

describe('StorageServersCard', () => {
  it('lists servers without secrets and tests connections', async () => {
    api.listStorageServers.mockResolvedValue([server(), server({ id: 's2', name: 'Lab', prefix: '', region: '' })])
    api.testStorageServer.mockResolvedValueOnce({ ok: true })
    api.testStorageServer.mockResolvedValueOnce({ ok: false, check: 'conditional_create', error: 'second upload answered 200' })
    await render(<StorageServersCard />)
    await waitFor(() => text().includes('Studio bucket'))
    expect(text()).toContain('https://s3.example.com')
    expect(text()).toContain('AKIA123')
    expect(text()).toContain('region eu-west-1')

    const [first, second] = [...document.querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Test connection')
    await click(first)
    await waitFor(() => text().includes('Connection works'))
    await click(second)
    await waitFor(() => text().includes('Check failed: Create-only upload'))
    expect(text()).toContain('If-None-Match')
    expect(text()).toContain('second upload answered 200')
    expect(api.testStorageServer).toHaveBeenCalledWith('s2')
  })

  it('shows the add-on banner when a test is refused', async () => {
    api.listStorageServers.mockResolvedValue([server()])
    api.testStorageServer.mockRejectedValue(OWN_STORAGE)
    await render(<StorageServersCard />)
    await waitFor(() => text().includes('Studio bucket'))
    await click(buttonByText(document.body, 'Test connection'))
    await waitFor(() => text().includes('Requires the Own storage add-on'))
    expect(document.querySelector('a[href="/settings/billing"]')).not.toBeNull()
  })

  it('deletes a server and explains when it is in use', async () => {
    api.listStorageServers.mockResolvedValue([server()])
    api.deleteStorageServer.mockRejectedValueOnce(new ApiError('storage server is in use', 409))
    api.deleteStorageServer.mockRejectedValueOnce(new ApiError('boom', 500))
    api.deleteStorageServer.mockResolvedValueOnce(undefined)
    await render(<StorageServersCard />)
    await waitFor(() => text().includes('Studio bucket'))

    await click(buttonByText(document.body, 'Delete'))
    await confirmDialog('Delete server')
    await waitFor(() => text().includes('is still used by a repository'))

    await click(buttonByText(document.body, 'Delete'))
    await confirmDialog('Delete server')
    await waitFor(() => text().includes('boom'))

    await click(buttonByText(document.body, 'Delete'))
    await confirmDialog('Delete server')
    await waitFor(() => api.listStorageServers.mock.calls.length === 2)
    expect(api.deleteStorageServer).toHaveBeenCalledTimes(3)
  })

  it('adds a server and reports probe and add-on failures', async () => {
    api.listStorageServers.mockResolvedValue([])
    api.createStorageServer
      .mockRejectedValueOnce(new ApiError('storage server check bucket failed: access denied', 400))
      .mockRejectedValueOnce(OWN_STORAGE)
      .mockRejectedValueOnce(new ApiError('a storage server with this name already exists', 409))
      .mockResolvedValueOnce(server())
    await render(<StorageServersCard />)
    await waitFor(() => text().includes('No storage servers yet.'))
    await click(buttonByText(document.body, 'Add storage server'))
    await waitFor(() => dialog() !== null)

    const submit = () => buttonByText(dialog(), 'Add storage server')
    expect(submit()?.disabled).toBe(true)
    await type(field('Name'), ' Studio ')
    await type(field('Endpoint'), 'https://s3.example.com')
    await type(field('Region'), 'eu-west-1')
    await type(field('Bucket'), 'assets')
    await type(field('Prefix'), 'nipa')
    await type(field('Access key ID'), 'AKIA123')
    await type(field('Secret access key'), 'secret')
    expect(submit()?.disabled).toBe(false)

    await click(submit())
    await waitFor(() => text().includes('Check failed: Bucket access'))
    expect(text()).toContain('access denied')

    await click(submit())
    await waitFor(() => text().includes('Requires the Own storage add-on'))
    expect(text()).not.toContain('Check failed')

    await click(submit())
    await waitFor(() => text().includes('already exists'))

    await click(submit())
    await waitFor(() => dialog() === null)
    expect(api.createStorageServer).toHaveBeenLastCalledWith({
      name: 'Studio',
      endpoint: 'https://s3.example.com',
      region: 'eu-west-1',
      bucket: 'assets',
      prefix: 'nipa',
      access_key_id: 'AKIA123',
      secret_access_key: 'secret',
    })
    expect(api.listStorageServers).toHaveBeenCalledTimes(2)
  })

  it('edits only the changed fields and keeps bucket and prefix fixed', async () => {
    api.listStorageServers.mockResolvedValue([server()])
    api.testStorageServer.mockResolvedValue({ ok: true })
    api.updateStorageServer.mockResolvedValue(server({ name: 'Renamed' }))
    await render(<StorageServersCard />)
    await waitFor(() => text().includes('Studio bucket'))
    await click(buttonByText(document.body, 'Test connection'))
    await waitFor(() => text().includes('Connection works'))

    await click(buttonByText(document.body, 'Edit'))
    await waitFor(() => dialog() !== null)
    expect(field('Bucket').disabled).toBe(true)
    expect(field('Prefix').disabled).toBe(true)
    expect(field('Secret access key').value).toBe('')

    await click(buttonByText(dialog(), 'Save'))
    await waitFor(() => dialog() === null)
    expect(api.updateStorageServer).not.toHaveBeenCalled()

    await click(buttonByText(document.body, 'Edit'))
    await waitFor(() => dialog() !== null)
    await type(field('Name'), 'Renamed')
    await type(field('Endpoint'), 'https://r2.example.com')
    await type(field('Region'), 'auto')
    await type(field('Access key ID'), 'AKIA999')
    await type(field('Secret access key'), 'new-secret')
    await click(buttonByText(dialog(), 'Save'))
    await waitFor(() => dialog() === null)
    expect(api.updateStorageServer).toHaveBeenCalledWith('s1', {
      name: 'Renamed',
      endpoint: 'https://r2.example.com',
      region: 'auto',
      access_key_id: 'AKIA999',
      secret_access_key: 'new-secret',
    })
    expect(text()).not.toContain('Connection works')
  })
})

describe('OrgDefaultStorageCard', () => {
  it('is read-only for people who cannot manage it', async () => {
    api.getOrgStorageServer.mockResolvedValue({ server: { id: 's9', name: 'Owner bucket' }, can_manage: false })
    await render(<OrgDefaultStorageCard org="acme" />)
    await waitFor(() => text().includes('Owner bucket'))
    expect(text()).toContain('billing owner can change this')
    expect(document.querySelector('select')).toBeNull()
    expect(api.listStorageServers).not.toHaveBeenCalled()
  })

  it('sets and clears the default', async () => {
    api.getOrgStorageServer
      .mockResolvedValueOnce({ server: null, can_manage: true })
      .mockResolvedValue({ server: { id: 's1', name: 'Studio bucket' }, can_manage: true })
    api.listStorageServers.mockResolvedValue([server()])
    api.setOrgStorageServer.mockRejectedValueOnce(OWN_STORAGE)
    api.setOrgStorageServer.mockResolvedValue({ server: { id: 's1', name: 'Studio bucket' }, can_manage: true })
    api.clearOrgStorageServer.mockResolvedValue(undefined)
    await render(<OrgDefaultStorageCard org="acme" />)
    await waitFor(() => document.querySelector('option[value="s1"]') !== null)
    const save = () => buttonByText(document.body, 'Save default')
    expect(save()?.disabled).toBe(true)

    await choose(document.querySelector('select'), 's1')
    await click(save())
    await waitFor(() => text().includes('Requires the Own storage add-on'))

    await click(save())
    await waitFor(() => text().includes('Default storage saved.'))
    expect(api.setOrgStorageServer).toHaveBeenLastCalledWith('acme', 's1')
    await waitFor(() => document.querySelector('select')?.value === 's1')

    await choose(document.querySelector('select'), '')
    await click(save())
    await waitFor(() => api.clearOrgStorageServer.mock.calls.length === 1)
    expect(api.clearOrgStorageServer).toHaveBeenCalledWith('acme')
  })

  it('keeps a default owned by someone else in the list', async () => {
    api.getOrgStorageServer.mockResolvedValue({ server: { id: 's9', name: 'Other bucket' }, can_manage: true })
    api.listStorageServers.mockRejectedValue(new Error('nope'))
    await render(<OrgDefaultStorageCard org="acme" />)
    await waitFor(() => document.querySelector('option[value="s9"]') !== null)
    expect(document.querySelector('select')?.value).toBe('s9')
  })
})

describe('ProjectStorageCard', () => {
  it('renders nothing when the feature is off', async () => {
    byo(false)
    const view = await render(<ProjectStorageCard org="acme" project="game" />)
    await flush()
    await flush()
    expect(view.container.textContent).toBe('')
    expect(api.getProjectStorage).not.toHaveBeenCalled()
  })

  it('shows the own server or Nipa storage', async () => {
    byo()
    api.getProjectStorage.mockResolvedValueOnce({ external: true, server: { id: 's1', name: 'Studio bucket' } })
    const view = await render(<ProjectStorageCard org="acme" project="game" />)
    await waitFor(() => text().includes('Studio bucket'))
    expect(text()).toContain('does not count toward the storage quota')
    await view.unmount()

    api.getProjectStorage.mockResolvedValueOnce({ external: false, server: null })
    await render(<ProjectStorageCard org="acme" project="game" />)
    await waitFor(() => text().includes('Nipa storage'))
    expect(api.getProjectStorage).toHaveBeenCalledWith('acme', 'game')
  })
})

describe('StorageCard', () => {
  it('marks external projects', async () => {
    const data: OrgStorageResponse = {
      used_bytes: 1024,
      quota_bytes: null,
      owner_id: '1',
      projects: [
        { id: 'p1', slug: 'game', name: 'Game', deleted: false, used_bytes: 1024 },
        { id: 'p2', slug: 'film', name: 'Film', deleted: false, used_bytes: 0, external: true },
      ],
    }
    await render(<StorageCard org="acme" data={data} reload={() => undefined} />)
    expect(text()).toContain('External storage')
    expect(text()).toContain('Game1 KiB')
  })
})

describe('pages', () => {
  it('shows the storage servers tab in account settings', async () => {
    byo()
    api.listStorageServers.mockResolvedValue([server()])
    await render(<ProfilePage />, '/settings/profile?tab=storage')
    await waitFor(() => text().includes('Studio bucket'))
    expect(document.querySelector('a[href="/settings/profile?tab=storage"]')).not.toBeNull()
  })

  it('hides the storage servers tab without the feature', async () => {
    byo(false)
    await render(<ProfilePage />, '/settings/profile')
    await flush()
    await flush()
    expect(document.querySelector('a[href="/settings/profile?tab=storage"]')).toBeNull()
  })

  it('shows the default storage tab in org settings without the ledger', async () => {
    byo()
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    api.listOrgs.mockResolvedValue([])
    api.getOrgStorage.mockResolvedValue(null)
    api.getOrgStorageServer.mockResolvedValue({ server: null, can_manage: false })
    await renderRoute('/:org/settings', '/acme/settings?tab=storage', <OrgSettingsPage />)
    await waitFor(() => text().includes('Default storage'))
    expect(text()).toContain('Nipa storage')
  })

  async function newProject(
    servers: StorageServerResponse[],
    orgDefault: { id: string; name: string } | null,
    canManage = true,
  ) {
    byo()
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    api.listOrgs.mockResolvedValue([])
    api.listStorageServers.mockResolvedValue(servers)
    api.getOrgStorageServer.mockResolvedValue({ server: orgDefault, can_manage: canManage })
    const view = await renderRoute('/:org/new', '/acme/new', <NewProjectPage />)
    await waitFor(() => document.querySelector('form') !== null)
    await type(field('Name'), 'Game')
    return view
  }

  it('creates a project on the chosen storage server', async () => {
    api.createProject.mockResolvedValue({ slug: 'game' } as never)
    const view = await newProject([server(), server({ id: 's2', name: 'Lab' })], { id: 's2', name: 'Lab' })
    await waitFor(() => document.querySelector('select')?.value === 's2')
    expect(document.querySelector('option[value=""]')).toBeNull()
    expect(text()).toContain('Lab (organization default)')
    await choose(document.querySelector('select'), 's1')
    await click(buttonByText(document.body, 'Create repository'))
    await waitFor(() => view.location() === '/acme/game')
    expect(api.createProject).toHaveBeenCalledWith('acme', 'Game', '', 'game', 's1')
  })

  it('offers Nipa storage and reports refusals', async () => {
    api.createProject
      .mockRejectedValueOnce(OWN_STORAGE)
      .mockRejectedValueOnce(new ApiError('only the organization’s billing owner can choose a storage server', 403))
    await newProject([server()], null)
    await waitFor(() => document.querySelector('option[value=""]')?.textContent === 'Nipa storage')
    await choose(document.querySelector('select'), 's1')
    await click(buttonByText(document.body, 'Create repository'))
    await waitFor(() => text().includes('Requires the Own storage add-on'))
    await click(buttonByText(document.body, 'Create repository'))
    await waitFor(() => text().includes('billing owner can choose'))
    expect(text()).not.toContain('Requires the Own storage add-on')
    expect(text()).toContain('Could not create the repository')

    await choose(document.querySelector('select'), '')
    api.createProject.mockResolvedValue({ slug: 'game' } as never)
    await click(buttonByText(document.body, 'Create repository'))
    await waitFor(() => api.createProject.mock.calls.length === 3)
    expect(api.createProject).toHaveBeenLastCalledWith('acme', 'Game', '', 'game', undefined)
  })

  it('names an org default the user does not own', async () => {
    await newProject([server()], { id: 's9', name: 'Owner bucket' })
    await waitFor(() => document.querySelector('option[value=""]')?.textContent === 'Organization default (Owner bucket)')
  })

  it('offers no servers to an org owner who is not the billing owner', async () => {
    api.createProject.mockResolvedValue({ slug: 'game' } as never)
    const view = await newProject([server()], { id: 's9', name: 'Owner bucket' }, false)
    await waitFor(() => text().includes('stored on Owner bucket'))
    expect(document.querySelector('select')).toBeNull()
    expect(api.listStorageServers).not.toHaveBeenCalled()
    await click(buttonByText(document.body, 'Create repository'))
    await waitFor(() => view.location() === '/acme/game')
    expect(api.createProject).toHaveBeenCalledWith('acme', 'Game', '', 'game', undefined)
  })

  it('mentions the org default when the user has no servers', async () => {
    await newProject([], { id: 's9', name: 'Owner bucket' })
    await waitFor(() => text().includes('stored on Owner bucket'))
    expect(document.querySelector('select')).toBeNull()
  })
})
