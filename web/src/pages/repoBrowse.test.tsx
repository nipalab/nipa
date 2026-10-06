import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { clearSession } from '../api/client'

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as Response
}

function textResponse(text: string, contentType = 'text/plain; charset=utf-8'): Response {
  const blob = {
    size: text.length,
    type: contentType,
    text: async () => text,
    arrayBuffer: async () => new TextEncoder().encode(text).buffer,
  } as unknown as Blob
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'Content-Type': contentType }),
    blob: async () => blob,
    text: async () => text,
  } as Response
}

async function waitFor(condition: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (condition()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for condition')
}

async function waitForText(container: HTMLElement, text: string, timeoutMs = 2000) {
  await waitFor(() => container.textContent?.includes(text) ?? false, timeoutMs)
}

const ME = {
  id: '1',
  name: 'Alice',
  email: 'alice@example.com',
  photo_url: '',
  is_admin: true,
  is_super_admin: false,
  deleted: false,
}

const BRANCHES = [
  { id: '1', name: 'main', is_default: true, is_protected: false, commit_id: 'c2', updated_at: new Date().toISOString() },
]

function stubRepoRoutes(extra: (url: string, init?: RequestInit) => Response | null = () => null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const custom = extra(url, init)
      if (custom) return custom
      if (url.includes('/auth/refresh')) {
        return jsonResponse({ access_token: 'token', token_type: 'Bearer', expires_in: 1800 })
      }
      if (url.includes('/api/v1/me')) return jsonResponse(ME)
      if (url.includes('/branches')) return jsonResponse(BRANCHES)
      if (url.includes('/permissions/me')) {
        return jsonResponse({ project_permission: 3, rules: [], defaults: [] })
      }
      return jsonResponse({ error: 'not found' }, 404)
    }),
  )
}

function findButton(text: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll('button')).find((item) => item.textContent?.trim() === text)
  if (!button) throw new Error(`button ${text} not found`)
  return button as HTMLButtonElement
}

function setInputValue(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(el: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

function setTextareaValue(el: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')?.set
  setter?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

async function renderApp(url: string) {
  window.history.pushState({}, '', url)
  const container = document.createElement('div')
  container.id = 'root'
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<App />)
  })
  return { container, root }
}

