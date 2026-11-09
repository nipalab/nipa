import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import * as endpoints from '../api/endpoints'
import type { BranchResponse, GroupResponse, OrgMemberResponse } from '../api/models'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import OrgSettingsPage from '../pages/OrgSettingsPage'
import ProjectSettingsPage from '../pages/ProjectSettingsPage'
import {
  buttonByText,
  buttonsByText,
  cancelDialog,
  choose,
  click,
  confirmDialog,
  flush,
  renderRoute,
  type,
  waitFor,
} from './render'

vi.mock('../api/endpoints')
vi.mock('../components/UserGroupPicker', () => ({
  UserGroupPicker: ({
    onSelect,
    kind,
  }: {
    onSelect: (entry: { id: string; label: string; sublabel: string; isGroup: boolean }) => void
    kind: string
  }) => (
    <button
      type="button"
      onClick={() =>
        onSelect(
          kind === 'user-or-group'
            ? { id: 'g7', label: 'Artists', sublabel: 'Group', isGroup: true }
            : { id: '9', label: 'Nina', sublabel: 'nina@example.com', isGroup: false },
        )
      }
    >
      pick subject
    </button>
  ),
}))

const auth = vi.hoisted(() => ({ me: null as Record<string, unknown> | null }))
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me }) }))

const api = vi.mocked(endpoints)

const SEAT_LIMIT = new ApiError('seat limit reached: 3 of 3 seats in use; upgrade the plan to add more people', 402)

async function expectSeatLimitBanner(location: () => string | null) {
  await waitFor(() => document.body.textContent?.includes('Seat limit reached') ?? false)
  const dialog = document.querySelector('[role="dialog"]') as HTMLElement
  expect(dialog.textContent).toContain('3 of 3 seats in use')
  expect(dialog.textContent).not.toContain('Could not save')
  const link = dialog.querySelector<HTMLAnchorElement>('a[href="/settings/billing"]')
  expect(link).not.toBeNull()
  await click(link)
  await waitFor(() => location() === '/settings/billing')
}

function member(overrides: Partial<OrgMemberResponse> = {}): OrgMemberResponse {
  return {
    user_id: '2',
    name: 'Dana',
    email: 'dana@example.com',
    photo_url: '',
    is_admin: false,
    is_super_admin: false,
    role: 'member',
    ...overrides,
  }
}

const GROUP: GroupResponse = {
  id: 'g1',
  org_id: 'o1',
  name: 'Coder',
  description: 'coders',
  member_count: 1,
  member_ids: ['2'],
  members: [{ user_id: '2', name: 'Dana', email: 'dana@example.com' }],
}

function byName(root: ParentNode, name: string): HTMLElement | null {
  return (
    [...root.querySelectorAll<HTMLElement>('[aria-label], [aria-labelledby]')].find(
      (element) =>
        element.getAttribute('aria-label') === name ||
        (element.getAttribute('aria-labelledby') ?? '')
          .split(' ')
          .some((id) => document.getElementById(id)?.textContent === name),
    ) ?? null
  )
}

