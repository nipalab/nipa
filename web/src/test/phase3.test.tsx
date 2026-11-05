import { useState } from 'react'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as endpoints from '../api/endpoints'
import type { BranchResponse, CommitResponse, FileLockResponse, TagResponse, ThreadResponse, UserResponse } from '../api/models'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import { BranchSelector } from '../components/repo/BranchSelector'
import { FileLockButton } from '../components/repo/FileLockButton'
import { ThreadCard } from '../components/repo/ThreadCard'
import { useTags } from '../components/repo/useTags'
import { sliceCursorPage, usePaged } from '../hooks'
import AdminUsersPage from '../pages/AdminUsersPage'
import BranchesPage from '../pages/BranchesPage'
import CommitsPage from '../pages/CommitsPage'
import ComparePage from '../pages/ComparePage'
import ProjectSettingsPage from '../pages/ProjectSettingsPage'
import TagsPage from '../pages/TagsPage'
import {
  buttonByText,
  buttonsByText,
  cancelDialog,
  click,
  confirmDialog,
  flush,
  render,
  renderHook,
  renderRoute,
  type,
  waitFor,
} from './render'

vi.mock('../api/endpoints')
const auth = vi.hoisted(() => ({ me: null as Record<string, unknown> | null }))
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me, refreshMe: vi.fn() }) }))

const api = vi.mocked(endpoints)

