import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import * as endpoints from '../api/endpoints'
import type { BranchResponse, IssueLinksResponse, MergeRequestResponse } from '../api/models'
import { PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'
import { IssuePicker, insertIssueKey, useIssueInsertion, useIssueTrackerEnabled } from '../components/repo/IssuePicker'
import BranchesPage from '../pages/BranchesPage'
import IssuePage from '../pages/IssuePage'
import MergeRequestPage from '../pages/MergeRequestPage'
import NewMergeRequestPage from '../pages/NewMergeRequestPage'
import { buttonByText, click, render, renderHook, renderRoute, type, waitFor } from './render'

vi.mock('../api/endpoints')
vi.mock('../auth', () => ({ useAuth: () => ({ me: { id: '1', name: 'Root', is_admin: true }, refreshMe: vi.fn() }) }))

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
    commit_id: 'cm',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

const LINKS: IssueLinksResponse = {
  key: 'GAME-7',
  url: 'https://studio.atlassian.net/browse/GAME-7',
  links: [
    {
      id: 'l3',
      kind: 'merge_request',
      relation: 'closes',
      created_at: '2026-11-20T10:00:00Z',
      merge_request: { number: 12, title: 'New rig', state: 'merged' },
    },
    {
      id: 'l2',
      kind: 'commit',
      relation: 'mentions',
      created_at: '2026-11-19T10:00:00Z',
      commit: { id: 'abc123def', title: 'Tweak the rig', created_at: '2026-11-19T10:00:00Z' },
    },
    { id: 'l1', kind: 'branch', relation: 'mentions', created_at: '2026-11-18T10:00:00Z', branch: { name: 'GAME-7-rig' } },
  ],
  next_cursor: 'l1',
}

beforeEach(() => {
  api.listBranches.mockResolvedValue([branch(), branch({ id: 'b2', name: 'feature', is_default: false })])
  api.getMyProjectPermissions.mockResolvedValue({
    project_permission: PERMISSION_READ | PERMISSION_WRITE | PERMISSION_ADMIN,
    rules: [],
    defaults: [],
  })
  api.listOrgMembers.mockResolvedValue([])
  api.resolveIssues.mockResolvedValue({ issues: [] })
})

afterEach(() => {
  vi.resetAllMocks()
  document.body.innerHTML = ''
})

function text(): string {
  return document.body.textContent ?? ''
}

async function openPicker() {
  await click(buttonByText(document.body, 'Reference issue'))
  await waitFor(() => document.querySelector('input[placeholder="Search issues"]') !== null)
  return document.querySelector<HTMLInputElement>('input[placeholder="Search issues"]')
}

describe('insertIssueKey', () => {
  it('separates the key from neighbouring words', () => {
    expect(insertIssueKey('', 'GAME-1', 0)).toBe('GAME-1')
    expect(insertIssueKey('Fixes', 'GAME-1', 5)).toBe('Fixes GAME-1')
    expect(insertIssueKey('Fixes ', 'GAME-1', 6)).toBe('Fixes GAME-1')
    expect(insertIssueKey('see (). done', 'GAME-1', 5)).toBe('see (GAME-1). done')
    expect(insertIssueKey('ab', 'GAME-1', 1)).toBe('a GAME-1 b')
    expect(insertIssueKey('replace XX here', 'GAME-1', 8, 10)).toBe('replace GAME-1 here')
  })
})

describe('useIssueTrackerEnabled', () => {
  it('is on only when the project answers an empty resolve', async () => {
    const on = await renderHook(() => useIssueTrackerEnabled('acme', 'game'))
    await waitFor(() => on.result.current)
    expect(api.resolveIssues).toHaveBeenCalledWith('acme', 'game', [])
    await on.unmount()

    api.resolveIssues.mockRejectedValue(new ApiError('the project does not use an issue tracker', 404))
    const off = await renderHook(() => useIssueTrackerEnabled('acme', 'game'))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(off.result.current).toBe(false)
  })
})

describe('useIssueInsertion', () => {
  it('inserts at the remembered caret, or at the end', async () => {
    const onChange = vi.fn()
    const { result } = await renderHook(() => useIssueInsertion('Fixes  soon', onChange))
    result.current.insert('GAME-1')
    expect(onChange).toHaveBeenLastCalledWith('Fixes  soon GAME-1')
    const textarea = document.createElement('textarea')
    textarea.value = 'Fixes  soon'
    textarea.setSelectionRange(6, 6)
    result.current.onBlur({ currentTarget: textarea } as unknown as React.FocusEvent<HTMLTextAreaElement>)
    result.current.insert('GAME-2')
    expect(onChange).toHaveBeenLastCalledWith('Fixes GAME-2 soon')
  })
})

describe('IssuePicker', () => {
  it('searches after a pause and hands the chosen key back', async () => {
    api.searchIssues.mockResolvedValue({
      supported: true,
      issues: [
        { key: 'GAME-7', url: 'u', state: 'ok', title: 'New rig', status: 'In Progress', type: 'Story' },
        { key: 'GAME-8', url: 'u', state: 'ok' },
      ],
    })
    const onPick = vi.fn()
    await render(<IssuePicker org="acme" project="game" onPick={onPick} />)
    const input = await openPicker()
    expect(text()).toContain('Search issues')
    await type(input, 'G')
    expect(api.searchIssues).not.toHaveBeenCalled()
    await type(input, 'rig')
    await waitFor(() => document.querySelectorAll('[role="option"]').length === 2)
    expect(api.searchIssues).toHaveBeenCalledWith('acme', 'game', 'rig', 20)
    const options = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    expect(options[0].textContent).toContain('GAME-7 New rig')
    expect(options[0].textContent).toContain('In Progress · Story')
    await act(async () => {
      options[0].click()
    })
    expect(onPick).toHaveBeenCalledWith('GAME-7')
  })

  it('says so when nothing matches or the tracker is down', async () => {
    api.searchIssues.mockResolvedValueOnce({ supported: true, issues: [] })
    await render(<IssuePicker org="acme" project="game" onPick={vi.fn()} />)
    const input = await openPicker()
    await type(input, 'nothing')
    await waitFor(() => text().includes('No matching issues'))

    api.searchIssues.mockResolvedValueOnce({ supported: true, unavailable: true, issues: [] })
    await type(input, 'down')
    await waitFor(() => text().includes('The issue tracker is not answering'))

    api.searchIssues.mockRejectedValueOnce(new Error('boom'))
    await type(input, 'boom')
    await waitFor(() => api.searchIssues.mock.calls.length === 3)
    await waitFor(() => text().includes('The issue tracker is not answering'))
  })

  it('disappears when the tracker cannot search', async () => {
    api.searchIssues.mockResolvedValue({ supported: false, issues: [] })
    const { container } = await render(<IssuePicker org="acme" project="game" onPick={vi.fn()} />)
    const input = await openPicker()
    await type(input, 'rig')
    await waitFor(() => buttonByText(container, 'Reference issue') === null)
  })
})

describe('merge request forms', () => {
  it('inserts a picked key into the new request description', async () => {
    api.searchIssues.mockResolvedValue({ supported: true, issues: [{ key: 'GAME-7', url: 'u', state: 'ok', title: 'New rig' }] })
    api.createMergeRequest.mockResolvedValue({ number: 3 } as MergeRequestResponse)
    const { container } = await renderRoute('/:org/:project/merges/new', '/acme/game/merges/new', <NewMergeRequestPage />)
    await waitFor(() => buttonByText(container, 'Reference issue') !== null)
    await type(container.querySelector('textarea'), 'Fixes')
    const input = await openPicker()
    await type(input, 'rig')
    await waitFor(() => document.querySelector('[role="option"]') !== null)
    await act(async () => {
      document.querySelector<HTMLElement>('[role="option"]')?.click()
    })
    await waitFor(() => container.querySelector('textarea')?.value === 'Fixes GAME-7')
  })

  it('hides the picker when the project has no tracker', async () => {
    api.resolveIssues.mockRejectedValue(new ApiError('integration off', 503))
    const { container } = await renderRoute('/:org/:project/merges/new', '/acme/game/merges/new', <NewMergeRequestPage />)
    await waitFor(() => container.querySelector('textarea') !== null)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(buttonByText(container, 'Reference issue')).toBeNull()
  })

  it('offers the picker in the edit dialog', async () => {
    const request = {
      id: '1',
      number: 7,
      title: 'Rig',
      description: '',
      source_branch: 'feature',
      target_branch: 'main',
      status: 'open',
      draft: false,
      author_id: '1',
      created_at: '2026-11-20T00:00:00Z',
      updated_at: '2026-11-20T00:00:00Z',
    } as unknown as MergeRequestResponse
    api.getMergeRequestDiff.mockResolvedValue({ files: [] } as unknown as Awaited<ReturnType<typeof api.getMergeRequestDiff>>)
    api.listMergeRequestCommits.mockResolvedValue([])
    api.getMergeRequestReviewState.mockResolvedValue({} as Awaited<ReturnType<typeof api.getMergeRequestReviewState>>)
    api.listMergeRequestReviews.mockResolvedValue([])
    api.listMergeRequestThreads.mockResolvedValue([])
    api.listMergeRequestReviewRequests.mockResolvedValue([])
    api.getMergeRequestTimeline.mockResolvedValue([] as unknown as Awaited<ReturnType<typeof api.getMergeRequestTimeline>>)
    api.listMergeRequestChecks.mockResolvedValue([])
    api.resolveIssues.mockResolvedValue({ issues: [] })
    api.getMergeRequest.mockResolvedValue(request)
    api.searchIssues.mockResolvedValue({ supported: true, issues: [{ key: 'GAME-9', url: 'u', state: 'ok' }] })
    await renderRoute('/:org/:project/merges/:id', '/acme/game/merges/7', <MergeRequestPage />)
    await waitFor(() => buttonByText(document.body, 'Edit') !== null)
    await click(buttonByText(document.body, 'Edit'))
    await waitFor(() => buttonByText(document.body, 'Reference issue') !== null)
    const input = await openPicker()
    await type(input, 'GAME-9')
    await waitFor(() => document.querySelector('[role="option"]') !== null)
    await act(async () => {
      document.querySelector<HTMLElement>('[role="option"]')?.click()
    })
    await waitFor(() => document.querySelector<HTMLTextAreaElement>('[role="dialog"] textarea')?.value === 'GAME-9')
  })
})

describe('IssuePage', () => {
  const pattern = '/:org/:project/issues/:key'

  it('lists the linked work with the resolved issue and pages on', async () => {
    api.listIssueLinks.mockResolvedValueOnce(LINKS).mockResolvedValueOnce({
      ...LINKS,
      links: [{ id: 'l0', kind: 'branch', relation: 'mentions', created_at: '2026-11-17T10:00:00Z', branch: { name: 'old-rig' } }],
      next_cursor: undefined,
    })
    api.resolveIssues.mockResolvedValue({
      issues: [{ key: 'GAME-7', url: LINKS.url, state: 'ok', title: 'Rig the hero', status: 'In Review', type: 'Story', assignee: 'Ana' }],
    })
    const { container } = await renderRoute(pattern, '/acme/game/issues/GAME-7', <IssuePage />)
    await waitFor(() => text().includes('New rig') && text().includes('Rig the hero'))
    expect(api.listIssueLinks).toHaveBeenCalledWith('acme', 'game', 'GAME-7', undefined)
    expect(text()).toContain('In Review')
    expect(text()).toContain('Story · Assignee: Ana')
    expect(text()).toContain('Closes')
    expect(text()).toContain('Merge request #12')
    const hrefs = [...container.querySelectorAll('a')].map((link) => link.getAttribute('href'))
    expect(hrefs).toContain('/acme/game/merges/12')
    expect(hrefs).toContain('/acme/game/commits/abc123def')
    expect(hrefs).toContain('/acme/game/tree/GAME-7-rig')
    const external = [...container.querySelectorAll<HTMLAnchorElement>('a')].find((link) => link.textContent === 'Open in issue tracker')
    expect(external?.getAttribute('href')).toBe(LINKS.url)
    expect(external?.getAttribute('rel')).toBe('noopener noreferrer')

    await click(buttonByText(container, 'Load more'))
    await waitFor(() => text().includes('old-rig'))
    expect(api.listIssueLinks).toHaveBeenLastCalledWith('acme', 'game', 'GAME-7', 'l1')
    expect(buttonByText(container, 'Load more')).toBeNull()
  })

  it('shows an empty issue', async () => {
    api.listIssueLinks.mockResolvedValue({ key: 'GAME-1', url: 'u', links: [] })
    await renderRoute(pattern, '/acme/game/issues/GAME-1', <IssuePage />)
    await waitFor(() => text().includes('Nothing is linked to GAME-1 yet'))
  })

  it('explains a project without a tracker, the integration off and other errors', async () => {
    api.listIssueLinks.mockRejectedValue(new ApiError('not found', 404))
    const first = await renderRoute(pattern, '/acme/game/issues/GAME-1', <IssuePage />)
    await waitFor(() => text().includes('This project does not use an issue tracker'))
    await first.unmount()

    api.listIssueLinks.mockRejectedValue(new ApiError('off', 503))
    const second = await renderRoute(pattern, '/acme/game/issues/GAME-1', <IssuePage />)
    await waitFor(() => text().includes('The issue tracker integration is off'))
    await second.unmount()

    api.listIssueLinks.mockRejectedValue(new ApiError('"ART-1" is not an issue of this project', 400))
    await renderRoute(pattern, '/acme/game/issues/ART-1', <IssuePage />)
    await waitFor(() => text().includes('is not an issue of this project'))
  })
})

describe('BranchesPage issue chips', () => {
  it('shows the issues linked to each branch', async () => {
    api.listBranches.mockResolvedValue([
      branch(),
      branch({ id: 'b2', name: 'GAME-7-rig', is_default: false, issue_keys: [{ key: 'GAME-7', url: 'https://j/browse/GAME-7' }] }),
    ])
    api.resolveIssues.mockResolvedValue({ issues: [{ key: 'GAME-7', url: 'https://j/browse/GAME-7', state: 'ok', status: 'Done', status_category: 'done' }] })
    const { container } = await renderRoute('/:org/:project/branches', '/acme/game/branches', <BranchesPage />)
    await waitFor(() => (container.textContent ?? '').includes('GAME-7Done'))
    expect(api.resolveIssues).toHaveBeenCalledWith('acme', 'game', ['GAME-7'])
    expect(container.querySelector('a.nipa-issue-chip-main')?.getAttribute('href')).toBe('/acme/game/issues/GAME-7')
  })
})
