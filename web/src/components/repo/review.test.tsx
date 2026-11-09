import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DiffView } from './DiffView'
import { ChangesView } from './ChangesView'
import { MergeRequestOverview } from './MergeRequestOverview'
import { ThreadCard } from './ThreadCard'
import type {
  DiffFileResponse,
  MergeRequestCheckResponse,
  OrgMemberResponse,
  ReviewRequestResponse,
  ReviewResponse,
  ThreadResponse,
  TimelineItemResponse,
} from '../../api/models'

vi.mock('../../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/endpoints')>()
  return {
    ...actual,
    listOrgMembers: vi.fn(async () => []),
    submitMergeRequestReview: vi.fn(async () => ({ id: '1' })),
    addMergeRequestComment: vi.fn(async () => ({ id: '1' })),
    requestMergeRequestReview: vi.fn(async () => ({ id: '1' })),
    removeMergeRequestReviewRequest: vi.fn(async () => ({})),
    dismissMergeRequestReview: vi.fn(async () => ({ id: '1' })),
    withdrawMergeRequestReview: vi.fn(async () => ({})),
  }
})

const endpoints = await import('../../api/endpoints')

const mounted: Array<ReturnType<typeof createRoot>> = []

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  mounted.push(root)
  await act(async () => {
    root.render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>{node}</MemoryRouter>,
    )
  })
  return { container, root }
}

function byLabel(root: ParentNode, label: string): HTMLElement | null {
  const direct = root.querySelector<HTMLElement>(`[aria-label="${label}"]`)
  if (direct) return direct
  return (
    [...root.querySelectorAll<HTMLElement>('[aria-labelledby]')].find((element) =>
      (element.getAttribute('aria-labelledby') ?? '')
        .split(' ')
        .some((id) => document.getElementById(id)?.textContent === label),
    ) ?? null
  )
}

