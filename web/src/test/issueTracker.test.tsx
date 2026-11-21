import { act, useState, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import * as endpoints from '../api/endpoints'
import type { IssueTrackerJobResponse, IssueTrackerResponse, ProjectIssueTrackerResponse } from '../api/models'
import { OrgIssueTrackerSettings, ProjectIssueTrackerSettings } from '../components/IssueTrackerSettings'
import { IssueChip, IssueChips, collectIssueKeys, useIssues } from '../components/repo/IssueChips'
import { buttonByText, choose, click, confirmDialog, flush, render, renderHook, type, waitFor } from './render'

vi.mock('../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/endpoints')>()
  return {
    ...actual,
    resolveIssues: vi.fn(),
    getIssueTracker: vi.fn(),
    putIssueTracker: vi.fn(),
    deleteIssueTracker: vi.fn(),
    testIssueTracker: vi.fn(),
    getProjectIssueTracker: vi.fn(),
    putProjectIssueTracker: vi.fn(),
    deleteProjectIssueTracker: vi.fn(),
    listIssueTrackerJobs: vi.fn(),
    retryIssueTrackerJob: vi.fn(),
  }
})

const api = vi.mocked(endpoints)

const TRACKER: IssueTrackerResponse = {
  id: 't1',
  kind: 'jira',
  name: 'Studio Jira',
  base_url: 'https://studio.atlassian.net',
  auth_kind: 'basic',
  account: 'bot@studio.com',
  can_manage: true,
}

const SETTINGS: ProjectIssueTrackerResponse = {
  tracker_id: 't1',
  keys: ['ART', 'GAME'],
  link_merge_requests: true,
  link_commits: 'off',
  comment_on_merge: true,
  merged_status: 'Done',
  opened_status: '',
  active: true,
  set_by: 'u1',
  updated_at: '2026-11-20T00:00:00Z',
}

beforeEach(() => {
  api.listIssueTrackerJobs.mockResolvedValue({ jobs: [] })
})

afterEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

function input(container: HTMLElement, placeholder: string): HTMLInputElement {
  const el = container.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)
  if (!el) throw new Error(`input ${placeholder} not found`)
  return el
}

// Switcher renders the first child until the "Switch" button is clicked, the
// way a route param change re-renders the same settings component.
function Switcher({ from, to }: { from: ReactNode; to: ReactNode }) {
  const [switched, setSwitched] = useState(false)
  return (
    <>
      <button onClick={() => setSwitched(true)}>Switch</button>
      {switched ? to : from}
    </>
  )
}

