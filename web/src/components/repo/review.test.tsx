import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MergeRequestDiff } from './MergeRequestDiff'
import { ReviewPanel } from './ReviewPanel'
import type { DiffFileResponse, ThreadResponse } from '../../api/models'

vi.mock('../../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/endpoints')>()
  return {
    ...actual,
    listOrgMembers: vi.fn(async () => []),
    submitMergeRequestReview: vi.fn(async () => ({ id: '1' })),
    addMergeRequestComment: vi.fn(async () => ({ id: '1' })),
    replyMergeRequestThread: vi.fn(async () => ({ id: '1' })),
    resolveMergeRequestThread: vi.fn(async () => ({ id: '1' })),
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

const file: DiffFileResponse = {
  path: 'code.txt',
  status: 'modified',
  binary: false,
  additions: 1,
  deletions: 0,
  hunks: [
    {
      old_start: 1,
      old_lines: 2,
      new_start: 1,
      new_lines: 3,
      lines: [
        { kind: 'context', old_line: 1, new_line: 1, text: 'one' },
        { kind: 'add', new_line: 2, text: 'two' },
        { kind: 'context', old_line: 2, new_line: 3, text: 'three' },
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
  comments: [{ id: 'c1', thread_id: 't1', user: { user_id: 'u2', name: 'Rev' }, body: 'rename this', system: false, created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z' }],
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

describe('MergeRequestDiff', () => {
  it('renders hunk lines with their numbers and inline thread comments', async () => {
    const { container } = await render(
      <MergeRequestDiff files={[file]} threads={[thread]} canComment onStartThread={() => {}} onOpenThread={() => {}} />,
    )
    const text = container.textContent ?? ''
    expect(text).toContain('code.txt')
    expect(text).toContain('+two')
    expect(text).toContain('rename this')
    expect(text).toContain('1 comment')
  })

  it('asks for a comment anchor when a line is clicked', async () => {
    const anchors: unknown[] = []
    const { container } = await render(
      <MergeRequestDiff
        files={[file]}
        threads={[]}
        canComment
        onStartThread={(filePath, oldLine, newLine) => anchors.push({ filePath, oldLine, newLine })}
        onOpenThread={() => {}}
      />,
    )
    const buttons = [...container.querySelectorAll('button')].filter((b) => b.textContent === 'comment')
    expect(buttons).toHaveLength(3)
    click(buttons[1])
    expect(anchors).toEqual([{ filePath: 'code.txt', oldLine: undefined, newLine: 2 }])
  })

  it('keeps only the lines with open threads when filtering', async () => {
    const { container } = await render(
      <MergeRequestDiff files={[file]} threads={[thread]} canComment={false} onStartThread={() => {}} onOpenThread={() => {}} />,
    )
    const toggle = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Only open threads')
    click(toggle)
    const text = container.textContent ?? ''
    expect(text).toContain('two')
    expect(text).not.toContain('three')
  })

  it('does not treat a resolved thread as an open conversation', async () => {
    const resolved: ThreadResponse = { ...thread, resolved: true }
    const { container } = await render(
      <MergeRequestDiff files={[file]} threads={[resolved]} canComment={false} onStartThread={() => {}} onOpenThread={() => {}} />,
    )
    click([...container.querySelectorAll('button')].find((b) => b.textContent === 'Only open threads'))
    expect(container.textContent).toContain('No lines to show')
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
    draftAnchor: null,
    activeThreadId: null,
    canWrite: true,
    onChanged: () => {},
    onCancelDraft: () => {},
    onOpenThread: () => {},
  }

  it('shows the live review summary and submits a decision', async () => {
    const onChanged = vi.fn()
    const { container } = await render(<ReviewPanel {...base} onChanged={onChanged} />)
    expect(container.textContent).toContain('1 approved')

    const box = container.querySelector('textarea')
    type(box, 'looks good')
    const submit = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Submit review')
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

  it('posts a new inline comment for the chosen anchor', async () => {
    const { container } = await render(
      <ReviewPanel {...base} draftAnchor={{ filePath: 'code.txt', newLine: 2 }} />,
    )
    expect(container.textContent).toContain('New comment on code.txt:2')
    const box = container.querySelector('textarea')
    type(box, 'rename this')
    const send = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Comment')
    await act(async () => {
      ;(send as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.addMergeRequestComment).toHaveBeenCalledWith('acme', 'game', '7', {
      file_path: 'code.txt',
      new_line: 2,
      body: 'rename this',
    })
  })

  it('replies in the selected thread', async () => {
    const { container } = await render(<ReviewPanel {...base} threads={[thread]} activeThreadId="t1" />)
    expect(container.textContent).toContain('Replying to Rev')
    const box = container.querySelector('textarea')
    type(box, 'done')
    const reply = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Reply')
    await act(async () => {
      ;(reply as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(endpoints.replyMergeRequestThread).toHaveBeenCalledWith('acme', 'game', '7', 't1', 'done')
  })

  it('lets the author comment but not decide', async () => {
    const { container } = await render(<ReviewPanel {...base} me="u1" />)
    expect(container.textContent).toContain('This is your own merge request')
    expect([...container.querySelectorAll('button')].some((b) => b.textContent === 'Submit review')).toBe(false)
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
    expect([...container.querySelectorAll('button')].some((b) => b.textContent === 'Dismiss')).toBe(false)
  })
})