function click(el: Element | null | undefined) {
  act(() => {
    ;(el as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

function type(el: Element | null | undefined, value: string) {
  act(() => {
    const target = el as HTMLTextAreaElement
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    setter?.call(target, value)
    target.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function typeInput(el: Element | null | undefined, value: string) {
  act(() => {
    const target = el as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    setter?.call(target, value)
    target.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function selectOption(el: Element | null | undefined, value: string) {
  act(() => {
    const target = el as HTMLSelectElement
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
    setter?.call(target, value)
    target.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

function rows(container: HTMLElement): HTMLTableRowElement[] {
  return [...container.querySelectorAll('tr')] as HTMLTableRowElement[]
}

function rowWith(container: HTMLElement, text: string): HTMLTableRowElement | undefined {
  return rows(container).find((row) => (row.textContent ?? '').includes(text))
}

const file: DiffFileResponse = {
  path: 'code.txt',
  status: 'modified',
  binary: false,
  additions: 2,
  deletions: 3,
  hunks: [
    {
      old_start: 1,
      old_lines: 4,
      new_start: 1,
      new_lines: 3,
      lines: [
        { kind: 'context', old_line: 1, new_line: 1, text: 'one' },
        { kind: 'remove', old_line: 2, text: 'two-old' },
        { kind: 'add', new_line: 2, text: 'two-new' },
        { kind: 'remove', old_line: 3, text: 'three-old1' },
        { kind: 'remove', old_line: 4, text: 'three-old2' },
        { kind: 'add', new_line: 3, text: 'three-new' },
        { kind: 'context', old_line: 5, new_line: 4, text: 'five' },
      ],
    },
  ],
}

const thread: ThreadResponse = {
  id: 't1',
  merge_request_id: 1,
  file_path: 'code.txt',
  new_line: 2,
  side: 'right',
  outdated: false,
  resolved: false,
  created_by: { user_id: 'u2', name: 'Rev' },
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
  comments: [
    {
      id: 'c1',
      thread_id: 't1',
      user: { user_id: 'u2', name: 'Rev' },
      body: 'rename this',
      system: false,
      created_at: '2024-01-01T00:00:00Z',
      updated_at: '2024-01-01T00:00:00Z',
    },
  ],
}

const mr = {
  id: '1',
  number: 7,
  project_id: 'p',
  source_branch: 'feature',
  target_branch: 'main',
  title: 'Change',
  description: '',
  status: 'open',
  draft: false,
  created_by: 'u1',
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
}

afterEach(async () => {
  await act(async () => {
    for (const root of mounted.splice(0)) root.unmount()
  })
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

describe('DiffView', () => {
  it('pairs a remove run with the add run side by side and pads the short side', async () => {
    const { container } = await render(<DiffView files={[file]} />)
    const paired = rowWith(container, 'two-old')
    expect(paired?.textContent).toContain('two-new')
    expect(rowWith(container, 'three-old1')?.textContent).toContain('three-new')
    const padded = rowWith(container, 'three-old2')
    expect(padded?.textContent).not.toContain('three-new')
    expect(rows(container).find((row) => (row.textContent ?? '').includes('three-old2') && (row.textContent ?? '').includes('three-old1'))).toBeUndefined()
  })

  it('renders context on both sides with their own line numbers', async () => {
    const { container } = await render(<DiffView files={[file]} />)
    const context = rowWith(container, 'one')
    expect(context?.textContent).toContain('1')
    expect(rowWith(container, 'five')?.textContent).toContain('5')
  })

  it('switches to unified view', async () => {
    const { container } = await render(<DiffView files={[file]} />)
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Unified'))
    expect(rowWith(container, 'two-old')?.textContent).not.toContain('two-new')
    expect(rowWith(container, 'two-new')?.textContent).toContain('two-new')
  })

  it('floats a thread directly under its anchored line', async () => {
    const { container } = await render(<DiffView files={[file]} threads={[thread]} canComment />)
    const all = rows(container)
    const line = all.findIndex((row) => (row.textContent ?? '').includes('two-new'))
    expect(line).toBeGreaterThanOrEqual(0)
    expect(all[line + 1]?.textContent).toContain('rename this')
  })

  it('asks for a thread anchor from the line gutter', async () => {
    const anchors: unknown[] = []
    const { container } = await render(
      <DiffView files={[file]} canComment onStartThread={(anchor) => anchors.push(anchor)} />,
    )
    click(container.querySelector('[aria-label="Comment on left line 2"]'))
    click(container.querySelector('[aria-label="Comment on right line 2"]'))
    expect(anchors).toEqual([
      { filePath: 'code.txt', oldLine: 2 },
      { filePath: 'code.txt', newLine: 2 },
    ])
  })

  it('opens the inline composer under the anchored row and posts the body', async () => {
    const submitted: unknown[] = []
    const { container } = await render(
      <DiffView
        files={[file]}
        canComment
        draftAnchor={{ filePath: 'code.txt', newLine: 2 }}
        onSubmitDraft={(anchor, body) => submitted.push({ anchor, body })}
      />,
    )
    expect(container.textContent).toContain('a comment on code.txt:2')
    type(container.querySelector('[aria-label="New inline comment"]'), 'rename this')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Comment'))
    expect(submitted).toEqual([{ anchor: { filePath: 'code.txt', newLine: 2 }, body: 'rename this' }])
  })

  it('filters to the lines with open threads', async () => {
    const { container } = await render(<DiffView files={[file]} threads={[thread]} canComment />)
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Only open threads'))
    const text = container.textContent ?? ''
    expect(text).toContain('two-new')
    expect(text).not.toContain('five')
  })

  it('matches comments on a renamed file by its old path too', async () => {
    const renamed: DiffFileResponse = {
      ...file,
      path: 'new.txt',
      old_path: 'old.txt',
      status: 'renamed',
    }
    const oldSide: ThreadResponse = {
      ...thread,
      id: 't-old',
      file_path: 'old.txt',
      side: 'left',
      new_line: undefined,
      old_line: 2,
      comments: [
        {
          ...thread.comments[0],
          thread_id: 't-old',
          body: 'this removed line matters',
        },
      ],
    }
    const { container } = await render(<DiffView files={[renamed]} threads={[oldSide]} canComment />)
    expect(container.textContent).toContain('this removed line matters')
    expect(container.textContent).toContain('1 open thread')

    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Only open threads'))
    expect(container.textContent).toContain('two-old')
  })

  it('does not treat a resolved thread as an open conversation', async () => {
    const resolved: ThreadResponse = { ...thread, resolved: true }
    const { container } = await render(<DiffView files={[file]} threads={[resolved]} canComment />)
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Only open threads'))
    expect(container.textContent).toContain('No lines to show')
  })
})

describe('ThreadCard', () => {
  it('lets the author edit and delete their own comment', async () => {
    const edited: unknown[] = []
    const deleted: unknown[] = []
    const { container } = await render(
      <ThreadCard
        thread={thread}
        me="u2"
        canWrite
        onEditComment={(threadId, commentId, body) => edited.push({ threadId, commentId, body })}
        onDeleteComment={(threadId, commentId) => deleted.push({ threadId, commentId })}
      />,
    )
    const iconButtons = () => container.querySelectorAll('[data-component="IconButton"]')
    click(iconButtons()[0])
    type(container.querySelector('[aria-label="Edit comment"]'), 'rename it')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Save'))
    expect(edited).toEqual([{ threadId: 't1', commentId: 'c1', body: 'rename it' }])
    click(iconButtons()[1])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    const dialog = document.querySelector('[role="alertdialog"]') as HTMLElement
    expect(dialog).not.toBeNull()
    click([...dialog.querySelectorAll('button')].find((button) => button.textContent === 'Delete'))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(deleted).toEqual([{ threadId: 't1', commentId: 'c1' }])
  })

  it('replies and resolves', async () => {
    const replies: unknown[] = []
    const resolved: unknown[] = []
    const { container } = await render(
      <ThreadCard
        thread={thread}
        me="u1"
        canWrite
        onReply={(threadId, body) => replies.push({ threadId, body })}
        onResolve={(threadId, value) => resolved.push({ threadId, value })}
      />,
    )
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    type(container.querySelector('[aria-label="Reply to thread"]'), 'done')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    expect(replies).toEqual([{ threadId: 't1', body: 'done' }])
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Resolve'))
    expect(resolved).toEqual([{ threadId: 't1', value: true }])
  })
})

describe('MergeRequestOverview', () => {
  const base = {
    org: 'acme',
    project: 'game',
    id: '7',
    me: 'u2',
    request: { ...mr, description: 'Please merge this', mergeability: { status: 'mergeable' } },
    state: {
      approvals: 1,
      changes_requested: 0,
      dismissed_approvals: 0,
      outstanding_reviewers: [],
      head_commit_id: 'c1',
    },
    reviews: [] as ReviewResponse[],
    threads: [] as ThreadResponse[],
    checks: [] as MergeRequestCheckResponse[],
    reviewRequests: [] as ReviewRequestResponse[],
    timeline: [] as TimelineItemResponse[],
    members: [],
    files: [] as DiffFileResponse[],
    canWrite: true,
    busy: false,
    onChanged: () => {},
    onReplyThread: () => {},
    onResolveThread: () => {},
    onEditComment: () => {},
    onDeleteComment: () => {},
    onMerge: () => {},
    onClose: () => {},
    onReopen: () => {},
  }

  it('shows the review summary and submits a decision', async () => {
    const onChanged = vi.fn()
    const { container } = await render(<MergeRequestOverview {...base} onChanged={onChanged} />)
    expect(container.textContent).toContain('Changes approved')
    expect(container.textContent).toContain('1 approved review')
    expect(container.textContent).toContain('opened this merge request')
    expect(container.textContent).toContain('Please merge this')

    const button = (label: string) =>
      [...container.querySelectorAll('button')].find((entry) => entry.textContent === label) as HTMLButtonElement
    expect(button('Approve')).toBeUndefined()
    click(button('Review changes'))
    expect(button('Comment')).toBeUndefined()
    click(container.querySelector('input[type="radio"][value="approved"]'))
    expect(button('Submit review').disabled).toBe(true)
    type(container.querySelector('[aria-label="Add a comment"]'), 'looks good')
    await act(async () => {
      button('Submit review').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.submitMergeRequestReview).toHaveBeenCalledWith('acme', 'game', '7', {
      state: 'approved',
      body: 'looks good',
      comments: [],
    })
    expect(onChanged).toHaveBeenCalled()
    expect(button('Review changes')).toBeDefined()
  })

  it('cancels a review and requests changes', async () => {
    const { container } = await render(<MergeRequestOverview {...base} />)
    const button = (label: string) =>
      [...container.querySelectorAll('button')].find((entry) => entry.textContent === label) as HTMLButtonElement
    click(button('Review changes'))
    click(button('Cancel review'))
    expect(container.querySelector('[aria-label="Review decision"]')).toBeNull()
    click(button('Review changes'))
    click(container.querySelector('input[type="radio"][value="changes_requested"]'))
    type(container.querySelector('[aria-label="Add a comment"]'), 'needs work')
    await act(async () => {
      button('Submit review').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.submitMergeRequestReview).toHaveBeenCalledWith('acme', 'game', '7', {
      state: 'changes_requested',
      body: 'needs work',
      comments: [],
    })
  })

  it('lets the author comment but not decide', async () => {
    const { container } = await render(<MergeRequestOverview {...base} me="u1" />)
    expect([...container.querySelectorAll('button')].some((button) => button.textContent === 'Review changes')).toBe(false)

    type(container.querySelector('[aria-label="Add a comment"]'), 'nice work')
    await act(async () => {
      const comment = [...container.querySelectorAll('button')].find((button) => button.textContent === 'Comment')
      ;(comment as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.addMergeRequestComment).toHaveBeenCalledWith('acme', 'game', '7', {
      file_path: '',
      body: 'nice work',
    })
  })

  it('renders the conversation and forwards thread actions', async () => {
    const replies: unknown[] = []
    const topLevel: ThreadResponse = { ...thread, id: 't9', file_path: undefined, new_line: undefined, side: '' }
    const { container } = await render(
      <MergeRequestOverview {...base} threads={[topLevel]} onReplyThread={(id, body) => replies.push({ id, body })} />,
    )
    expect(container.textContent).toContain('rename this')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    type(container.querySelector('[aria-label="Reply to thread"]'), 'thanks')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    expect(replies).toEqual([{ id: 't9', body: 'thanks' }])
  })

  it('renders the timeline and the pending review requests', async () => {
    const { container } = await render(
      <MergeRequestOverview
        {...base}
        reviewRequests={[
          {
            id: 'r1',
            merge_request_id: 1,
            reviewer: { user_id: 'u3', name: 'Peer' },
            requested_by: { user_id: 'u1', name: 'Author' },
            created_at: '2024-01-01T00:00:00Z',
          },
        ]}
        timeline={[
          {
            id: 'e1',
            kind: 'review_requested',
            actor: { user_id: 'u1', name: 'Author' },
            subject: { user_id: 'u3', name: 'Peer' },
            created_at: '2024-01-01T00:00:00Z',
          },
          { id: 'e2', kind: 'pushed', actor: { user_id: 'u1', name: 'Author' }, created_at: '2024-01-02T00:00:00Z' },
        ]}
      />,
    )
    const text = container.textContent ?? ''
    expect(text).toContain('Peer')
    expect(text).toContain('Author requested a review from')
    expect(text).toContain('Author pushed new commits')
    expect(container.querySelector('[title="review pending"]')).not.toBeNull()
  })

  it('marks dismissed reviews as no longer counting', async () => {
    const { container } = await render(
      <MergeRequestOverview
        {...base}
        reviews={[
          {
            id: 'r9',
            merge_request_id: 1,
            reviewer: { user_id: 'u3', name: 'Peer' },
            state: 'approved',
            body: 'ok',
            head_commit_id: 'c0',
            stale: true,
            dismissed_at: '2024-01-03T00:00:00Z',
            dismissed_by: { user_id: 'u1', name: 'Author' },
            dismissed_reason: 'new_commits',
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
        ]}
      />,
    )
    const text = container.textContent ?? ''
    expect(text).toContain('had their review dismissed')
    expect(text).toContain('Dismissed by new commits')
    expect([...container.querySelectorAll('button')].some((button) => button.textContent === 'Dismiss')).toBe(false)
  })

  const liveReview = (reviewer: { user_id: string; name: string }): ReviewResponse => ({
    id: 'r5',
    merge_request_id: 1,
    reviewer,
    state: 'approved',
    body: 'ok',
    head_commit_id: 'c1',
    stale: false,
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
  })

  async function confirmInDialog(label: string) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    const dialog = document.querySelector('[role="alertdialog"]') as HTMLElement
    expect(dialog).not.toBeNull()
    click([...dialog.querySelectorAll('button')].find((button) => button.textContent === label))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }

  it('lets the reviewer withdraw their own review after confirmation', async () => {
    const changed = vi.fn()
    const { container } = await render(
      <MergeRequestOverview {...base} me="u2" onChanged={changed} reviews={[liveReview({ user_id: 'u2', name: 'Me' })]} />,
    )
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Withdraw'))
    await confirmInDialog('Withdraw')
    expect(endpoints.withdrawMergeRequestReview).toHaveBeenCalledWith('acme', 'game', '7', 'r5')
    expect(changed).toHaveBeenCalled()
  })

  it('dismisses someone elses review after confirmation and keeps it when cancelled', async () => {
    vi.mocked(endpoints.dismissMergeRequestReview).mockClear()
    const { container } = await render(
      <MergeRequestOverview {...base} me="u2" reviews={[liveReview({ user_id: 'u3', name: 'Peer' })]} />,
    )
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Dismiss'))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    click(
      [...(document.querySelector('[role="alertdialog"]') as HTMLElement).querySelectorAll('button')].find(
        (button) => button.textContent === 'Cancel',
      ),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(endpoints.dismissMergeRequestReview).not.toHaveBeenCalled()

    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Dismiss'))
    await confirmInDialog('Dismiss')
    expect(endpoints.dismissMergeRequestReview).toHaveBeenCalledWith('acme', 'game', '7', 'r5')
  })

  it('shows a dismissible banner when a review action fails', async () => {
    vi.mocked(endpoints.withdrawMergeRequestReview).mockRejectedValueOnce(new Error('review is locked'))
    const { container } = await render(
      <MergeRequestOverview {...base} me="u2" reviews={[liveReview({ user_id: 'u2', name: 'Me' })]} />,
    )
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Withdraw'))
    await confirmInDialog('Withdraw')
    expect(container.textContent).toContain('review is locked')
    click(container.querySelector('button[class*="BannerDismiss"]'))
    await act(async () => {
      await Promise.resolve()
    })
    expect(container.textContent).not.toContain('review is locked')
  })

  it('collapses resolved line conversations until shown', async () => {
    const open: ThreadResponse = { ...thread, id: 'ta', resolved: false, comments: [{ ...thread.comments[0], id: 'ca', body: 'still open body' }] }
    const done: ThreadResponse = {
      ...thread,
      id: 'tb',
      resolved: true,
      resolved_by: { user_id: 'u3', name: 'Peer' },
      comments: [{ ...thread.comments[0], id: 'cb', body: 'already fixed body' }],
    }
    const { container } = await render(<MergeRequestOverview {...base} threads={[open, done]} />)
    const button = (label: string) =>
      [...container.querySelectorAll('button')].find((entry) => entry.textContent === label) as HTMLButtonElement
    expect(container.textContent).toContain('still open body')
    expect(container.textContent).not.toContain('already fixed body')
    expect(container.textContent).toContain('Peer resolved a conversation on')
    click(button('Show resolved'))
    expect(container.textContent).toContain('already fixed body')
    expect(container.textContent).toContain('Resolved')
    click(button('Hide resolved'))
    expect(container.textContent).not.toContain('already fixed body')
  })

  it('shows inline code threads with a link to the file', async () => {
    const inlineThread: ThreadResponse = { ...thread, id: 't2', file_path: 'code.txt', new_line: 2 }
    const { container } = await render(
      <MergeRequestOverview {...base} files={[file]} threads={[inlineThread]} />,
    )
    expect(container.textContent).toContain('code.txt:2')
    expect(container.textContent).toContain('View on file')
    expect(container.querySelector('a[href="/acme/game/merges/7?tab=files#file-code.txt"]')).not.toBeNull()
    expect(container.textContent).toContain('rename this')
    expect(container.textContent).toContain('two-new')
  })

  it.each([
    ['mergeable', 'This branch has no conflicts with the base branch.'],
    ['behind_target', 'The source branch is behind the target branch.'],
    ['up_to_date', 'The source branch is already up to date with the target branch.'],
    ['invalid', 'The source or target branch no longer exists.'],
  ])('explains the %s mergeability status', async (status, expected) => {
    const request = { ...base.request, mergeability: { status } }
    const { container } = await render(<MergeRequestOverview {...base} request={request} />)
    expect(container.textContent).toContain(expected)
    const mergeButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Merge request',
    ) as HTMLButtonElement
    expect(mergeButton.disabled).toBe(status !== 'mergeable')
  })

  it.each([
    ['changes_requested', 'Changes were requested on this merge request.'],
    ['insufficient_approvals', 'This merge request does not have enough approvals yet.'],
  ])('explains the %s review block and disables merge', async (blockedBy, expected) => {
    const request = { ...base.request, mergeability: { status: 'mergeable', blocked_by: blockedBy } }
    const { container } = await render(<MergeRequestOverview {...base} request={request} />)
    expect(container.textContent).toContain(expected)
    const mergeButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Merge request',
    ) as HTMLButtonElement
    expect(mergeButton.disabled).toBe(true)
  })

  it('enables a diverged merge for non-fast-forward strategies and forwards the choice', async () => {
    const onMerge = vi.fn()
    const request = { ...base.request, mergeability: { status: 'behind_target' } }
    const { container } = await render(<MergeRequestOverview {...base} request={request} onMerge={onMerge} />)
    const mergeButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Merge request',
    ) as HTMLButtonElement
    expect(mergeButton.disabled).toBe(true)

    selectOption(container.querySelector('[aria-label="Merge strategy"]'), 'squash')
    expect(mergeButton.disabled).toBe(false)

    click(container.querySelector('[aria-label="Delete source branch"]'))
    click(mergeButton)
    expect(onMerge).toHaveBeenCalledWith('squash', true)
  })

  it('renders assignees and the assign picker', async () => {
    const request = { ...base.request, assignees: [{ user_id: 'u3', name: 'Dana' }] }
    const members = [{ user_id: 'u4', name: 'Eve', email: 'eve@example.com' }] as OrgMemberResponse[]
    const { container } = await render(
      <MergeRequestOverview {...base} request={request} members={members} />,
    )
    expect(container.textContent).toContain('Assignees')
    expect(container.textContent).toContain('Dana')
    expect(container.querySelector('[aria-label="Add assignee"]')).not.toBeNull()
    expect(byLabel(container, 'unassign Dana')).not.toBeNull()
  })

  it('shows merged and closed requests', async () => {
    const merged = { ...base.request, status: 'merged', merge_commit_id: 'abcdef1234567' }
    const first = await render(<MergeRequestOverview {...base} request={merged} />)
    expect(first.container.textContent).toContain('Merge request successfully merged')
    expect(first.container.textContent).toContain('abcdef1234')
    expect([...first.container.querySelectorAll('button')].some((entry) => entry.textContent === 'Close merge request')).toBe(false)

    const onReopen = vi.fn()
    const closed = { ...base.request, status: 'closed' }
    const second = await render(<MergeRequestOverview {...base} request={closed} onReopen={onReopen} />)
    expect(second.container.textContent).toContain('Closed with unmerged commits')
    click([...second.container.querySelectorAll('button')].find((entry) => entry.textContent === 'Reopen merge request'))
    expect(onReopen).toHaveBeenCalled()
  })

  it('closes from the comment box', async () => {
    const onClose = vi.fn()
    const { container } = await render(<MergeRequestOverview {...base} onClose={onClose} />)
    click([...container.querySelectorAll('button')].find((entry) => entry.textContent === 'Close merge request'))
    expect(onClose).toHaveBeenCalled()
  })

  it('summarizes checks, reviews and drafts in the merge box', async () => {
    const checks = [
      { id: 'k1', name: 'build', state: 'success', details_url: 'https://ci.test/1' },
      { id: 'k2', name: 'lint', state: 'failed' },
      { id: 'k3', name: 'e2e', state: 'pending' },
    ] as MergeRequestCheckResponse[]
    const draft = { ...base.request, draft: true, mergeability: { status: 'mergeable', blocked_by: 'draft' } }
    const { container } = await render(
      <MergeRequestOverview {...base} request={draft} checks={checks} state={{ ...base.state, approvals: 0, changes_requested: 2 }} />,
    )
    const text = container.textContent ?? ''
    expect(text).toContain('Changes requested')
    expect(text).toContain('2 reviews requesting changes')
    expect(text).toContain('Some checks were not successful')
    expect(text).toContain('1 successful, 1 failing, 1 pending')
    expect(container.querySelector('a[href="https://ci.test/1"]')).not.toBeNull()
    expect(text).toContain('This merge request is still a work in progress.')

    const pendingOnly = await render(
      <MergeRequestOverview
        {...base}
        checks={[{ id: 'k3', name: 'e2e', state: 'pending' }] as MergeRequestCheckResponse[]}
        state={{ ...base.state, approvals: 0 }}
        request={{ ...base.request, mergeability: { status: 'mergeable', blocked_by: 'insufficient_approvals' } }}
      />,
    )
    expect(pendingOnly.container.textContent).toContain("Some checks haven't completed yet")
    expect(pendingOnly.container.textContent).toContain('Review required')

    const passing = await render(
      <MergeRequestOverview
        {...base}
        checks={[{ id: 'k1', name: 'build', state: 'success' }] as MergeRequestCheckResponse[]}
        state={null}
      />,
    )
    expect(passing.container.textContent).toContain('All checks have passed')
    expect(passing.container.textContent).toContain('No reviews yet')
  })

  it('labels every timeline event', async () => {
    const actor = { user_id: 'u1', name: 'Author' }
    const events: TimelineItemResponse[] = [
      { id: 'e1', kind: 'pushed', actor, commit_id: 'abc1234567', commit_hash: 'deadbeef99', created_at: '2024-01-02T00:00:00Z' },
      { id: 'e2', kind: 'review_request_removed', actor, subject: { user_id: 'u3', name: 'Peer' }, created_at: '2024-01-03T00:00:00Z' },
      { id: 'e3', kind: 'review_dismissed', actor, created_at: '2024-01-04T00:00:00Z' },
      { id: 'e4', kind: 'closed', actor, created_at: '2024-01-05T00:00:00Z' },
      { id: 'e5', kind: 'reopened', actor, created_at: '2024-01-06T00:00:00Z' },
      { id: 'e6', kind: 'ready_for_review', actor, created_at: '2024-01-07T00:00:00Z' },
      { id: 'e7', kind: 'merged', actor, commit_hash: 'cafebabe11', created_at: '2024-01-08T00:00:00Z' },
      { id: 'e8', kind: 'labels_changed', actor, created_at: '2024-01-09T00:00:00Z' },
      { id: 'e9', kind: 'opened', actor, created_at: '2024-01-01T00:00:00Z' },
    ]
    const { container } = await render(<MergeRequestOverview {...base} timeline={events} />)
    const text = container.textContent ?? ''
    expect(text).toContain('Author pushed new commits deadbee')
    expect(container.querySelector('a[href="/acme/game/commits/abc1234567"]')).not.toBeNull()
    expect(text).toContain('Author removed the review request for Peer')
    expect(text).toContain('Author dismissed a review')
    expect(text).toContain('Author closed this merge request')
    expect(text).toContain('Author reopened this merge request')
    expect(text).toContain('Author marked this merge request as ready for review')
    expect(text).toContain('Author merged commit cafebab')
    expect(text).toContain('Author labels changed')
  })

  it('shows reviews with a comment box and reviewer states in the sidebar', async () => {
    const reviews: ReviewResponse[] = [
      { ...liveReview({ user_id: 'u3', name: 'Peer' }), id: 'r1', state: 'changes_requested', body: 'fix the **name**' },
      { ...liveReview({ user_id: 'u4', name: 'Quin' }), id: 'r2', state: 'commented', body: '' },
    ]
    const { container } = await render(<MergeRequestOverview {...base} reviews={reviews} />)
    const text = container.textContent ?? ''
    expect(text).toContain('Peer requested changes')
    expect(text).toContain('Peer left a comment')
    expect(text).toContain('Quin reviewed')
    expect(text).not.toContain('Quin left a comment')
    expect(container.querySelector('[title="requested changes"]')).not.toBeNull()
    expect(container.querySelector('[title="reviewed"]')).not.toBeNull()
    expect(text).toContain('No one assigned')
  })
})

describe('ChangesView', () => {
  const addedFile: DiffFileResponse = {
    path: 'docs/readme.md',
    status: 'added',
    binary: false,
    additions: 1,
    deletions: 0,
    hunks: [
      {
        old_start: 0,
        old_lines: 0,
        new_start: 1,
        new_lines: 1,
        lines: [{ kind: 'add', new_line: 1, text: 'hello docs' }],
      },
    ],
  }

  it('lists changed files and filters them from the toolbar', async () => {
    const { container } = await render(<ChangesView files={[file, addedFile]} />)
    expect(container.textContent).toContain('2 files changed')
    expect(container.querySelector('a[href="#file-code.txt"]')).not.toBeNull()
    expect(container.querySelector('a[href="#file-docs/readme.md"]')).not.toBeNull()

    typeInput(container.querySelector('[aria-label="Filter files"]'), 'docs')
    expect(container.textContent).toContain('1 file changed')
    expect(container.textContent).not.toContain('code.txt')
    expect(container.textContent).toContain('hello docs')
  })

  it('marks files as viewed and collapses them', async () => {
    const { container } = await render(<ChangesView files={[file, addedFile]} />)
    click(container.querySelector('[aria-label="Viewed docs/readme.md"]'))
    expect((container.querySelector('[aria-label="Viewed docs/readme.md"]') as HTMLInputElement).checked).toBe(true)

    click(container.querySelector('[aria-label="Collapse docs/readme.md"]'))
    expect(container.textContent).not.toContain('hello docs')
    click(container.querySelector('[aria-label="Expand docs/readme.md"]'))
    expect(container.textContent).toContain('hello docs')
  })
})

