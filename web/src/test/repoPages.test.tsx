import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import type { BranchResponse, FileLockResponse } from '../api/models'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import BranchesPage from '../pages/BranchesPage'
import LocksPage from '../pages/LocksPage'
import {
  buttonByText,
  buttonsByText,
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

describe('LocksPage', () => {
  const url = '/acme/game/locks'
  const pattern = '/:org/:project/locks'

  it('lists locks with scope, holder and merge request', async () => {
    api.listFileLocks.mockResolvedValue([
      lock(),
      lock({ id: 'l2', path: 'assets/hero.fbx', global: false, branch: 'feature', merge_request_number: 7, held_by_name: '' }),
    ])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    expect(container.textContent).toContain('mainline')
    expect(container.textContent).toContain('Dana')
    expect(container.textContent).toContain('branch feature')
    expect(container.textContent).toContain('MR #7')
    expect(container.textContent).toContain('never')
  })

  it('shows when a lock expires', async () => {
    const expires = new Date(Date.now() + 10 * 60 * 1000).toISOString()
    api.listFileLocks.mockResolvedValue([lock({ expires_at: expires })])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('assets/orc.png'))
    expect(container.querySelector(`time[datetime="${expires}"]`)).not.toBeNull()
    expect(container.textContent).not.toContain('never')
  })

  it('shows the empty message when there are no locks', async () => {
    api.listFileLocks.mockResolvedValue([])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('No locks.'))
  })

  it('releases a mainline lock after confirmation and a branch lock with its scope', async () => {
    api.listFileLocks.mockResolvedValue([
      lock(),
      lock({ id: 'l2', path: 'assets/hero.fbx', global: false, branch: 'feature' }),
    ])
    api.unlockFile.mockResolvedValue({ message: 'ok' })
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => buttonsByText(container, 'Unlock').length === 2)

    await click(buttonsByText(container, 'Unlock')[0])
    await confirmDialog('Unlock')
    expect(api.unlockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', '')

    await click(buttonsByText(container, 'Unlock')[1])
    await confirmDialog('Unlock')
    expect(api.unlockFile).toHaveBeenCalledWith('acme', 'game', 'assets/hero.fbx', 'feature')
  })

  it('keeps the lock when the confirmation is cancelled', async () => {
    api.listFileLocks.mockResolvedValue([lock()])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => buttonsByText(container, 'Unlock').length === 1)
    await click(buttonsByText(container, 'Unlock')[0])
    await cancelDialog()
    expect(api.unlockFile).not.toHaveBeenCalled()
  })

  it('locks a path and clears the field, and reports failures', async () => {
    api.listFileLocks.mockResolvedValue([])
    api.lockFile.mockResolvedValueOnce(lock()).mockRejectedValueOnce(new Error('already locked'))
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => container.querySelector('input') !== null)
    const input = container.querySelector('input') as HTMLInputElement

    await type(input, 'assets/orc.png')
    await click(buttonByText(container, 'Lock'))
    await flush()
    expect(api.lockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', '')
    expect(input.value).toBe('')

    await type(input, 'assets/hero.fbx')
    await click(buttonByText(container, 'Lock'))
    await waitFor(() => (container.textContent ?? '').includes('already locked'))
  })

  it('hides the lock form for read-only users', async () => {
    allow(PERMISSION_READ)
    api.listFileLocks.mockResolvedValue([])
    const { container } = await renderRoute(pattern, url, <LocksPage />)
    await waitFor(() => (container.textContent ?? '').includes('No locks.'))
    expect(container.textContent).not.toContain('Lock a binary file or directory')
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

  it('lists branches with default and protected labels', async () => {
    api.listBranches.mockResolvedValue([
      branch({ is_protected: true }),
      branch({ id: 'b2', name: 'feature', is_default: false, commit_id: undefined }),
    ])
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('feature'))
    expect(container.textContent).toContain('default')
    expect(container.textContent).toContain('protected')
    expect(container.textContent).toContain('abcdef0123')
    expect(container.textContent).toContain('empty')
  })

  it('sets the default branch and protects a branch', async () => {
    api.setDefaultBranch.mockResolvedValue(branch())
    api.setBranchProtection.mockResolvedValue(branch())
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => buttonsByText(container, 'Make default').length === 1)

    await click(buttonByText(container, 'Make default'))
    await flush()
    expect(api.setDefaultBranch).toHaveBeenCalledWith('acme', 'game', 'feature')

    await click(buttonsByText(container, 'Protect')[0])
    await flush()
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', true)
  })

  it('asks before unprotecting', async () => {
    api.listBranches.mockResolvedValue([branch({ is_protected: true })])
    api.setBranchProtection.mockResolvedValue(branch())
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => buttonsByText(container, 'Unprotect').length === 1)

    await click(buttonByText(container, 'Unprotect'))
    await cancelDialog()
    expect(api.setBranchProtection).not.toHaveBeenCalled()

    await click(buttonByText(container, 'Unprotect'))
    await confirmDialog('Unprotect')
    expect(api.setBranchProtection).toHaveBeenCalledWith('acme', 'game', 'main', false)
  })

  it('renames through a dialog and keeps submit disabled for unchanged names', async () => {
    api.renameBranch.mockResolvedValue(branch({ name: 'trunk' }))
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => buttonsByText(container, 'Rename').length === 2)

    await click(buttonsByText(container, 'Rename')[0])
    await waitFor(() => document.body.textContent?.includes('New branch name') ?? false)
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
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => buttonsByText(container, 'Delete').length === 1)

    await click(buttonByText(container, 'Delete'))
    await confirmDialog('Delete branch')
    expect(api.deleteBranch).toHaveBeenCalledWith('acme', 'game', 'feature')
  })

  it('reports action failures and can be dismissed', async () => {
    api.setDefaultBranch.mockRejectedValue(new Error('nope'))
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => buttonsByText(container, 'Make default').length === 1)
    await click(buttonByText(container, 'Make default'))
    await waitFor(() => (container.textContent ?? '').includes('nope'))
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(container.textContent).not.toContain('nope')
  })

  it('hides admin actions for writers and offers Add branch', async () => {
    allow(PERMISSION_READ | PERMISSION_WRITE)
    const { container, location } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('feature'))
    expect(buttonsByText(container, 'Rename')).toHaveLength(0)
    await click(buttonByText(container, 'Add branch'))
    await waitFor(() => location() === '/acme/game/branches/new')
  })

  it('shows the empty message', async () => {
    api.listBranches.mockResolvedValue([])
    const { container } = await renderRoute(pattern, url, <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('No branches.'))
  })
})
