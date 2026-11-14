import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import type { BranchResponse, FileLockResponse } from '../api/models'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import BranchesPage from '../pages/BranchesPage'
import LocksPage from '../pages/LocksPage'
import {
  buttonByText,
  cancelDialog,
  click,
  confirmDialog,
  flush,
  renderRoute,
  type,
  waitFor,
} from './render'

vi.mock('../api/endpoints')
vi.mock('../auth', () => ({ useAuth: () => ({ me: { id: '1', is_admin: false, is_super_admin: false } }) }))

const api = vi.mocked(endpoints)

function branch(overrides: Partial<BranchResponse> = {}): BranchResponse {
  return {
    id: 'b1',
    name: 'main',
    is_default: true,
    is_protected: false,
    required_approvals: 0,
    dismiss_stale_approvals: true,
    require_status_checks: false,
    commit_id: 'abcdef0123456789',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function lock(overrides: Partial<FileLockResponse> = {}): FileLockResponse {
  return {
    id: 'l1',
    path: 'assets/orc.png',
    global: true,
    held_by: '2',
    held_by_name: 'Dana',
    acquired_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function allow(mask: number) {
  api.getMyProjectPermissions.mockResolvedValue({ project_permission: mask, rules: [], defaults: [] })
}

beforeEach(() => {
  api.listBranches.mockResolvedValue([branch(), branch({ id: 'b2', name: 'feature', is_default: false })])
  allow(PERMISSION_READ | PERMISSION_WRITE | PERMISSION_ADMIN)
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

async function menuItem(anchor: string, label: string) {
  await click(byName(anchor))
  await waitFor(() => document.querySelector('[role="menuitem"]') !== null)
  const item = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((entry) => entry.textContent?.includes(label))
  if (!item) throw new Error(`menu item ${label} not found`)
  await click(item)
}

describe('LocksPage', () => {
  const url = '/acme/game/locks'
  const pattern = '/:org/:project/locks'

  it('lists locks with scope, holder, merge request and expiry', async () => {
    const expires = new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString()
    api.listFileLocks.mockResolvedValue([
      lock(),
      lock({ id: 'l2', path: 'assets/hero.fbx', global: false, branch: 'feature', merge_request_number: 7, held_by_name: '', expires_at: expires }),
      lock({ id: 'l3', path: 'assets/textures', held_by: '1', held_by_name: 'Me' }),
    ])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    const text = container.textContent ?? ''
    expect(text).toContain('Mainline')
    expect(text).toContain('Dana locked')
    expect(text).toContain('feature')
    expect(text).toContain('MR #7')
    expect(text).toContain('expires in 2 hours')
    expect(text).toContain('You locked')
    expect(container.querySelector('a[href="/acme/game/blob/main/assets/orc.png"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/blob/feature/assets/hero.fbx"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/tree/main/assets/textures"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/merges/7"]')).not.toBeNull()
  })

  it('shows the empty message when there are no locks', async () => {
    api.listFileLocks.mockResolvedValue([])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('No locks.'))
  })

  it('unlocks your own lock and force unlocks others as an admin', async () => {
    api.listFileLocks.mockResolvedValue([
      lock({ held_by: '1', held_by_name: 'Me' }),
      lock({ id: 'l2', path: 'assets/hero.fbx', global: false, branch: 'feature' }),
    ])
    api.unlockFile.mockResolvedValue({ message: 'ok' })
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => buttonByText(container, 'Unlock') !== null)

    await click(buttonByText(container, 'Unlock'))
    await confirmDialog('Unlock')
    expect(api.unlockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', '')

    await click(buttonByText(container, 'Force unlock'))
    await confirmDialog('Force unlock')
    expect(api.unlockFile).toHaveBeenCalledWith('acme', 'game', 'assets/hero.fbx', 'feature')
  })

  it('keeps the lock when the confirmation is cancelled', async () => {
    api.listFileLocks.mockResolvedValue([lock({ held_by: '1' })])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => buttonByText(container, 'Unlock') !== null)
    await click(buttonByText(container, 'Unlock'))
    await cancelDialog()
    expect(api.unlockFile).not.toHaveBeenCalled()
  })

  it('filters to my locks and by path', async () => {
    api.listFileLocks.mockResolvedValue([lock(), lock({ id: 'l3', path: 'docs/plan.psd', held_by: '1', held_by_name: 'Me' })])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    const tab = (label: string) =>
      [...container.querySelectorAll<HTMLButtonElement>('nav[aria-label="Lock filter"] button')].find((button) =>
        button.textContent?.startsWith(label),
      ) as HTMLButtonElement
    expect(tab('All locks').textContent).toBe('All locks2')
    await click(tab('Mine'))
    expect(container.textContent).not.toContain('assets/orc.png')
    expect(container.textContent).toContain('docs/plan.psd')
    await type(container.querySelector<HTMLInputElement>('[aria-label="Search locks"]'), 'nothing')
    expect(container.textContent).toContain('No locks match these filters.')
    await type(container.querySelector<HTMLInputElement>('[aria-label="Search locks"]'), '')
    api.listFileLocks.mockResolvedValue([lock()])
  })

  it('locks a path through the dialog, on mainline or a branch, and reports failures', async () => {
    api.listFileLocks.mockResolvedValue([])
    api.lockFile.mockResolvedValueOnce(lock()).mockResolvedValueOnce(lock()).mockRejectedValueOnce(new Error('already locked'))
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => buttonByText(container, 'Lock file') !== null)

    await click(buttonByText(container, 'Lock file'))
    await waitFor(() => document.body.textContent?.includes('Lock a file or folder') ?? false)
    expect(document.body.textContent).toContain('mainline lock')
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), '/assets/orc.png/')
    await click(submit())
    await flush()
    expect(api.lockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', '')

    await click(buttonByText(container, 'Lock file'))
    await waitFor(() => document.body.querySelector('[role="dialog"] input') !== null)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'assets/hero.fbx')
    await click(byName('Choose the lock branch'))
    await waitFor(() => document.querySelector('[role="option"]') !== null)
    await click([...document.querySelectorAll<HTMLElement>('[role="option"]')].find((option) => option.textContent?.includes('feature')))
    expect(document.body.textContent).toContain('only applies to pushes to feature')
    await click(submit())
    await flush()
    expect(api.lockFile).toHaveBeenLastCalledWith('acme', 'game', 'assets/hero.fbx', 'feature')

    await click(buttonByText(container, 'Lock file'))
    await waitFor(() => document.body.querySelector('[role="dialog"] input') !== null)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'assets/x.png')
    await click(submit())
    await waitFor(() => (document.body.textContent ?? '').includes('already locked'))
  })

  it('hides lock and unlock buttons for readers', async () => {
    allow(PERMISSION_READ)
    api.listFileLocks.mockResolvedValue([lock()])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    expect(buttonByText(container, 'Lock file')).toBeNull()
    expect(buttonByText(container, 'Force unlock')).toBeNull()
  })

  it('asks the holder to release a lock and shows the requests on the lock', async () => {
    api.listFileLocks.mockResolvedValue([
      lock({ requests: [{ id: 'q1', requester_id: '1', requester_name: 'Me', message: 'soon?', requested_at: '2026-01-02T00:00:00Z' }] }),
      lock({
        id: 'l2',
        path: 'assets/hero.fbx',
        requests: [
          { id: 'q2', requester_id: '5', requester_name: '', message: '', requested_at: '2026-01-02T00:00:00Z' },
          { id: 'q3', requester_id: '6', requester_name: 'Eve', message: 'please', requested_at: '2026-01-02T00:00:00Z' },
        ],
      }),
      lock({ id: 'l3', path: 'docs/mine.psd', held_by: '1', held_by_name: 'Me' }),
    ])
    api.requestLockRelease.mockResolvedValueOnce({} as never).mockRejectedValueOnce(new Error('too long'))
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    expect(container.textContent).toContain('1 release request')
    expect(container.textContent).toContain('2 release requests')
    const titles = [...container.querySelectorAll('[title]')].map((el) => el.getAttribute('title'))
    expect(titles).toContain('Me: soon?')
    expect(titles).toContain('5\nEve: please')
    expect(container.querySelectorAll('button[title="You already asked; ask again"]')).toHaveLength(1)
    expect(
      [...container.querySelectorAll('button')].filter((button) => button.textContent === 'Ask to release'),
    ).toHaveLength(2)

    await click(buttonByText(container, 'Ask to release'))
    await waitFor(() => (document.body.textContent ?? '').includes('Dana holds assets/orc.png'))
    await type(document.body.querySelector<HTMLTextAreaElement>('[role="dialog"] textarea'), '  need it ')
    await click(document.body.querySelector<HTMLButtonElement>('button[type="submit"]'))
    await flush()
    expect(api.requestLockRelease).toHaveBeenCalledWith('acme', 'game', 'l1', 'need it')

    await click(buttonByText(container, 'Ask to release'))
    await waitFor(() => document.body.querySelector('[role="dialog"] textarea') !== null)
    await click(document.body.querySelector<HTMLButtonElement>('button[type="submit"]'))
    await waitFor(() => (document.body.textContent ?? '').includes('too long'))
  })

  it('shows a load error', async () => {
    api.listFileLocks.mockRejectedValue(new Error('server down'))
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('server down'))
  })
})