function pending<T>() {
  let resolve!: (value: T) => void
  let reject!: (err: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('issue chips', () => {
  it('collects sorted unique keys', () => {
    expect(
      collectIssueKeys([
        { issue_keys: [{ key: 'GAME-2', url: 'u2' }, { key: 'GAME-1', url: 'u1' }] },
        { issue_keys: [{ key: 'GAME-2', url: 'u2' }] },
        {},
      ]),
    ).toEqual(['GAME-1', 'GAME-2'])
  })

  it('links each key to its issue page and to the tracker', async () => {
    const { container } = await render(
      <IssueChips
        org="acme"
        project="game"
        refs={[
          { key: 'GAME-2', url: 'https://j/browse/GAME-2' },
          { key: 'GAME-1', url: 'https://j/browse/GAME-1' },
          { key: 'GAME-3', url: 'https://j/browse/GAME-3' },
        ]}
        issues={
          new Map([
            ['GAME-1', { key: 'GAME-1', url: '', state: 'ok', title: 'Rig', status: 'Done', status_category: 'done', assignee: 'Ana' }],
            ['GAME-3', { key: 'GAME-3', url: '', state: 'not_found' }],
          ])
        }
      />,
    )
    const pages = Array.from(container.querySelectorAll<HTMLAnchorElement>('a.nipa-issue-chip-main'))
    expect(pages.map((link) => link.textContent)).toEqual(['GAME-1Done', 'GAME-2', 'GAME-3Unknown'])
    expect(pages[0].getAttribute('href')).toBe('/acme/game/issues/GAME-1')
    expect(pages[0].getAttribute('title')).toBe('Rig · Done · Assignee: Ana')
    expect(pages[1].getAttribute('title')).toBe('Work linked to GAME-2')
    const external = Array.from(container.querySelectorAll<HTMLAnchorElement>('a.nipa-issue-chip-external'))
    expect(external[0].getAttribute('href')).toBe('https://j/browse/GAME-1')
    expect(external[0].getAttribute('target')).toBe('_blank')
    expect(external[0].getAttribute('rel')).toBe('noopener noreferrer')
    expect(external[1].getAttribute('aria-label')).toBe('Open GAME-2 in the issue tracker')
  })

  it('renders nothing without keys and an in-progress label', async () => {
    const empty = await render(<IssueChips org="acme" project="game" refs={undefined} issues={new Map()} />)
    expect(empty.container.innerHTML).toBe('')
    const chip = await render(
      <IssueChip
        org="acme"
        project="game"
        issueRef={{ key: 'GAME-4', url: 'u' }}
        issue={{ key: 'GAME-4', url: 'u', state: 'ok', status: 'In Progress', status_category: 'in_progress' }}
      />,
    )
    expect(chip.container.textContent).toBe('GAME-4In Progress')
  })

  it('resolves only keys it has not asked for, in batches of 50', async () => {
    api.resolveIssues.mockImplementation(async (_org, _project, keys) => ({
      issues: keys.map((key) => ({ key, url: `u/${key}`, state: 'ok' as const, status: 'Done' })),
    }))
    let keys = ['GAME-1']
    const { result, unmount } = await renderHook(() => useIssues('acme', 'game', keys))
    await waitFor(() => result.current.size === 1)
    expect(api.resolveIssues).toHaveBeenCalledWith('acme', 'game', ['GAME-1'])
    await unmount()

    keys = Array.from({ length: 60 }, (_, i) => `GAME-${i + 1}`)
    api.resolveIssues.mockClear()
    const many = await renderHook(() => useIssues('acme', 'game', keys))
    await waitFor(() => many.result.current.size === 60)
    expect(api.resolveIssues.mock.calls.map((call) => call[2].length)).toEqual([50, 10])
  })

  it('ignores resolve failures', async () => {
    api.resolveIssues.mockRejectedValue(new Error('down'))
    const { result } = await renderHook(() => useIssues('acme', 'game', ['GAME-1']))
    await flush()
    expect(result.current.size).toBe(0)
    const none = await renderHook(() => useIssues('acme', 'game', []))
    expect(none.result.current.size).toBe(0)
  })
})

describe('org issue tracker settings', () => {
  it('explains when the integration is off', async () => {
    api.getIssueTracker.mockRejectedValue(new ApiError('not configured', 503))
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => container.textContent?.includes('Issue tracker integration is off') ?? false)
  })

  it('shows members the connection read-only', async () => {
    api.getIssueTracker.mockResolvedValue({ kind: 'jira', name: 'Studio Jira', base_url: 'https://j', can_manage: false })
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => container.textContent?.includes('Only organization owners can change the connection') ?? false)
    expect(container.textContent).toContain('https://j')
    expect(container.querySelector('input[type="password"]')).toBeNull()
  })

  it('connects a new Jira site with a token', async () => {
    api.getIssueTracker.mockRejectedValueOnce(new ApiError('no issue tracker connection', 404))
    api.putIssueTracker.mockResolvedValue(TRACKER)
    api.getIssueTracker.mockResolvedValue(TRACKER)
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => buttonByText(container, 'Connect') !== null)
    expect(buttonByText(container, 'Connect')?.disabled).toBe(true)
    await type(input(container, 'https://studio.atlassian.net'), 'https://studio.atlassian.net')
    await type(container.querySelector<HTMLInputElement>('input[type="email"]'), 'bot@studio.com')
    await type(container.querySelector<HTMLInputElement>('input[type="password"]'), 'secret')
    await click(buttonByText(container, 'Connect'))
    await waitFor(() => container.textContent?.includes('Connection saved and verified.') ?? false)
    expect(api.putIssueTracker).toHaveBeenCalledWith('acme', {
      kind: 'jira',
      name: '',
      base_url: 'https://studio.atlassian.net',
      auth_kind: 'basic',
      account: 'bot@studio.com',
      token: 'secret',
    })
  })

  it('keeps the stored token, switches to bearer, tests and reports a failed check', async () => {
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.putIssueTracker.mockRejectedValueOnce(new ApiError('issue tracker check auth failed: refused', 400))
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => buttonByText(container, 'Save') !== null)
    expect(input(container, 'Leave blank to keep the stored token').value).toBe('')
    const bearer = container.querySelector<HTMLInputElement>('input[value="bearer"]')
    await click(bearer)
    await click(buttonByText(container, 'Save'))
    await waitFor(() => container.textContent?.includes('issue tracker check auth failed') ?? false)
    expect(api.putIssueTracker.mock.calls[0][1]).toEqual({
      kind: 'jira',
      name: 'Studio Jira',
      base_url: 'https://studio.atlassian.net',
      auth_kind: 'bearer',
      account: '',
    })

    api.testIssueTracker.mockResolvedValueOnce({ ok: false, check: 'reach', error: 'dial failed' })
    await click(buttonByText(container, 'Test connection'))
    await waitFor(() => container.textContent?.includes('Check failed: reach') ?? false)
    expect(container.textContent).toContain('The server could not reach this address.')

    api.testIssueTracker.mockResolvedValueOnce({
      ok: true,
      site: { kind: 'jira', title: 'Studio', version: '', deployment: 'Cloud', account_id: 'a', account_name: 'Bot' },
    })
    await click(buttonByText(container, 'Test connection'))
    await waitFor(() => container.textContent?.includes('Connection works') ?? false)
    expect(container.textContent).toContain('Studio · Cloud · signed in as Bot')
  })

  it('shows the conflict when a project still uses the connection', async () => {
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.deleteIssueTracker.mockRejectedValue(new ApiError('the issue tracker connection is used by 1 project(s)', 409))
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => buttonByText(container, 'Delete') !== null)
    await click(buttonByText(container, 'Delete'))
    await confirmDialog('Delete connection')
    await waitFor(() => container.textContent?.includes('used by 1 project(s)') ?? false)
  })

  it('never shows the previous org connection while another org loads', async () => {
    const beta = pending<IssueTrackerResponse>()
    api.getIssueTracker.mockImplementation((org: string) => (org === 'acme' ? Promise.resolve(TRACKER) : beta.promise))
    const { container } = await render(
      <Switcher from={<OrgIssueTrackerSettings org="acme" />} to={<OrgIssueTrackerSettings org="beta" />} />,
    )
    await waitFor(() => buttonByText(container, 'Save') !== null)
    expect(input(container, 'https://studio.atlassian.net').value).toBe(TRACKER.base_url)
    await click(buttonByText(container, 'Switch'))
    expect(buttonByText(container, 'Save')).toBeNull()
    expect(container.querySelector('input')).toBeNull()
    await act(async () => beta.reject(new ApiError('no issue tracker connection', 404)))
    await waitFor(() => buttonByText(container, 'Connect') !== null)
    expect(input(container, 'https://studio.atlassian.net').value).toBe('')
    await click(buttonByText(container, 'Connect'))
    expect(api.putIssueTracker).not.toHaveBeenCalled()
  })

  it('shows other load errors', async () => {
    api.getIssueTracker.mockRejectedValue(new ApiError('boom', 500))
    const { container } = await render(<OrgIssueTrackerSettings org="acme" />)
    await waitFor(() => container.textContent?.includes('boom') ?? false)
  })
})

