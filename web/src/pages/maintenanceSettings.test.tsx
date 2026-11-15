import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import { resetMaintenance, storeProject } from '../maintenance'
import { renderRoute, waitFor } from '../test/render'
import AdminMaintenancePage from './AdminMaintenancePage'
import MergeRequestsPage from './MergeRequestsPage'
import OrgSettingsPage from './OrgSettingsPage'
import ProjectSettingsPage from './ProjectSettingsPage'

vi.mock('../api/endpoints')

const auth = vi.hoisted(() => ({ me: null as Record<string, unknown> | null }))
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me }) }))

const api = vi.mocked(endpoints)
const text = () => document.body.textContent ?? ''
const views: { unmount: () => Promise<void> }[] = []

async function show(pattern: string, url: string, node: React.ReactNode) {
  const view = await renderRoute(pattern, url, node)
  views.push(view)
  return view
}

const WINDOW = { mode: 'read_only' as const, active: true, message: 'backup', starts_at: '2030-01-01T10:00:00Z' }

beforeEach(() => {
  resetMaintenance()
  auth.me = { id: '1', name: 'Alice', is_admin: false, is_super_admin: false }
  api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'acme', name: 'Acme', role: 'owner' }])
  api.getOrgStorage.mockRejectedValue(new Error('not found'))
  api.listOrgMembers.mockResolvedValue([])
  api.listGroups.mockResolvedValue([])
  api.listBranches.mockResolvedValue([])
  api.listProjectRules.mockResolvedValue([])
  api.listProjectDefaults.mockResolvedValue([])
  api.getMyProjectPermissions.mockResolvedValue({
    project_permission: PERMISSION_READ | PERMISSION_WRITE | PERMISSION_ADMIN,
    rules: [],
    defaults: [],
  } as never)
})

afterEach(async () => {
  while (views.length) await views.pop()?.unmount()
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('AdminMaintenancePage', () => {
  it('refuses users who are not super admins', async () => {
    await show('/admin/maintenance', '/admin/maintenance', <AdminMaintenancePage />)
    expect(text()).toContain('Only super admins')
    expect(api.getAdminMaintenance).not.toHaveBeenCalled()
  })

  it('shows the server form and the org and project windows', async () => {
    auth.me = { id: '1', is_super_admin: true }
    api.getAdminMaintenance.mockResolvedValue({
      server: null,
      source: 'db',
      orgs: [{ ...WINDOW, org: 'acme' }],
      projects: [{ mode: 'full', active: false, message: '', org: 'acme', project: 'acme/game', ends_at: '2030-01-02T10:00:00Z' }],
    })
    await show('/admin/maintenance', '/admin/maintenance', <AdminMaintenancePage />)
    await waitFor(() => text().includes('Organization windows'))
    expect(text()).toContain('Schedule maintenance')
    expect(text()).toContain('Email organization owners')
    expect(document.querySelector('option[value="frozen"]')).not.toBeNull()
    expect(document.querySelector('a[href="/acme/settings?tab=maintenance"]')).not.toBeNull()
    expect(document.querySelector('a[href="/acme/game/settings?tab=maintenance"]')).not.toBeNull()
    expect(text()).toContain('Scheduled')
    expect(text()).toContain('estimated end')
  })

  it('reports a load failure', async () => {
    auth.me = { id: '1', is_super_admin: true }
    api.getAdminMaintenance.mockRejectedValue(new Error('forbidden'))
    await show('/admin/maintenance', '/admin/maintenance', <AdminMaintenancePage />)
    await waitFor(() => text().includes('forbidden'))
  })
})

describe('OrgSettingsPage maintenance tab', () => {
  it('lets owners manage the org window and lists project windows', async () => {
    api.getOrgMaintenance.mockResolvedValue({
      server: { mode: 'full', active: false, message: 'upgrade' },
      org: { ...WINDOW, org: 'acme' },
      projects: [],
    })
    api.setOrgMaintenance.mockResolvedValue({ ...WINDOW, notified: true })
    await show('/:org/settings', '/acme/settings?tab=maintenance', <OrgSettingsPage />)
    await waitFor(() => text().includes('Project windows'))
    expect(text()).toContain('Organization acme is read-only for maintenance')
    expect(text()).toContain('The server has maintenance scheduled')
    expect(text()).toContain('No project in this organization')
    expect(document.querySelector('option[value="frozen"]')).toBeNull()
    expect(text()).toContain('Email organization owners and project admins')
  })

  it('hides the tab from members who are not owners', async () => {
    auth.me = { id: '1', is_admin: true, is_super_admin: false }
    api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'acme', name: 'Acme', role: 'member' }])
    await show('/:org/settings', '/acme/settings?tab=maintenance', <OrgSettingsPage />)
    await waitFor(() => api.listOrgMembers.mock.calls.length > 0)
    expect(text()).not.toContain('Maintenance window')
    expect(api.getOrgMaintenance).not.toHaveBeenCalled()
  })

  it('reports a load failure', async () => {
    api.getOrgMaintenance.mockRejectedValue(new Error('denied'))
    await show('/:org/settings', '/acme/settings?tab=maintenance', <OrgSettingsPage />)
    await waitFor(() => text().includes('denied'))
  })
})

describe('ProjectSettingsPage maintenance tab', () => {
  it('shows the covering windows and the project form', async () => {
    api.getProjectMaintenance.mockResolvedValue({
      server: null,
      org: { ...WINDOW, org: 'acme' },
      project: null,
    })
    api.listProjectRules.mockRejectedValue(new Error('rules unavailable'))
    await show('/:org/:project/settings', '/acme/game/settings?tab=maintenance', <ProjectSettingsPage />)
    await waitFor(() => text().includes('Maintenance window'))
    expect(text()).toContain('Organization acme is read-only')
    expect(text()).toContain('No maintenance is scheduled')
    expect(text()).not.toContain('rules unavailable')
  })

  it('reports a load failure', async () => {
    api.getProjectMaintenance.mockRejectedValue(new Error('denied'))
    await show('/:org/:project/settings', '/acme/game/settings?tab=maintenance', <ProjectSettingsPage />)
    await waitFor(() => text().includes('denied'))
  })
})

describe('MergeRequestsPage during maintenance', () => {
  it('disables New merge request while writes are paused', async () => {
    api.listMergeRequests.mockResolvedValue({ merge_requests: [], next_cursor: '' } as never)
    await show('/:org/:project/merges', '/acme/game/merges', <MergeRequestsPage />)
    const button = () => [...document.querySelectorAll('button')].find((item) => item.textContent?.includes('New merge request'))
    await waitFor(() => button() !== undefined)
    expect(button()?.disabled).toBe(false)
    await act(async () => storeProject('acme/game', WINDOW))
    expect(button()?.disabled).toBe(true)
    expect(button()?.getAttribute('title')).toContain('Project acme/game is read-only')
  })
})
