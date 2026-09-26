import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DiffView } from './DiffView'
import { ReviewPanel } from './ReviewPanel'
import { ThreadCard } from './ThreadCard'
import type { DiffFileResponse, ThreadResponse } from '../../api/models'

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

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(node)
  })
  return { container, root }
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
  created_by: 'u1',
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
}

afterEach(() => {
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
    expect(container.textContent).toContain('New comment on code.txt:2')
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
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Edit'))
    type(container.querySelector('[aria-label="Edit comment"]'), 'rename it')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Save'))
    expect(edited).toEqual([{ threadId: 't1', commentId: 'c1', body: 'rename it' }])
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Delete'))
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

describe('ReviewPanel', () => {
  const base = {
    org: 'acme',
    project: 'game',
    id: '7',
    me: 'u2',
    request: mr,
    state: { approvals: 1, changes_requested: 0, dismissed_approvals: 0, outstanding_reviewers: [], head_commit_id: 'c1' },
    reviews: [],
    threads: [],
    reviewRequests: [],
    timeline: [],
    canWrite: true,
    busy: false,
    onChanged: () => {},
    onReplyThread: () => {},
    onResolveThread: () => {},
    onEditComment: () => {},
    onDeleteComment: () => {},
  }

  it('shows the live review summary and submits a decision', async () => {
    const onChanged = vi.fn()
    const { container } = await render(<ReviewPanel {...base} onChanged={onChanged} />)
    expect(container.textContent).toContain('1 approved')

    type(container.querySelector('textarea'), 'looks good')
    const submit = [...container.querySelectorAll('button')].find((button) => button.textContent === 'Submit review')
    await act(async () => {
      ;(submit as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.submitMergeRequestReview).toHaveBeenCalledWith('acme', 'game', '7', {
      state: 'commented',
      body: 'looks good',
      comments: [],
    })
    expect(onChanged).toHaveBeenCalled()
  })

  it('lets the author comment but not decide', async () => {
    const { container } = await render(<ReviewPanel {...base} me="u1" />)
    expect(container.textContent).toContain('This is your own merge request')
    expect([...container.querySelectorAll('button')].some((button) => button.textContent === 'Submit review')).toBe(false)

    type(container.querySelector('textarea'), 'nice work')
    await act(async () => {
      ;(container.querySelector('button') as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
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
      <ReviewPanel {...base} threads={[topLevel]} onReplyThread={(id, body) => replies.push({ id, body })} />,
    )
    expect(container.textContent).toContain('Conversation')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    type(container.querySelector('[aria-label="Reply to thread"]'), 'thanks')
    click([...container.querySelectorAll('button')].find((button) => button.textContent === 'Reply'))
    expect(replies).toEqual([{ id: 't9', body: 'thanks' }])
  })

  it('renders the timeline and the pending review requests', async () => {
    const { container } = await render(
      <ReviewPanel
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
    expect(text).toContain('was asked to review by Author')
    expect(text).toContain('Author requested a review from Peer')
    expect(text).toContain('Author pushed new commits')
  })

  it('marks dismissed reviews as no longer counting', async () => {
    const { container } = await render(
      <ReviewPanel
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
    expect(text).toContain('Approve (outdated)')
    expect(text).toContain('Dismissed by new commits by Author')
    expect([...container.querySelectorAll('button')].some((button) => button.textContent === 'Dismiss')).toBe(false)
  })
})