describe('project issue tracker settings', () => {
  it('asks for an org connection first', async () => {
    api.getProjectIssueTracker.mockRejectedValue(new ApiError('not used', 404))
    api.getIssueTracker.mockRejectedValue(new ApiError('none', 404))
    const { container } = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('has no issue tracker connection') ?? false)
  })

  it('explains when the integration is off', async () => {
    api.getProjectIssueTracker.mockRejectedValue(new ApiError('off', 503))
    api.getIssueTracker.mockRejectedValue(new ApiError('off', 503))
    const { container } = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => container.textContent?.includes('Issue tracker integration is off') ?? false)
  })

  it('saves keys and rules with the visibility notes', async () => {
    api.getProjectIssueTracker.mockRejectedValueOnce(new ApiError('not used', 404))
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.putProjectIssueTracker.mockResolvedValue(SETTINGS)
    api.getProjectIssueTracker.mockResolvedValue(SETTINGS)
    const { container } = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => buttonByText(container, 'Save') !== null)
    expect(container.textContent).toContain('Studio Jira (https://studio.atlassian.net)')
    expect(container.textContent).toContain('Who sees what')
    expect(container.textContent).toContain('even for a private')
    expect(buttonByText(container, 'Save')?.disabled).toBe(true)
    await type(input(container, 'GAME, ART'), 'game, art')
    await choose(container.querySelector('select'), 'all')
    await type(input(container, 'Done'), 'Done')
    await type(input(container, 'In Review'), 'In Review')
    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    await click(checkboxes[1])
    await click(buttonByText(container, 'Save'))
    await waitFor(() => container.textContent?.includes('Issue tracker settings saved.') ?? false)
    expect(api.putProjectIssueTracker).toHaveBeenCalledWith('acme', 'game', {
      keys: ['GAME', 'ART'],
      link_merge_requests: true,
      link_commits: 'all',
      comment_on_merge: false,
      merged_status: 'Done',
      opened_status: 'In Review',
      active: true,
    })
  })

  it('removes the settings and shows save errors', async () => {
    api.getProjectIssueTracker.mockResolvedValue(SETTINGS)
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.putProjectIssueTracker.mockRejectedValue(new ApiError('invalid project key "1A"', 400))
    api.deleteProjectIssueTracker.mockResolvedValue(undefined)
    const { container } = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => buttonByText(container, 'Remove') !== null)
    expect(input(container, 'GAME, ART').value).toBe('ART, GAME')
    await click(buttonByText(container, 'Save'))
    await waitFor(() => container.textContent?.includes('invalid project key') ?? false)
    api.getProjectIssueTracker.mockRejectedValue(new ApiError('not used', 404))
    await click(buttonByText(container, 'Remove'))
    await confirmDialog('Remove')
    await waitFor(() => container.textContent?.includes('no longer uses the issue tracker') ?? false)
    expect(api.deleteProjectIssueTracker).toHaveBeenCalledWith('acme', 'game')
    // The reload resolves to "not set up": the removed keys must not stay in
    // the form where Save would silently recreate them.
    await waitFor(() => buttonByText(container, 'Remove') === null)
    expect(input(container, 'GAME, ART').value).toBe('')
    expect(input(container, 'Done').value).toBe('')
    expect(buttonByText(container, 'Save')?.disabled).toBe(true)
  })

  it('never shows the previous project settings while another project loads', async () => {
    const tools = pending<ProjectIssueTrackerResponse>()
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.getProjectIssueTracker.mockImplementation((_org: string, project: string) =>
      project === 'game' ? Promise.resolve(SETTINGS) : tools.promise,
    )
    const { container } = await render(
      <Switcher
        from={<ProjectIssueTrackerSettings org="acme" project="game" />}
        to={<ProjectIssueTrackerSettings org="acme" project="tools" />}
      />,
    )
    await waitFor(() => buttonByText(container, 'Remove') !== null)
    await click(buttonByText(container, 'Switch'))
    expect(buttonByText(container, 'Save')).toBeNull()
    expect(buttonByText(container, 'Remove')).toBeNull()
    await act(async () => tools.reject(new ApiError('not used', 404)))
    await waitFor(() => buttonByText(container, 'Save') !== null)
    expect(input(container, 'GAME, ART').value).toBe('')
    expect(buttonByText(container, 'Save')?.disabled).toBe(true)
  })
})