beforeEach(() => {
  clearSession()
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('RepoPage', () => {
  it('renders the tree with last commits and the readme', async () => {
    const now = new Date().toISOString()
    stubRepoRoutes((url) => {
      if (url.includes('/tree?')) {
        return jsonResponse({
          path: '',
          latest_commit: { id: 'c2', message: 'update readme', author_name: 'Bob', created_at: now },
          entries: [
            {
              name: 'src',
              path: 'src',
              type: 'tree',
              last_commit: { id: 'c1', message: 'add sources', author_name: 'Alice', created_at: now },
            },
            {
              name: 'README.md',
              path: 'README.md',
              type: 'file',
              size_bytes: 12,
              hash: 'aabbccddeeff',
              last_commit: { id: 'c2', message: 'update readme', author_name: 'Bob', created_at: now },
            },
          ],
        })
      }
      if (url.includes('/blob?') && url.includes('README.md')) {
        return textResponse('# Hello Nipa\n\nWelcome to the repo.')
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game')
    await waitForText(container, 'Hello Nipa')
    expect(container.textContent).toContain('update readme')
    expect(container.textContent).toContain('add sources')
    expect(container.querySelector('h1')?.textContent).toBe('Hello Nipa')
    expect(container.querySelector('a[href="/acme/game/tree/main/src"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/blob/main/README.md"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/commits?branch=main"]')).not.toBeNull()
    act(() => root.unmount())
  })

  it('shows an empty state for a branch without commits', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/branches')) {
        return jsonResponse([
          { id: '1', name: 'main', is_default: true, is_protected: false, updated_at: new Date().toISOString() },
        ])
      }
      if (url.includes('/tree?')) return jsonResponse({ path: '', entries: [] })
      return null
    })

    const { container, root } = await renderApp('/acme/game')
    await waitForText(container, 'This branch has no commits yet.')
    act(() => root.unmount())
  })

  it('shows an empty state for a project without branches instead of an error', async () => {
    const treeCalls: string[] = []
    stubRepoRoutes((url) => {
      if (url.includes('/branches')) return jsonResponse([])
      if (url.includes('/tree?')) {
        treeCalls.push(url)
        return jsonResponse({ error: 'record not found' }, 404)
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game')
    await waitForText(container, 'This repository is empty.')
    expect(container.textContent).not.toContain('Request failed')
    expect(treeCalls).toHaveLength(0)
    act(() => root.unmount())
  })

  it('shows an empty state for commits of a project without branches', async () => {
    const commitCalls: string[] = []
    stubRepoRoutes((url) => {
      if (url.includes('/branches')) return jsonResponse([])
      if (url.includes('/commits')) {
        commitCalls.push(url)
        return jsonResponse({ error: 'record not found' }, 404)
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/commits')
    await waitForText(container, 'This repository is empty.')
    expect(container.textContent).not.toContain('Request failed')
    expect(commitCalls).toHaveLength(0)
    act(() => root.unmount())
  })

  it('redirects legacy query-param URLs to path URLs', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/tree?')) return jsonResponse({ path: 'src', entries: [] })
      return null
    })

    const { root } = await renderApp('/acme/game?rev=main&path=src')
    await waitFor(() => window.location.pathname === '/acme/game/tree/main/src')
    act(() => root.unmount())
  })

  it('resolves branch names containing slashes', async () => {
    const urls: string[] = []
    stubRepoRoutes((url) => {
      if (url.includes('/tree?')) {
        urls.push(url)
        return jsonResponse({ path: '', entries: [] })
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/tree/feature%2Fx')
    await waitFor(() => urls.length > 0)
    expect(urls[0]).toContain('rev=feature%2Fx')
    await waitForText(container, 'This directory is empty.')
    act(() => root.unmount())
  })

  it('opens go-to-file and navigates to the selected file', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('recursive=1')) {
        return jsonResponse({
          path: '',
          entries: [{ name: 'main.ts', path: 'src/main.ts', type: 'file', size_bytes: 10, hash: 'aa' }],
        })
      }
      if (url.includes('/tree?')) return jsonResponse({ path: '', entries: [] })
      if (url.includes('/blob?')) return textResponse('const x = 1\n')
      return null
    })

    const { container, root } = await renderApp('/acme/game')
    await waitForText(container, 'Go to file')
    const openButton = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Go to file'),
    )
    await act(async () => {
      openButton?.click()
    })
    await waitFor(() => document.body.textContent?.includes('src/main.ts') ?? false)
    const fileButton = Array.from(document.body.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('src/main.ts'),
    )
    await act(async () => {
      fileButton?.click()
    })
    await waitFor(() => window.location.pathname === '/acme/game/blob/main/src/main.ts')
    act(() => root.unmount())
  })
})

describe('BlobPage', () => {
  it('renders text blobs with line numbers', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/blob?')) return textResponse('const x = 1\nconsole.log(x)\n')
      return null
    })

    const { container, root } = await renderApp('/acme/game/blob/main/src/main.ts')
    await waitForText(container, 'console.log')
    expect(container.textContent).toContain('2 lines')
    const lines = Array.from(container.querySelectorAll('.nipa-code-line')).map((el) => el.textContent)
    expect(lines).toHaveLength(2)
    expect(lines[0]?.startsWith('1')).toBe(true)
    expect(lines[1]?.startsWith('2')).toBe(true)
    expect(container.querySelector('a[href="/acme/game/commits?branch=main&path=src%2Fmain.ts"]')).not.toBeNull()
    act(() => root.unmount())
  })

  it('previews image blobs', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/blob?')) return textResponse('\x00\x01binary', 'application/octet-stream')
      return null
    })

    const { container, root } = await renderApp('/acme/game/blob/main/assets/logo.png')
    await waitFor(() => container.querySelector('img') !== null)
    expect(container.querySelector('img')?.getAttribute('src')).toBe('blob:nipa-test')
    act(() => root.unmount())
  })

  it('shows the server error for oversized blobs', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/blob?')) {
        return jsonResponse({ error: 'file is too large for the web viewer' }, 413)
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/blob/main/big.bin')
    await waitForText(container, 'file is too large for the web viewer')
    act(() => root.unmount())
  })
})

