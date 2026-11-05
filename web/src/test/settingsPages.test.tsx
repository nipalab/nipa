import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
    await waitFor(() => document.body.textContent?.includes('Selected: Artists') ?? false)
    await type(document.body.querySelector<HTMLInputElement>('input[placeholder^="e.g. assets"]'), 'art')
    await click(document.body.querySelector('[role="dialog"] input[type="checkbox"]'))
    await click(submit())
    await flush()
    expect(api.createProjectRule).toHaveBeenCalledWith('acme', 'game', '', 'g7', 'art', PERMISSION_READ)

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
    expect(submit()?.disabled).toBe(true)
    await type(document.body.querySelector<HTMLInputElement>('input[placeholder^="e.g. assets"]'), 'docs')
    await click(document.body.querySelector('[role="dialog"] input[type="checkbox"]'))
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

  it('edits branch protection settings', async () => {
    api.listBranches.mockResolvedValue([
      branch({
        is_protected: true,
        required_approvals: 1,
        required_checks: ['ci'],
        required_reviewers: [{ user_id: '2', name: 'Dana' }],
      }),
    ])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => container.querySelector('input[aria-label="Required approvals for main"]') !== null)
    const lastCall = () => api.setBranchProtection.mock.calls[api.setBranchProtection.mock.calls.length - 1]

    const approvals = container.querySelector<HTMLInputElement>('input[aria-label="Required approvals for main"]')
    await type(approvals, '3')
    await act(async () => {
      approvals?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    await flush()
    expect(lastCall()).toEqual(['acme', 'game', 'main', true, 3, undefined, undefined, undefined, undefined])

    await click(container.querySelector('input[aria-label="Dismiss stale approvals on push for main"]'))
    await flush()
    expect(lastCall()?.[5]).toBe(false)

    await click(container.querySelector('input[aria-label="Require status checks for main"]'))
    await flush()
    expect(lastCall()?.[6]).toBe(true)

    const checks = container.querySelector<HTMLInputElement>('input[aria-label="Required checks for main"]')
    await type(checks, 'ci, lint ,')
    await act(async () => {
      checks?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    await flush()
    expect(lastCall()?.[8]).toEqual(['ci', 'lint'])

    await choose(container.querySelector<HTMLSelectElement>('select[aria-label="Add required reviewer for main"]'), '3')
    await click(buttonByText(container, 'Add'))
    await flush()
    expect(lastCall()?.[7]).toEqual(['2', '3'])

    const removeReviewer = [...container.querySelectorAll<HTMLElement>('[aria-labelledby]')].find((element) =>
      (element.getAttribute('aria-labelledby') ?? '')
        .split(' ')
        .some((id) => document.getElementById(id)?.textContent === 'remove required reviewer Dana'),
    )
    await click(removeReviewer)
    await flush()
    expect(lastCall()?.[7]).toEqual([])
  })

  it('keeps unchanged approval and check values without saving', async () => {
    api.listBranches.mockResolvedValue([branch({ required_approvals: 2, required_checks: ['ci'] })])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => container.querySelector('input[aria-label="Required approvals for main"]') !== null)
    const approvals = container.querySelector<HTMLInputElement>('input[aria-label="Required approvals for main"]')
    await act(async () => {
      approvals?.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
    })
    const checks = container.querySelector<HTMLInputElement>('input[aria-label="Required checks for main"]')
    await act(async () => {
      checks?.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
    })
    await type(approvals, 'abc')
    await act(async () => {
      approvals?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', false, 0, undefined, undefined, undefined, undefined)
  })

  it('confirms before unprotecting a branch from the toggle and shows empty branches', async () => {
    api.listBranches.mockResolvedValue([branch({ is_protected: true })])
    const { container } = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => container.querySelector('button[aria-labelledby="branch-protection-0"]') !== null)
    await click(container.querySelector('button[aria-labelledby="branch-protection-0"]'))
    await cancelDialog()
    expect(api.setBranchProtection).not.toHaveBeenCalled()
    await click(container.querySelector('button[aria-labelledby="branch-protection-0"]'))
    await confirmDialog('Unprotect')
    expect(api.setBranchProtection.mock.calls[0]?.[3]).toBe(false)

    api.listBranches.mockResolvedValue([])
    const empty = await renderRoute(pattern, `${base}?tab=branches`, <ProjectSettingsPage />)
    await waitFor(() => (empty.container.textContent ?? '').includes('No branches yet.'))
  })

  it('renders the webhooks and email tabs', async () => {
    const hooks = await renderRoute(pattern, `${base}?tab=webhooks`, <ProjectSettingsPage />)
    await waitFor(() => (hooks.container.textContent ?? '').includes('Create webhook'))
    const email = await renderRoute(pattern, `${base}?tab=email`, <ProjectSettingsPage />)
    await waitFor(() => (email.container.textContent ?? '').includes('No email deliveries.'))
  })
})