function tag(n: number): TagResponse {
  return { id: `t${n}`, name: `v${n}`, commit_id: `c${n}`, message: n === 1 ? 'first release' : '', user_id: '1', created_at: '2026-01-01T00:00:00Z' }
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
    commit_id: 'cm',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function commit(n: number): CommitResponse {
  return { id: `k${n}`, message: `commit ${n}`, author_name: 'Ann', created_at: '2026-01-01T00:00:00Z' }
}

function allow(mask: number) {
  api.getMyProjectPermissions.mockResolvedValue({ project_permission: mask, rules: [], defaults: [] })
}

beforeEach(() => {
  auth.me = { id: '1', name: 'Root', is_admin: true, is_super_admin: true }
  api.listBranches.mockResolvedValue([branch(), branch({ id: 'b2', name: 'feature', is_default: false, commit_id: 'cf' })])
  allow(PERMISSION_READ | PERMISSION_WRITE | PERMISSION_ADMIN)
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

describe('usePaged', () => {
  it('slices an over-fetched page and exposes the next cursor', () => {
    expect(sliceCursorPage([1, 2], 3, (rows) => rows[0])).toEqual({ items: [1, 2] })
    expect(sliceCursorPage([1, 2, 3, 4], 3, (rows) => rows[3])).toEqual({ items: [1, 2, 3], next: 4 })
  })

  it('loads more pages, resets on reload and reports failures', async () => {
    const fetchPage = vi
      .fn()
      .mockResolvedValueOnce({ items: ['a', 'b'], next: 2 })
      .mockResolvedValueOnce({ items: ['c'] })
      .mockResolvedValue({ items: ['x'] })
    const { result } = await renderHook(() => usePaged<string, number>(fetchPage, []))
    await flush()
    expect(result.current.items).toEqual(['a', 'b'])
    expect(result.current.hasMore).toBe(true)

    await act(async () => result.current.loadMore())
    expect(result.current.items).toEqual(['a', 'b', 'c'])
    expect(result.current.hasMore).toBe(false)
    expect(fetchPage).toHaveBeenLastCalledWith(2)

    await act(async () => result.current.loadMore())
    expect(fetchPage).toHaveBeenCalledTimes(2)

    await act(async () => result.current.reload())
    await flush()
    expect(result.current.items).toEqual(['x'])
  })

  it('reports a failure while loading more', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce({ items: ['a'], next: 1 }).mockRejectedValueOnce(new Error('page failed'))
    const { result } = await renderHook(() => usePaged<string, number>(fetchPage, []))
    await flush()
    await act(async () => result.current.loadMore())
    expect(result.current.error).toBe('page failed')
    expect(result.current.items).toEqual(['a'])
  })
})

describe('TagsPage', () => {
  const pattern = '/:org/:project/tags'
  const url = '/acme/game/tags'

  it('lists tags, creates one through the dialog and deletes after confirmation', async () => {
    api.listTags.mockResolvedValue([tag(1), tag(2)])
    api.createTag.mockResolvedValue(tag(3))
    api.deleteTag.mockResolvedValue(undefined as never)
    const { container } = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => (container.textContent ?? '').includes('v1'))
    expect(container.textContent).toContain('first release')
    expect(container.querySelector('a[href="/acme/game/tree/c1"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/commits/c1"]')).not.toBeNull()

    await click(buttonByText(container, 'New tag'))
    await waitFor(() => document.body.textContent?.includes('Tag name') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    const inputs = document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input')
    await type(inputs[0], 'v3')
    await type(inputs[1], 'feature')
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] textarea'), 'notes')
    await click(submit())
    await flush()
    expect(api.createTag).toHaveBeenCalledWith('acme', 'game', 'v3', 'feature', 'notes')

    await click(buttonsByText(container, 'Delete')[0])
    await cancelDialog()
    expect(api.deleteTag).not.toHaveBeenCalled()
    await click(buttonsByText(container, 'Delete')[0])
    await confirmDialog('Delete tag')
    expect(api.deleteTag).toHaveBeenCalledWith('acme', 'game', 'v1')
  })

  it('pages with a cursor taken from the last visible tag', async () => {
    const page = Array.from({ length: 51 }, (_, i) => tag(i + 1))
    api.listTags.mockResolvedValueOnce(page).mockResolvedValueOnce([tag(51)])
    const { container } = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => buttonByText(container, 'Load more') !== null)
    expect(container.textContent).toContain('50+')
    expect(container.textContent).not.toContain('v51')
    await click(buttonByText(container, 'Load more'))
    await waitFor(() => (container.textContent ?? '').includes('v51'))
    expect(api.listTags).toHaveBeenLastCalledWith('acme', 'game', { limit: 51, after: page[49] })
    expect(buttonByText(container, 'Load more')).toBeNull()
  })

  it('hides write actions for readers, shows empty and error states', async () => {
    auth.me = { id: '5', is_admin: false, is_super_admin: false }
    allow(PERMISSION_READ)
    api.listTags.mockResolvedValue([])
    const empty = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => (empty.container.textContent ?? '').includes('No tags yet.'))
    expect(buttonByText(empty.container, 'New tag')).toBeNull()

    allow(PERMISSION_READ | PERMISSION_WRITE)
    api.listTags.mockResolvedValue([tag(1)])
    const readers = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => (readers.container.textContent ?? '').includes('v1'))
    expect(buttonByText(readers.container, 'New tag')).not.toBeNull()

    api.listTags.mockRejectedValue(new Error('tags unavailable'))
    const failed = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => (failed.container.textContent ?? '').includes('tags unavailable'))
  })

  it('reports delete failures', async () => {
    api.listTags.mockResolvedValue([tag(1)])
    api.deleteTag.mockRejectedValue(new Error('tag is in use'))
    const { container } = await renderRoute(pattern, url, <TagsPage />)
    await waitFor(() => buttonByText(container, 'Delete') !== null)
    await click(buttonByText(container, 'Delete'))
    await confirmDialog('Delete tag')
    await waitFor(() => (container.textContent ?? '').includes('tag is in use'))
    await click(container.querySelector('button[class*="BannerDismiss"]'))
    expect(container.textContent).not.toContain('tag is in use')
  })
})

describe('BranchSelector tags', () => {
  const branches = [branch(), branch({ id: 'b2', name: 'feature', is_default: false })]

  it('stays branch-only without tag props', async () => {
    const { container } = await render(<BranchSelector branches={branches} rev="main" onSelect={() => undefined} />)
    await click(container.querySelector('button'))
    expect(document.body.textContent).not.toContain('Tags')
  })

  it('lazy-loads tags on open, switches tabs and selects a tag', async () => {
    const onOpen = vi.fn()
    const onSelectTag = vi.fn()
    const { container } = await render(
      <BranchSelector
        branches={branches}
        rev="main"
        onSelect={() => undefined}
        tags={null}
        onOpen={onOpen}
        onSelectTag={onSelectTag}
      />,
    )
    await click(container.querySelector('button'))
    expect(onOpen).toHaveBeenCalled()
    await click(buttonByText(document.body, 'Tags'))
    expect(document.body.textContent).toContain('Loading tags…')

    document.body.innerHTML = ''
    const loaded = await render(
      <BranchSelector
        branches={branches}
        rev="main"
        onSelect={() => undefined}
        tags={[tag(1), tag(2)]}
        onSelectTag={onSelectTag}
      />,
    )
    await click(loaded.container.querySelector('button'))
    await click(buttonByText(document.body, 'Tags'))
    expect(document.body.textContent).toContain('v2')
    await type(document.body.querySelector<HTMLInputElement>('input[aria-label="Find a tag"]'), 'zzz')
    expect(document.body.textContent).toContain('No tags found')
    await type(document.body.querySelector<HTMLInputElement>('input[aria-label="Find a tag"]'), 'v1')
    await click(Array.from(document.body.querySelectorAll('[role="menuitem"], li')).find((item) => item.textContent?.includes('v1')))
    expect(onSelectTag).toHaveBeenCalledWith(tag(1))
  })

  it('shows the tag name and a placeholder for the current revision', async () => {
    const tagged = await render(
      <BranchSelector branches={branches} rev="c2" onSelect={() => undefined} tags={[tag(2)]} onSelectTag={() => undefined} />,
    )
    expect(tagged.container.textContent).toContain('v2')
    document.body.innerHTML = ''
    const empty = await render(<BranchSelector branches={branches} rev="" placeholder="choose to compare" onSelect={() => undefined} />)
    expect(empty.container.textContent).toContain('choose to compare')
  })
})