describe('repo nav', () => {
  it('keeps the repository tabs on commits, branches, merges and settings', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/commits?')) return jsonResponse([])
      if (url.includes('/merge-requests')) return jsonResponse({ merge_requests: [] })
      if (url.includes('/permissions/rules')) return jsonResponse([])
      if (url.includes('/permissions/defaults')) return jsonResponse([])
      return null
    })

    const tabs = [
      ['/acme/game/commits', 'Commits'],
      ['/acme/game/branches', 'Branches'],
      ['/acme/game/merges', 'Merge requests'],
      ['/acme/game/settings', 'Settings'],
    ] as const

    for (const [url, active] of tabs) {
      const { container, root } = await renderApp(url)
      await waitFor(() => container.querySelector('[aria-current="page"]')?.textContent === active)
      expect(container.querySelector('a[href="/acme/game/tree/main"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/commits?branch=main"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/branches"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/merges"]')).not.toBeNull()
      act(() => root.unmount())
    }
  })
})

describe('MergeRequestsPage', () => {
  const TWO_BRANCHES = [
    { id: '1', name: 'main', is_default: true, is_protected: false, updated_at: new Date().toISOString() },
    { id: '2', name: 'feature', is_default: false, is_protected: false, updated_at: new Date().toISOString() },
  ]

  it('creates a merge request from the dedicated page', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    const created = {
      id: '1',
      number: 7,
      project_id: 'p1',
      source_branch: 'feature',
      target_branch: 'main',
      title: 'Add feature',
      description: '',
      status: 'open',
      created_by: '1',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url.includes('/merge-requests') && method === 'POST') return jsonResponse(created)
      if (url.includes('/merge-requests/7/diff') && method === 'GET') return jsonResponse({ files: [] })
      if (url.includes('/merge-requests/7/review-state') && method === 'GET') return jsonResponse({})
      if (url.includes('/merge-requests/7/') && method === 'GET') return jsonResponse([])
      if (url.endsWith('/merge-requests/7') && method === 'GET') return jsonResponse(created)
      if (url.includes('/merge-requests') && method === 'GET') return jsonResponse({ merge_requests: [] })
      if (url.includes('/branches') && method === 'GET') return jsonResponse(TWO_BRANCHES)
      return null
    })

    const { container, root } = await renderApp('/acme/game/merges')
    await waitForText(container, 'No merge requests.')
    await act(async () => {
      findButton('New merge request').click()
    })
    await waitFor(() => window.location.pathname === '/acme/game/merges/new')
    await waitFor(() => container.querySelector('form') !== null)

    const titleInput = container.querySelector('input') as HTMLInputElement
    const [sourceSelect, targetSelect] = Array.from(container.querySelectorAll('select')) as HTMLSelectElement[]
    await act(async () => {
      setInputValue(titleInput, 'Add feature')
      setSelectValue(sourceSelect, 'feature')
      setSelectValue(targetSelect, 'main')
    })
    await act(async () => {
      findButton('Create merge request').click()
    })
    await waitFor(() => calls.some((call) => call.method === 'POST' && call.url.includes('/merge-requests')))
    const post = calls.find((call) => call.method === 'POST' && call.url.includes('/merge-requests'))
    expect(post?.body).toEqual({
      title: 'Add feature',
      description: '',
      source_branch: 'feature',
      target_branch: 'main',
    })
    await waitFor(() => window.location.pathname === '/acme/game/merges/7')
    act(() => root.unmount())
  })

  it('filters and paginates the list', async () => {
    const calls: string[] = []
    const page = (numbers: number[], next?: string) => ({
      merge_requests: numbers.map((number) => ({
        id: String(number),
        number,
        project_id: 'p1',
        source_branch: 'feature',
        target_branch: 'main',
        title: number === 2 ? 'Two' : 'One',
        description: '',
        status: 'open',
        created_by: '1',
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      })),
      next_cursor: next,
    })
    stubRepoRoutes((url) => {
      if (url.includes('/orgs/acme/members')) {
        return jsonResponse([
          { user_id: '1', name: 'Alice', email: 'a@example.com', photo_url: '', is_admin: true, is_super_admin: false, role: 'owner' },
        ])
      }
      if (url.includes('/merge-requests')) {
        calls.push(url)
        if (url.includes('after=2')) return jsonResponse(page([1]))
        if (url.includes('source=feature')) return jsonResponse(page([1]))
        return jsonResponse(page([2], '2'))
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/merges')
    await waitForText(container, '#2 Two')
    await act(async () => {
      findButton('Load more').click()
    })
    await waitForText(container, '#1 One')

    await act(async () => {
      setInputValue(container.querySelector('[aria-label="Filter by source branch"]') as HTMLInputElement, 'feature')
    })
    await waitFor(() => calls.some((url) => url.includes('source=feature')))
    await waitForText(container, '#1 One')
    expect(container.textContent).not.toContain('#2 Two')
    act(() => root.unmount())
  })
})

describe('MergeRequestPage', () => {
  const MR = {
    id: '1',
    number: 7,
    project_id: 'p1',
    source_branch: 'feature',
    target_branch: 'main',
    title: 'Change code',
    description: '',
    status: 'open',
    created_by: '1',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    mergeability: { status: 'mergeable' },
  }
  const FILES = [
    {
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
    },
  ]
  const THREAD = {
    id: 't1',
    merge_request_id: 7,
    file_path: 'src/main.ts',
    new_line: 1,
    side: 'right',
    outdated: false,
    resolved: false,
    created_by: { user_id: '1', name: 'Alice' },
    created_at: '2024-01-02T00:00:00Z',
    updated_at: '2024-01-02T00:00:00Z',
    comments: [
      {
        id: 'c1',
        thread_id: 't1',
        user: { user_id: '1', name: 'Alice' },
        body: 'please fix this',
        system: false,
        created_at: '2024-01-02T00:00:00Z',
        updated_at: '2024-01-02T00:00:00Z',
      },
    ],
  }

  it('shows a new inline comment in the diff and in the conversation', async () => {
    let created = false
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/merge-requests/7/diff') && method === 'GET') return jsonResponse({ files: FILES })
      if (url.includes('/merge-requests/7/commits') && method === 'GET') return jsonResponse([])
      if (url.includes('/merge-requests/7/review-state') && method === 'GET') {
        return jsonResponse({ approvals: 0, changes_requested: 0, dismissed_approvals: 0, outstanding_reviewers: [] })
      }
      if (url.includes('/merge-requests/7/threads') && method === 'POST') {
        created = true
        return jsonResponse(THREAD)
      }
      if (url.includes('/merge-requests/7/threads') && method === 'GET') {
        return jsonResponse(created ? [THREAD] : [])
      }
      if (url.includes('/merge-requests/7/') && method === 'GET') return jsonResponse([])
      if (url.endsWith('/merge-requests/7') && method === 'GET') return jsonResponse(MR)
      if (url.includes('/branches') && method === 'GET') return jsonResponse(BRANCHES)
      return null
    })

    const { container, root } = await renderApp('/acme/game/merges/7?tab=files')
    await waitFor(() => container.querySelector('[aria-label="Comment on right line 1"]') !== null)
    await act(async () => {
      ;(container.querySelector('[aria-label="Comment on right line 1"]') as HTMLElement).dispatchEvent(
        new MouseEvent('click', { bubbles: true }),
      )
    })
    await waitFor(() => container.querySelector('[aria-label="New inline comment"]') !== null)
    await act(async () => {
      setTextareaValue(
        container.querySelector('[aria-label="New inline comment"]') as HTMLTextAreaElement,
        'please fix this',
      )
    })
    await act(async () => {
      findButton('Comment').click()
    })
    await waitForText(container, 'please fix this')

    await act(async () => {
      findButton('Conversation').click()
    })
    await waitForText(container, 'View on file')
    expect(container.textContent).toContain('src/main.ts:1')
    expect(container.textContent).toContain('opened this merge request')
    expect(container.textContent).toContain('new line')
    act(() => root.unmount())
  })

  it('edits the title and description from the dialog', async () => {
    let patched: unknown = null
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/merge-requests/7') && method === 'PATCH') {
        patched = JSON.parse(String(init?.body ?? '{}'))
        return jsonResponse({ ...MR, title: 'New title', description: 'New body' })
      }
      if (url.includes('/merge-requests/7/diff') && method === 'GET') return jsonResponse({ files: [] })
      if (url.includes('/merge-requests/7/commits') && method === 'GET') return jsonResponse([])
      if (url.includes('/merge-requests/7/review-state') && method === 'GET') {
        return jsonResponse({ approvals: 0, changes_requested: 0, dismissed_approvals: 0, outstanding_reviewers: [] })
      }
      if (url.includes('/merge-requests/7/') && method === 'GET') return jsonResponse([])
      if (url.endsWith('/merge-requests/7') && method === 'GET') return jsonResponse(MR)
      if (url.includes('/branches') && method === 'GET') return jsonResponse(BRANCHES)
      return null
    })

    const { container, root } = await renderApp('/acme/game/merges/7')
    await waitForText(container, 'Change code')
    await act(async () => {
      findButton('Edit').click()
    })
    await waitFor(() => document.body.textContent?.includes('Edit merge request #7') ?? false)

    const titleInput = document.querySelector('input[value="Change code"]') as HTMLInputElement
    expect(titleInput).not.toBeNull()
    await act(async () => {
      setInputValue(titleInput, 'New title')
    })
    await act(async () => {
      findButton('Save changes').click()
    })
    await waitFor(() => patched !== null)
    expect(patched).toEqual({ title: 'New title', description: '' })
    act(() => root.unmount())
  })
})