function branch(overrides: Partial<BranchResponse> = {}): BranchResponse {
  return {
    id: 'b1',
    name: 'main',
    is_default: true,
    is_protected: false,
    required_approvals: 0,
    dismiss_stale_approvals: true,
    require_status_checks: false,
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  auth.me = { id: '1', is_admin: false, is_super_admin: false }
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('OrgSettingsPage', () => {
  const pattern = '/:org/settings'
  const url = '/sticker/settings'

  beforeEach(() => {
    api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'sticker', name: 'Sticker', role: 'owner' }])
    api.listOrgMembers.mockResolvedValue([member(), member({ user_id: '3', name: 'Eve', email: 'eve@example.com', role: 'owner', is_admin: true })])
    api.listGroups.mockResolvedValue([GROUP])
    api.getGroup.mockResolvedValue(GROUP)
    api.getOrgStorage.mockRejectedValue(new Error('not found'))
  })

  it('refuses plain members', async () => {
    api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'sticker', name: 'Sticker', role: 'member' }])
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('Only organization owners'))
  })

  it('lists members with role labels', async () => {
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('Eve'))
    expect(container.textContent).toContain('Owner')
    expect(container.textContent).toContain('Admin')
    expect(container.textContent).not.toContain('Storage')
  })

  it('changes a role after confirmation and can cancel', async () => {
    api.updateOrgMember.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => buttonsByText(container, 'Make owner').length === 1)

    await click(buttonByText(container, 'Make owner'))
    await cancelDialog()
    expect(api.updateOrgMember).not.toHaveBeenCalled()

    await click(buttonByText(container, 'Make owner'))
    await confirmDialog('Make owner')
    expect(api.updateOrgMember).toHaveBeenCalledWith('sticker', '2', 'owner')

    await click(buttonByText(container, 'Make member'))
    await confirmDialog('Make member')
    expect(api.updateOrgMember).toHaveBeenCalledWith('sticker', '3', 'member')
  })

  it('removes a member after confirmation and reports failures', async () => {
    api.removeOrgMember.mockRejectedValueOnce(new Error('last owner')).mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => buttonsByText(container, 'Remove').length === 2)
    await click(buttonsByText(container, 'Remove')[0])
    await confirmDialog('Remove')
    await waitFor(() => (container.textContent ?? '').includes('last owner'))
    expect(api.removeOrgMember).toHaveBeenCalledWith('sticker', '2')
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(container.textContent).not.toContain('last owner')
  })

  it('adds a member with a role', async () => {
    api.addOrgMember.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => buttonByText(container, 'Add member') !== null)
    await click(buttonByText(container, 'Add member'))
    await waitFor(() => document.body.textContent?.includes('Role') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await click(buttonByText(document.body, 'pick subject'))
    await choose(document.body.querySelector<HTMLSelectElement>('[role="dialog"] select'), 'owner')
    await click(submit())
    await flush()
    expect(api.addOrgMember).toHaveBeenCalledWith('sticker', '9', 'owner')
  })

  it('explains a seat-limit 402 when adding a member', async () => {
    api.addOrgMember.mockRejectedValue(SEAT_LIMIT)
    const { container, location } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => buttonByText(container, 'Add member') !== null)
    await click(buttonByText(container, 'Add member'))
    await waitFor(() => document.body.textContent?.includes('Role') ?? false)
    await click(buttonByText(document.body, 'pick subject'))
    await click(document.body.querySelector('button[type="submit"]'))
    await expectSeatLimitBanner(location)
  })

  it('explains a seat-limit 402 when adding a group member', async () => {
    api.addGroupMember.mockRejectedValue(SEAT_LIMIT)
    const { container, location } = await renderRoute(pattern, `${url}?tab=groups`, <OrgSettingsPage />)
    await waitFor(() => buttonByText(container, 'View') !== null)
    await click(buttonByText(container, 'View'))
    await waitFor(() => (container.textContent ?? '').includes('Coder members'))
    const addButtons = buttonsByText(container, 'Add member')
    await click(addButtons[addButtons.length - 1])
    await waitFor(() => document.body.textContent?.includes('Add group member') ?? false)
    await click(buttonByText(document.body, 'pick subject'))
    await click(document.body.querySelector('button[type="submit"]'))
    await expectSeatLimitBanner(location)
  })

  it('keeps other 402s as a plain error', async () => {
    api.addOrgMember.mockRejectedValue(new ApiError('storage quota exceeded', 402))
    const { container } = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => buttonByText(container, 'Add member') !== null)
    await click(buttonByText(container, 'Add member'))
    await waitFor(() => document.body.textContent?.includes('Role') ?? false)
    await click(buttonByText(document.body, 'pick subject'))
    await click(document.body.querySelector('button[type="submit"]'))
    await waitFor(() => document.body.textContent?.includes('Could not save') ?? false)
    expect(document.body.textContent).not.toContain('Seat limit reached')
  })

  it('manages groups: create, view, add and remove a member', async () => {
    api.createGroup.mockResolvedValue(GROUP)
    api.addGroupMember.mockResolvedValue(undefined as never)
    api.removeGroupMember.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, `${url}?tab=groups`, <OrgSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('Coder'))

    await click(buttonByText(container, 'Create group'))
    await waitFor(() => document.body.textContent?.includes('Group name') ?? false)
    const inputs = document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input')
    await type(inputs[0], 'artists')
    await type(inputs[1], 'draw things')
    await click(document.body.querySelector('button[type="submit"]'))
    await flush()
    expect(api.createGroup).toHaveBeenCalledWith('sticker', 'artists', 'draw things')

    await click(buttonByText(container, 'View'))
    await waitFor(() => (container.textContent ?? '').includes('Coder members'))
    await click(buttonByText(container, 'Hide'))
    await waitFor(() => !(container.textContent ?? '').includes('Coder members'))
    await click(buttonByText(container, 'View'))
    await waitFor(() => (container.textContent ?? '').includes('dana@example.com'))

    const addButtons = buttonsByText(container, 'Add member')
    await click(addButtons[addButtons.length - 1])
    await waitFor(() => document.body.textContent?.includes('Add group member') ?? false)
    await click(buttonByText(document.body, 'pick subject'))
    await click(document.body.querySelector('button[type="submit"]'))
    await flush()
    expect(api.addGroupMember).toHaveBeenCalledWith('sticker', 'g1', '9')

    const memberRemove = buttonsByText(container, 'Remove')[0]
    await click(memberRemove)
    await confirmDialog('Remove')
    expect(api.removeGroupMember).toHaveBeenCalledWith('sticker', 'g1', '2')
  })

  it('shows empty group and member states', async () => {
    api.listGroups.mockResolvedValue([])
    api.listOrgMembers.mockResolvedValue([])
    const groups = await renderRoute(pattern, `${url}?tab=groups`, <OrgSettingsPage />)
    await waitFor(() => (groups.container.textContent ?? '').includes('No groups.'))
    const members = await renderRoute(pattern, url, <OrgSettingsPage />)
    await waitFor(() => (members.container.textContent ?? '').includes('No members.'))
  })

  it('shows group detail with no members', async () => {
    api.getGroup.mockResolvedValue({ ...GROUP, members: [], member_ids: [], member_count: 0 })
    const { container } = await renderRoute(pattern, `${url}?tab=groups`, <OrgSettingsPage />)
    await waitFor(() => buttonByText(container, 'View') !== null)
    await click(buttonByText(container, 'View'))
    await waitFor(() => (container.textContent ?? '').includes('Coder members'))
    expect(container.textContent).toContain('No members.')
  })

  it('offers the storage tab when the ledger is enabled', async () => {
    api.getOrgStorage.mockResolvedValue({ used_bytes: 1024, quota_bytes: null, owner_id: '1', projects: [] })
    const { container } = await renderRoute(pattern, `${url}?tab=storage`, <OrgSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('unlimited quota'))
    expect(container.textContent).toContain('Storage')
  })

  it('falls back to members for an unknown or unavailable tab', async () => {
    const { container } = await renderRoute(pattern, `${url}?tab=storage`, <OrgSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('Dana'))
  })
})