describe('useTags', () => {
  it('loads once, falls back to an empty list on failure and resets per project', async () => {
    api.listTags.mockResolvedValueOnce([tag(1)])
    const { result } = await renderHook(() => useTags('acme', 'game'))
    expect(result.current.tags).toBeNull()
    await act(async () => result.current.load())
    await flush()
    expect(result.current.tags).toEqual([tag(1)])
    await act(async () => result.current.load())
    expect(api.listTags).toHaveBeenCalledTimes(1)

    api.listTags.mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce([tag(2)])
    const failing = await renderHook(() => useTags('acme', 'other'))
    await act(async () => failing.result.current.load())
    await flush()
    expect(failing.result.current.tags).toEqual([])
    await act(async () => failing.result.current.load())
    await flush()
    expect(failing.result.current.tags).toEqual([tag(2)])
  })

  it('ignores a late response from a previous project', async () => {
    let resolveA: (value: TagResponse[]) => void = () => undefined
    api.listTags.mockReturnValueOnce(new Promise<TagResponse[]>((resolve) => (resolveA = resolve)))
    const scope = { project: 'a' }
    const bump = { fn: () => undefined as void }
    const { result } = await renderHook(() => {
      const [, setN] = useState(0)
      bump.fn = () => setN((n) => n + 1)
      return useTags('acme', scope.project)
    })
    await act(async () => result.current.load())
    scope.project = 'b'
    await act(async () => bump.fn())
    await act(async () => resolveA([tag(1)]))
    expect(result.current.tags).toBeNull()
  })
})