describe('BranchesPage', () => {
  const TWO_BRANCHES = [
    { id: '1', name: 'main', is_default: true, is_protected: false, updated_at: new Date().toISOString() },
    { id: '2', name: 'feature', is_default: false, is_protected: false, updated_at: new Date().toISOString() },
  ]

  it('creates a branch from the dedicated page', async () => {
    const calls: { url: string; method: string; body: unknown }[] = []
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url.includes('/branches') && method === 'POST') {
        return jsonResponse({
          id: '3',
          name: 'feature',
          is_default: false,
          is_protected: false,
          updated_at: new Date().toISOString(),
        })
      }
      if (url.includes('/branches') && method === 'GET') return jsonResponse(TWO_BRANCHES)
      return null
    })

    const { container, root } = await renderApp('/acme/game/branches')
    await waitForText(container, 'feature')
    await act(async () => {
      findButton('Add branch').click()
    })
    await waitFor(() => window.location.pathname === '/acme/game/branches/new')

    const nameInput = container.querySelector('input') as HTMLInputElement
    await act(async () => {
      setInputValue(nameInput, 'feature')
    })
    await act(async () => {
      findButton('Create branch').click()
    })
    await waitFor(() => calls.some((call) => call.method === 'POST' && call.url.includes('/branches')))
    const post = calls.find((call) => call.method === 'POST' && call.url.includes('/branches'))
    expect(post?.body).toEqual({ name: 'feature', from: '' })
    await waitFor(() => window.location.pathname === '/acme/game/branches')
    act(() => root.unmount())
  })

  it('deletes a branch after confirmation', async () => {
    const calls: { url: string; method: string }[] = []
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method })
      if (url.includes('/branches/feature') && method === 'DELETE') {
        return jsonResponse({ message: 'branch deleted' })
      }
      if (url.includes('/branches') && method === 'GET') return jsonResponse(TWO_BRANCHES)
      return null
    })

    const { container, root } = await renderApp('/acme/game/branches')
    await waitForText(container, 'feature')
    await act(async () => {
      findButton('Delete').click()
    })
    await waitFor(() => (document.body.textContent?.includes('Delete branch feature') ?? false))
    await act(async () => {
      findButton('Delete branch').click()
    })
    await waitFor(() => calls.some((call) => call.method === 'DELETE'))
    expect(calls.find((call) => call.method === 'DELETE')?.url).toContain('/branches/feature')
    await waitFor(() => document.querySelector('[role="dialog"]') === null)
    act(() => root.unmount())
  })

  it('hides delete when only one branch exists', async () => {
    stubRepoRoutes((url, init) => {
      const method = init?.method ?? 'GET'
      if (url.includes('/branches') && method === 'GET') {
        return jsonResponse([
          { id: '1', name: 'trunk', is_default: false, is_protected: false, updated_at: new Date().toISOString() },
        ])
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/branches')
    await waitForText(container, 'trunk')
    const deleteButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent?.trim() === 'Delete',
    )
    expect(deleteButton).toBeUndefined()
    act(() => root.unmount())
  })
})

describe('CommitsPage', () => {
  it('filters the history by path', async () => {
    const urls: string[] = []
    stubRepoRoutes((url) => {
      if (url.includes('/commits?')) {
        urls.push(url)
        return jsonResponse([
          { id: 'c2', message: 'touch file', author_name: 'Bob', created_at: new Date().toISOString() },
        ])
      }
      return null
    })

    const { container, root } = await renderApp('/acme/game/commits?branch=main&path=src/main.ts')
    await waitForText(container, 'touch file')
    expect(urls.some((url) => url.includes('path=src%2Fmain.ts'))).toBe(true)
    expect(container.textContent).toContain('src/main.ts')
    act(() => root.unmount())
  })
})