const JOBS: IssueTrackerJobResponse[] = [
  { id: 'j3', issue_key: 'GAME-3', action: 'comment', state: 'pending', attempts: 0, created_at: '2026-11-20T00:00:00Z' },
  {
    id: 'j2',
    issue_key: 'GAME-2',
    action: 'transition',
    state: 'failed',
    attempts: 1,
    status: 'Done',
    last_error: 'no matching transition',
    created_at: '2026-11-20T00:00:00Z',
  },
  {
    id: 'j1',
    issue_key: 'GAME-1',
    action: 'link',
    state: 'delivered',
    attempts: 2,
    title: '#4 Rig',
    url: 'https://nipa.example/acme/game/merges/4',
    created_at: '2026-11-20T00:00:00Z',
  },
  { id: 'j0', issue_key: 'GAME-1', action: 'link', state: 'delivered', attempts: 1, created_at: '2026-11-20T00:00:00Z' },
  { id: 'j9', issue_key: 'GAME-4', action: 'unlink', state: 'pending', attempts: 0, created_at: '2026-11-20T00:00:00Z' },
]

describe('issue tracker jobs', () => {
  async function renderJobs() {
    api.getProjectIssueTracker.mockResolvedValue(SETTINGS)
    api.getIssueTracker.mockResolvedValue(TRACKER)
    api.listIssueTrackerJobs.mockResolvedValue({ jobs: JOBS })
    const view = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => view.container.textContent?.includes('no matching transition') ?? false)
    return view
  }

  it('lists jobs with their detail and retries a failed one', async () => {
    const { container } = await renderJobs()
    expect(api.listIssueTrackerJobs).toHaveBeenLastCalledWith('acme', 'game', '')
    const text = container.textContent ?? ''
    expect(text).toContain('Transition')
    expect(text).toContain('To Done')
    expect(text).toContain('Comment')
    expect(text).toContain('Unlink')
    expect(text).toContain('Remove the merge request link')
    const link = container.querySelector<HTMLAnchorElement>('a[href="https://nipa.example/acme/game/merges/4"]')
    expect(link?.textContent).toBe('#4 Rig')
    expect(Array.from(container.querySelectorAll('button')).filter((b) => b.textContent === 'Retry')).toHaveLength(3)

    api.retryIssueTrackerJob.mockResolvedValue({ ...JOBS[1], state: 'pending', attempts: 0 })
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter((b) => b.textContent === 'Retry')[0]
    await click(retry)
    await flush()
    expect(api.retryIssueTrackerJob).toHaveBeenCalledWith('acme', 'game', 'j2')
    expect(api.listIssueTrackerJobs).toHaveBeenCalledTimes(2)
  })

  it('filters by state and shows retry errors', async () => {
    const { container } = await renderJobs()
    const menuButton = () => container.querySelector<HTMLButtonElement>('button[aria-label="Filter by job state"]')
    await click(menuButton())
    await waitFor(() => document.querySelector('[role="menuitemradio"]') !== null)
    const options = [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')]
    expect(options.map((option) => option.textContent)).toEqual(['All states', 'Failed', 'Pending', 'Delivered'])
    await click(options[1])
    await flush()
    expect(api.listIssueTrackerJobs).toHaveBeenLastCalledWith('acme', 'game', 'failed')
    expect(menuButton()?.textContent).toContain('State: Failed')

    api.retryIssueTrackerJob.mockRejectedValue(new ApiError('the job can no longer be retried', 409))
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent === 'Retry')
    await click(retry)
    await waitFor(() => container.textContent?.includes('can no longer be retried') ?? false)
  })

  it('stays hidden until the project uses the tracker', async () => {
    api.getProjectIssueTracker.mockRejectedValue(new ApiError('not used', 404))
    api.getIssueTracker.mockResolvedValue(TRACKER)
    const { container } = await render(<ProjectIssueTrackerSettings org="acme" project="game" />)
    await waitFor(() => buttonByText(container, 'Save') !== null)
    expect(api.listIssueTrackerJobs).not.toHaveBeenCalled()
  })
})