describe('ComparePage', () => {
  const pattern = '/:org/:project/compare'
  const url = '/acme/game/compare'
  const file = {
    path: 'src/main.ts',
    status: 'modified',
    binary: false,
    additions: 1,
    deletions: 1,
    hunks: [
      {
        old_start: 1,
        old_lines: 1,
        new_start: 1,
        new_lines: 1,
        lines: [
          { kind: 'remove', old_line: 1, text: 'old line' },
          { kind: 'add', new_line: 1, text: 'new line' },
        ],
      },
    ],
  }

  it('asks for a compare side first', async () => {
    const { container } = await renderRoute(pattern, url, <ComparePage />)
    await waitFor(() => (container.textContent ?? '').includes('Choose a branch, tag or commit'))
    expect(api.getCommitDiff).not.toHaveBeenCalled()
  })

  it('resolves branch names to commits and renders the diff', async () => {
    api.getCommitDiff.mockResolvedValue({ files: [file] } as never)
    const { container } = await renderRoute(pattern, `${url}?base=main&head=feature`, <ComparePage />)
    await waitFor(() => (container.textContent ?? '').includes('src/main.ts'))
    expect(api.getCommitDiff).toHaveBeenCalledWith('acme', 'game', 'cf', 'cm')
  })

  it('accepts raw commit ids, reports no differences and identical sides', async () => {
    api.getCommitDiff.mockResolvedValue({ files: [] } as never)
    const empty = await renderRoute(pattern, `${url}?base=c1&head=c2`, <ComparePage />)
    await waitFor(() => (empty.container.textContent ?? '').includes('no differences'))
    expect(api.getCommitDiff).toHaveBeenCalledWith('acme', 'game', 'c2', 'c1')

    api.getCommitDiff.mockClear()
    const same = await renderRoute(pattern, `${url}?base=main&head=cm`, <ComparePage />)
    await waitFor(() => (same.container.textContent ?? '').includes('nothing to compare'))
    expect(api.getCommitDiff).not.toHaveBeenCalled()
  })

  it('updates the comparison when a side is chosen from the selector', async () => {
    api.getCommitDiff.mockResolvedValue({ files: [] } as never)
    api.listTags.mockResolvedValue([tag(2)])
    const { container } = await renderRoute(pattern, `${url}?base=main`, <ComparePage />)
    await waitFor(() => (container.textContent ?? '').includes('choose to compare'))
    const trigger = (label: string) =>
      Array.from(container.querySelectorAll<HTMLButtonElement>('button[aria-haspopup]')).find((button) => button.textContent === label)

    await click(trigger('choose to compare'))
    await click(Array.from(document.body.querySelectorAll('li')).find((item) => item.textContent?.includes('feature')))
    await waitFor(() => api.getCommitDiff.mock.calls.length > 0)
    expect(api.getCommitDiff).toHaveBeenLastCalledWith('acme', 'game', 'cf', 'cm')

    await click(trigger('main'))
    await click(buttonByText(document.body, 'Tags'))
    await waitFor(() => document.body.textContent?.includes('v2') ?? false)
    await click(Array.from(document.body.querySelectorAll('li')).find((item) => item.textContent?.includes('v2')))
    await waitFor(() => api.getCommitDiff.mock.calls.length > 1)
    expect(api.getCommitDiff).toHaveBeenLastCalledWith('acme', 'game', 'cf', 'c2')
  })

  it('resolves tag names from shared links', async () => {
    api.getTag.mockImplementation(async (_o, _p, name) => (name === 'v1.0.0' ? ({ ...tag(1), commit_id: 'ct' } as never) : Promise.reject(new Error('nf'))))
    api.getCommitDiff.mockResolvedValue({ files: [] } as never)
    await renderRoute(pattern, `${url}?base=v1.0.0&head=feature`, <ComparePage />)
    await waitFor(() => api.getCommitDiff.mock.calls.length > 0)
    expect(api.getCommitDiff).toHaveBeenLastCalledWith('acme', 'game', 'cf', 'ct')
  })

  it('shows diff errors', async () => {
    api.getCommitDiff.mockRejectedValue(new Error('diff too large'))
    const { container } = await renderRoute(pattern, `${url}?base=main&head=feature`, <ComparePage />)
    await waitFor(() => (container.textContent ?? '').includes('diff too large'))
  })
})

describe('CommitsPage and BranchesPage paging', () => {
  it('loads older commits starting from the over-fetched commit', async () => {
    const page = Array.from({ length: 51 }, (_, i) => commit(i + 1))
    api.listCommits.mockResolvedValueOnce(page).mockResolvedValueOnce([commit(51)])
    const { container } = await renderRoute('/:org/:project/commits', '/acme/game/commits', <CommitsPage />)
    await waitFor(() => (container.textContent ?? '').includes('commit 50'))
    expect(container.textContent).not.toContain('commit 51')
    await click(buttonByText(container, 'Load more'))
    await waitFor(() => (container.textContent ?? '').includes('commit 51'))
    expect(api.listCommits).toHaveBeenLastCalledWith('acme', 'game', '', '', { start: 'k51', limit: 51 })
  })

  it('loads more branches using the last visible branch as the cursor', async () => {
    const many = Array.from({ length: 51 }, (_, i) => branch({ id: `b${i}`, name: `br-${i}`, is_default: i === 0 }))
    api.listBranches.mockImplementation(async (_org, _project, options) =>
      options?.after ? [branch({ id: 'bz', name: 'br-last', is_default: false })] : many,
    )
    const { container } = await renderRoute('/:org/:project/branches', '/acme/game/branches', <BranchesPage />)
    await waitFor(() => buttonByText(container, 'Load more') !== null)
    await click(buttonByText(container, 'Load more'))
    await waitFor(() => (container.textContent ?? '').includes('br-last'))
    expect(api.listBranches).toHaveBeenCalledWith('acme', 'game', { limit: 51, after: many[49] })
  })

  it('links the compare view from the branches page', async () => {
    const { container, location } = await renderRoute('/:org/:project/branches', '/acme/game/branches', <BranchesPage />)
    await waitFor(() => buttonByText(container, 'Compare') !== null)
    await click(buttonByText(container, 'Compare'))
    await waitFor(() => location() === '/acme/game/compare')
  })
})