describe('BranchesPage', () => {
  const url = '/acme/game/branches'
  const pattern = '/:org/:project/branches'

  it('lists the default branch apart from the others with labels and heads', async () => {
    api.listBranches.mockResolvedValue([
      branch({ is_protected: true }),
      branch({ id: 'b2', name: 'feature', is_default: false, commit_id: undefined }),
    ])
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('feature'))
    const defaults = container.querySelector('section[aria-label="Default branch"]') as HTMLElement
    const others = container.querySelector('section[aria-label="Other branches"]') as HTMLElement
    expect(defaults.textContent).toContain('main')
    expect(defaults.textContent).toContain('Default')
    expect(defaults.textContent).toContain('Protected')
    expect(defaults.textContent).toContain('abcdef0')
    expect(others.textContent).toContain('feature')
    expect(others.textContent).toContain('No commits yet')

    await type(container.querySelector<HTMLInputElement>('[aria-label="Search branches"]'), 'feat')
    expect(container.querySelector('section[aria-label="Default branch"]')).toBeNull()
    expect(container.textContent).toContain('Matching branches')
    await type(container.querySelector<HTMLInputElement>('[aria-label="Search branches"]'), 'zzz')
    expect(container.textContent).toContain('No branches match')
  })

  it('makes a branch default and protects one from the menu', async () => {
    api.setDefaultBranch.mockResolvedValue(branch())
    api.setBranchProtection.mockResolvedValue(branch())
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => byName('More actions for feature') !== null)

    await menuItem('More actions for feature', 'Make default')
    await confirmDialog('Make default')
    expect(api.setDefaultBranch).toHaveBeenCalledWith('acme', 'game', 'feature')

    await menuItem('More actions for main', 'Protect')
    await flush()
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', true)
    expect(container).toBeTruthy()
  })

  it('asks before unprotecting', async () => {
    api.listBranches.mockResolvedValue([branch({ is_protected: true })])
    api.setBranchProtection.mockResolvedValue(branch())
    await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => byName('More actions for main') !== null)

    await menuItem('More actions for main', 'Unprotect')
    await cancelDialog()
    expect(api.setBranchProtection).not.toHaveBeenCalled()

    await menuItem('More actions for main', 'Unprotect')
    await confirmDialog('Unprotect')
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', false)
  })

  it('renames through a dialog and keeps submit disabled for unchanged names', async () => {
    api.renameBranch.mockResolvedValue(branch({ name: 'trunk' }))
    await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => byName('More actions for main') !== null)

    await menuItem('More actions for main', 'Rename')
    await waitFor(() => document.body.textContent?.includes('Rename this branch to:') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)

    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'trunk')
    expect(submit()?.disabled).toBe(false)
    await click(submit())
    await flush()
    expect(api.renameBranch).toHaveBeenCalledWith('acme', 'game', 'main', 'trunk')
  })

  it('deletes a non-default branch after confirmation', async () => {
    api.deleteBranch.mockResolvedValue({ message: 'ok' })
    await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => byName('Delete branch feature') !== null)
    expect(byName('Delete branch main')).toBeNull()

    await click(byName('Delete branch feature'))
    await confirmDialog('Delete branch')
    expect(api.deleteBranch).toHaveBeenCalledWith('acme', 'game', 'feature')
  })

  it('reports action failures and can be dismissed', async () => {
    api.setDefaultBranch.mockRejectedValue(new Error('nope'))
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => byName('More actions for feature') !== null)
    await menuItem('More actions for feature', 'Make default')
    await confirmDialog('Make default')
    await waitFor(() => (container.textContent ?? '').includes('nope'))
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(container.textContent).not.toContain('nope')
  })

  it('hides admin actions for writers and offers New branch', async () => {
    allow(PERMISSION_READ | PERMISSION_WRITE)
    const { container, location } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('feature'))
    expect(byName('More actions for feature')).toBeNull()
    expect(byName('Delete branch feature')).toBeNull()
    await click(buttonByText(container, 'New branch'))
    await waitFor(() => location() === '/acme/game/branches/new')
  })

  it('shows the empty message', async () => {
    api.listBranches.mockResolvedValue([])
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('No branches yet.'))
  })
})