describe('ProjectSettingsPage', () => {
  const pattern = '/:org/:project/settings'
  const base = '/acme/game/settings'
  const url = `${base}?tab=access`

  function allow(mask: number) {
    api.getMyProjectPermissions.mockResolvedValue({ project_permission: mask, rules: [], defaults: [] })
  }

  beforeEach(() => {
    allow(PERMISSION_READ | PERMISSION_WRITE | PERMISSION_ADMIN)
    api.listBranches.mockResolvedValue([branch(), branch({ id: 'b2', name: 'dev', is_default: false })])
    api.listProjectRules.mockResolvedValue([])
    api.listProjectDefaults.mockResolvedValue([])
    api.listOrgMembers.mockResolvedValue([member(), member({ user_id: '3', name: 'Eve', email: 'eve@example.com' })])
    api.listWebhooks.mockResolvedValue([])
    api.listEmailDeliveries.mockResolvedValue({ deliveries: [], next_cursor: '' } as never)
    api.setBranchProtection.mockResolvedValue(branch())
  })

  it('refuses users without admin permission', async () => {
    allow(PERMISSION_READ)
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('project admin permission'))
  })

  it('shows empty states for rules and defaults', async () => {
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('No access rules'))
    expect(container.textContent).toContain('No path defaults')
  })

  it('adds an access rule for a group, then revokes rules after confirmation', async () => {
    api.listProjectRules.mockResolvedValue([
      { id: 1, user_id: '2', user_name: 'Dana', user_email: 'dana@example.com', path_prefix: 'assets', permission: 3 },
      { id: 2, group_id: 'g1', group_name: 'Coder', path_prefix: '', permission: 1 },
      { id: 3, user_id: '9', path_prefix: 'x', permission: 1 },
    ])
    api.createProjectRule.mockResolvedValue(undefined as never)
    api.deleteProjectRule.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('Coder'))

    await click(buttonByText(container, 'Add access rule'))
    await waitFor(() => document.body.textContent?.includes('User or group') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await click(buttonByText(document.body, 'pick subject'))
    const picked = () => document.body.querySelector('[aria-label="Selected user or group"]')
    await waitFor(() => picked() !== null)
    expect(picked()?.textContent).toContain('Artists')
    expect(picked()?.textContent).toContain('Group')
    await click(buttonByText(picked() as HTMLElement, 'Change'))
    expect(picked()).toBeNull()
    await click(buttonByText(document.body, 'pick subject'))
    await waitFor(() => picked() !== null)
    await type(document.body.querySelector<HTMLInputElement>('input[placeholder^="e.g. assets"]'), 'art')
    const boxes = () => document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input[type="checkbox"]')
    expect(boxes()[0].checked).toBe(true)
    await click(boxes()[1])
    await click(submit())
    await flush()
    expect(api.createProjectRule).toHaveBeenCalledWith('acme', 'game', '', 'g7', 'art', PERMISSION_READ | PERMISSION_WRITE)

    await click(buttonsByText(container, 'Revoke')[0])
    await confirmDialog('Revoke')
    expect(api.deleteProjectRule).toHaveBeenCalledWith('acme', 'game', 1)
    await click(buttonsByText(container, 'Revoke')[1])
    await confirmDialog('Revoke')
    expect(api.deleteProjectRule).toHaveBeenCalledWith('acme', 'game', 2)
    await click(buttonsByText(container, 'Revoke')[2])
    await confirmDialog('Revoke')
    expect(api.deleteProjectRule).toHaveBeenCalledWith('acme', 'game', 3)
  })

  it('adds, edits and removes path defaults', async () => {
    api.listProjectDefaults.mockResolvedValue([{ path_prefix: 'assets/textures', permission: 3 }])
    api.setProjectDefault.mockResolvedValue(undefined as never)
    api.deleteProjectDefault.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/textures'))

    await click(buttonByText(container, 'Add path default'))
    await waitFor(() => document.body.textContent?.includes('Add default') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(document.body.textContent).toContain('Default permissions')
    const readBox = () => document.body.querySelector<HTMLInputElement>('[role="dialog"] input[type="checkbox"]')
    await click(readBox())
    expect(submit()?.disabled).toBe(true)
    await click(readBox())
    await type(document.body.querySelector<HTMLInputElement>('input[placeholder^="e.g. assets"]'), 'docs')
    await click(submit())
    await flush()
    expect(api.setProjectDefault).toHaveBeenCalledWith('acme', 'game', 'docs', PERMISSION_READ)

    await click(buttonByText(container, 'Edit'))
    await waitFor(() => document.body.textContent?.includes('Edit path default') ?? false)
    await click(submit())
    await flush()
    expect(api.setProjectDefault).toHaveBeenCalledWith('acme', 'game', 'assets/textures', 3)

    await click(buttonByText(container, 'Remove'))
    await confirmDialog('Remove')
    expect(api.deleteProjectDefault).toHaveBeenCalledWith('acme', 'game', 'assets/textures')
  })

  it('reports failures from access changes', async () => {
    api.listProjectRules.mockResolvedValue([{ id: 1, user_id: '2', user_name: 'Dana', path_prefix: '', permission: 1 }])
    api.deleteProjectRule.mockRejectedValue(new Error('rule locked'))
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => buttonByText(container, 'Revoke') !== null)
    await click(buttonByText(container, 'Revoke'))
    await confirmDialog('Revoke')
    await waitFor(() => (container.textContent ?? '').includes('rule locked'))
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(container.textContent).not.toContain('rule locked')
  })

  it('lists protection rules and edits one in the rule form', async () => {
    api.listBranches.mockResolvedValue([
      branch({
        is_protected: true,
        required_approvals: 1,
        require_status_checks: true,
        required_checks: ['ci'],
        required_reviewers: [{ user_id: '2', name: 'Dana' }],
      }),
    ])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() =>
      (container.textContent ?? '').includes('1 approval required · stale approvals dismissed · 1 required reviewer · 1 required check'),
    )

    await click(buttonByText(container, 'Edit'))
    await waitFor(() => (container.textContent ?? '').includes('Edit protection for main'))
    const field = (selector: string) => container.querySelector(selector) as HTMLInputElement
    expect(field('#protect-approvals').checked).toBe(true)
    expect(field('#protect-reviewers').checked).toBe(true)
    expect(field('#protect-checks').checked).toBe(true)

    await choose(container.querySelector<HTMLSelectElement>('select[aria-label="Add required reviewer"]'), '3')
    await click(buttonByText(container, 'Add reviewer'))
    await click(byName(container, 'Remove required reviewer Dana'))

    await choose(
      [...container.querySelectorAll<HTMLSelectElement>('select')].find((select) => select.value === '1') ?? null,
      '3',
    )
    await click(field('#protect-dismiss-stale'))

    const checkInput = container.querySelector<HTMLInputElement>('input[aria-label="Status check name"]')
    await type(checkInput, 'lint')
    await act(async () => {
      checkInput?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    await type(checkInput, 'ci')
    await click(buttonByText(container, 'Add check'))
    await type(checkInput, 'e2e')
    await click(buttonByText(container, 'Add check'))
    await click(byName(container, 'Remove required check e2e'))

    await click(buttonByText(container, 'Save changes'))
    await flush()
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', true, 3, false, true, ['3'], ['ci', 'lint'])
    await waitFor(() => (container.textContent ?? '').includes('Add rule'))
  })

  it('adds a rule for an unprotected branch and keeps the form open on failure', async () => {
    api.listBranches.mockResolvedValue([branch(), branch({ id: 'b2', name: 'dev', is_default: false })])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('No branch protection rules yet'))

    await click(buttonByText(container, 'Add rule'))
    await waitFor(() => (container.textContent ?? '').includes('Add branch protection rule'))
    await click(buttonByText(container, 'Cancel'))
    await waitFor(() => (container.textContent ?? '').includes('No branch protection rules yet'))

    await click(buttonByText(container, 'Add rule'))
    const branchSelect = [...container.querySelectorAll<HTMLSelectElement>('select')].find((select) => select.value === 'main')
    expect(branchSelect).toBeDefined()
    await choose(branchSelect, 'dev')
    expect((container.querySelector('#protect-approvals') as HTMLInputElement).checked).toBe(false)
    await click(container.querySelector('#protect-approvals'))
    await click(buttonByText(container, 'Create'))
    await flush()
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'dev', true, 1, true, false, [], [])

    api.setBranchProtection.mockRejectedValueOnce(new Error('branch is busy'))
    await click(buttonByText(container, 'Add rule'))
    await click(buttonByText(container, 'Create'))
    await waitFor(() => (container.textContent ?? '').includes('branch is busy'))
    expect(api.setBranchProtection).toHaveBeenLastCalledWith('acme', 'game', 'main', true, 0, true, false, [], [])
    expect(container.textContent).toContain('Add branch protection rule')
  })

  it('confirms before deleting a rule and disables adding when every branch is protected', async () => {
    api.listBranches.mockResolvedValue([branch({ is_protected: true })])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => byName(container, 'Delete protection rule for main') !== null)
    expect(container.textContent).toContain('Merge requests only')
    expect(buttonByText(container, 'Add rule')?.disabled).toBe(true)

    await click(byName(container, 'Delete protection rule for main'))
    await cancelDialog()
    expect(api.setBranchProtection).not.toHaveBeenCalled()
    await click(byName(container, 'Delete protection rule for main'))
    await confirmDialog('Delete rule')
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', false, 0, true, false, [], [])
  })

  it('explains a seat-limit 402 when a new rule would add a user', async () => {
    api.createProjectRule.mockRejectedValue(SEAT_LIMIT)
    const { container, location } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => buttonByText(container, 'Add access rule') !== null)
    await click(buttonByText(container, 'Add access rule'))
    await waitFor(() => document.body.textContent?.includes('User or group') ?? false)
    await click(buttonByText(document.body, 'pick subject'))
    await click(document.body.querySelector('button[type="submit"]'))
    await expectSeatLimitBanner(location)
  })

  it('renders the webhooks and email tabs', async () => {
    const hooks = await renderRoute(pattern, `${base}?tab=webhooks`, <ProjectSettingsPage />)
    await waitFor(() => (hooks.container.textContent ?? '').includes('Create webhook'))
    const email = await renderRoute(pattern, `${base}?tab=email`, <ProjectSettingsPage />)
    await waitFor(() => (email.container.textContent ?? '').includes('No email deliveries.'))
  })

  it('filters email deliveries by state from the state menu', async () => {
    const { container } = await renderRoute(pattern, `${base}?tab=email`, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('No email deliveries.'))
    expect(api.listEmailDeliveries).toHaveBeenLastCalledWith('acme', 'game', '')
    const menuButton = () => byName(container, 'Filter by delivery state') as HTMLButtonElement
    expect(menuButton().textContent).toContain('State: All states')

    await click(menuButton())
    await waitFor(() => document.querySelector('[role="menuitemradio"]') !== null)
    const options = [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')]
    expect(options.map((option) => option.textContent)).toEqual(['All states', 'Failed', 'Pending', 'Delivered'])
    expect(options[0].getAttribute('aria-checked')).toBe('true')
    await click(options[1])
    await flush()

    expect(api.listEmailDeliveries).toHaveBeenLastCalledWith('acme', 'game', 'failed')
    expect(menuButton().textContent).toContain('State: Failed')
  })
})