describe('Project General settings', () => {
  const pattern = '/:org/:project/settings'
  const url = '/acme/game/settings'
  const project = { id: 'p1', org_id: 'o1', slug: 'game', name: 'Game', description: 'A game' }

  beforeEach(() => {
    api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'acme', name: 'Acme', role: 'owner' }])
    api.listProjects.mockResolvedValue([project])
    api.listProjectRules.mockResolvedValue([])
    api.listProjectDefaults.mockResolvedValue([])
    api.listOrgMembers.mockResolvedValue([])
  })

  it('opens on General and saves a rename', async () => {
    api.updateProject.mockResolvedValue({ ...project, name: 'Game 2' })
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (container.textContent ?? '').includes('The URL name cannot be changed.'))
    const nameInput = container.querySelector<HTMLInputElement>('input')
    expect(nameInput?.value).toBe('Game')
    const save = () => buttonByText(container, 'Save changes')
    expect(save()?.disabled).toBe(true)

    await type(nameInput, 'Game 2')
    await type(container.querySelector<HTMLInputElement>('textarea'), 'Better')
    await click(save())
    await waitFor(() => (container.textContent ?? '').includes('Repository updated.'))
    expect(api.updateProject).toHaveBeenCalledWith('acme', 'game', 'Game 2', 'Better')
  })

  it('shows save failures and rejects an empty name', async () => {
    api.updateProject.mockRejectedValue(new Error('name taken'))
    const { container } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => container.querySelector('input') !== null)
    const nameInput = container.querySelector<HTMLInputElement>('input')
    await type(nameInput, '')
    expect(buttonByText(container, 'Save changes')?.disabled).toBe(true)
    await type(nameInput, 'Taken')
    await click(buttonByText(container, 'Save changes'))
    await waitFor(() => (container.textContent ?? '').includes('name taken'))
  })

  it('deletes the repository from the danger zone and returns to the organization', async () => {
    api.deleteProject.mockResolvedValue(undefined as never)
    const { container, location } = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => buttonByText(container, 'Delete repository') !== null)
    await click(buttonByText(container, 'Delete repository'))
    await waitFor(() => document.body.textContent?.includes('to confirm') ?? false)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'game')
    await click(buttonByText(document.body, 'Delete project'))
    await waitFor(() => location() === '/acme')
    expect(api.deleteProject).toHaveBeenCalledWith('acme', 'game')
  })

  it('hides the danger zone from non-owners and handles a missing project', async () => {
    api.listOrgs.mockResolvedValue([{ id: 'o1', slug: 'acme', name: 'Acme', role: 'member' }])
    auth.me = { id: '5', is_admin: false, is_super_admin: false }
    const member = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (member.container.textContent ?? '').includes('The URL name cannot be changed.'))
    expect(member.container.textContent).not.toContain('Danger zone')

    api.listProjects.mockResolvedValue([])
    const missing = await renderRoute(pattern, url, <ProjectSettingsPage />)
    await waitFor(() => (missing.container.textContent ?? '').includes('Repository not found.'))
  })
})

describe('AdminUsersPage edit email', () => {
  const user = (overrides: Partial<UserResponse> = {}): UserResponse => ({
    id: '2',
    name: 'Dana',
    email: 'dana@example.com',
    photo_url: '',
    is_admin: false,
    is_super_admin: false,
    notify_email: true,
    deleted: false,
    ...overrides,
  })

  it('changes a users email through a dialog', async () => {
    api.listUsers.mockResolvedValue([user()])
    api.updateUserEmail.mockResolvedValue(user({ email: 'new@example.com' }))
    const { container } = await renderRoute('/admin/users', '/admin/users', <AdminUsersPage />)
    await waitFor(() => buttonByText(container, 'Edit email') !== null)
    await click(buttonByText(container, 'Edit email'))
    await waitFor(() => document.body.textContent?.includes('Change email for Dana') ?? false)
    const submit = () => document.body.querySelector<HTMLButtonElement>('button[type="submit"]')
    expect(submit()?.disabled).toBe(true)
    await type(document.body.querySelector<HTMLInputElement>('[role="dialog"] input'), 'new@example.com')
    await click(submit())
    await flush()
    expect(api.updateUserEmail).toHaveBeenCalledWith('2', 'new@example.com')
  })
})

describe('ThreadCard delete thread', () => {
  const thread: ThreadResponse = {
    id: 't1',
    merge_request_id: 7,
    side: 'right',
    outdated: false,
    resolved: false,
    created_by: { user_id: 'u1', name: 'Author' },
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    comments: [
      {
        id: 'c1',
        thread_id: 't1',
        user: { user_id: 'u1', name: 'Author' },
        body: 'hello',
        system: false,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      },
    ],
  }

  it('lets the thread author delete it after confirmation', async () => {
    const deleted: string[] = []
    const { container } = await render(
      <ThreadCard thread={thread} me="u1" canWrite onDeleteThread={(id) => deleted.push(id)} />,
    )
    await click(buttonByText(container, 'Delete thread'))
    await cancelDialog()
    expect(deleted).toEqual([])
    await click(buttonByText(container, 'Delete thread'))
    await confirmDialog('Delete thread')
    expect(deleted).toEqual(['t1'])
  })

  it('offers deletion to admins but not to other users', async () => {
    const asAdmin = await render(<ThreadCard thread={thread} me="u2" canWrite canAdmin onDeleteThread={() => undefined} />)
    expect(buttonByText(asAdmin.container, 'Delete thread')).not.toBeNull()
    const other = await render(<ThreadCard thread={thread} me="u2" canWrite onDeleteThread={() => undefined} />)
    expect(buttonByText(other.container, 'Delete thread')).toBeNull()
  })
})

describe('FileLockButton', () => {
  const lock = (overrides: Partial<FileLockResponse> = {}): FileLockResponse => ({
    id: 'l1',
    path: 'assets/orc.png',
    global: true,
    held_by: '1',
    held_by_name: 'Me',
    acquired_at: '2026-01-01T00:00:00Z',
    ...overrides,
  })
  const props = { org: 'acme', project: 'game', path: 'assets/orc.png', me: '1', canWrite: true, onError: vi.fn() }

  it('locks a free file', async () => {
    api.listFileLocks.mockResolvedValue([])
    api.lockFile.mockResolvedValue(lock())
    const { container } = await render(<FileLockButton {...props} />)
    await waitFor(() => buttonByText(container, 'Lock') !== null)
    await click(buttonByText(container, 'Lock'))
    await flush()
    expect(api.lockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', '')
  })

  it('locks on the given branch', async () => {
    api.listFileLocks.mockResolvedValue([])
    api.lockFile.mockResolvedValue(lock())
    const { container } = await render(<FileLockButton {...props} branch="feature" />)
    await waitFor(() => buttonByText(container, 'Lock') !== null)
    await click(buttonByText(container, 'Lock'))
    await flush()
    expect(api.lockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', 'feature')
  })

  it('shows nothing to read-only users on a free file', async () => {
    api.listFileLocks.mockResolvedValue([])
    const { container } = await render(<FileLockButton {...props} canWrite={false} />)
    await flush()
    expect(container.textContent).toBe('')
  })

  it('lets the holder unlock after confirmation, with branch scope', async () => {
    api.listFileLocks.mockResolvedValue([lock({ global: false, branch: 'feature' })])
    api.unlockFile.mockResolvedValue(undefined as never)
    const { container } = await render(<FileLockButton {...props} />)
    await waitFor(() => buttonByText(container, 'Unlock') !== null)
    await click(buttonByText(container, 'Unlock'))
    await confirmDialog('Unlock')
    expect(api.unlockFile).toHaveBeenCalledWith('acme', 'game', 'assets/orc.png', 'feature')
  })

  it('names another holder and notes a covering directory lock', async () => {
    api.listFileLocks.mockResolvedValue([lock({ path: 'assets', held_by: '2', held_by_name: 'Dana' })])
    const { container } = await render(<FileLockButton {...props} />)
    await waitFor(() => (container.textContent ?? '').includes('Locked by Dana'))
    expect(container.textContent).toContain('(assets)')
    expect(buttonByText(container, 'Unlock')).toBeNull()
  })

  it('reports lock failures to the parent', async () => {
    const onError = vi.fn()
    api.listFileLocks.mockResolvedValue([])
    api.lockFile.mockRejectedValue(new Error('already locked'))
    const { container } = await render(<FileLockButton {...props} onError={onError} />)
    await waitFor(() => buttonByText(container, 'Lock') !== null)
    await click(buttonByText(container, 'Lock'))
    await waitFor(() => onError.mock.calls.some((call) => call[0] === 'already locked'))
  })
})
