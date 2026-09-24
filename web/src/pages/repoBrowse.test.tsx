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

function stubRepoRoutes(extra: (url: string) => Response | null = () => null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const custom = extra(url)
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
  it('keeps the repository tabs on commits, branches, pulls and settings', async () => {
    stubRepoRoutes((url) => {
      if (url.includes('/commits?')) return jsonResponse([])
      if (url.includes('/merge-requests')) return jsonResponse([])
      if (url.includes('/permissions/rules')) return jsonResponse([])
      if (url.includes('/permissions/defaults')) return jsonResponse([])
      return null
    })

    const tabs = [
      ['/acme/game/commits', 'Commits'],
      ['/acme/game/branches', 'Branches'],
      ['/acme/game/pulls', 'Merge requests'],
      ['/acme/game/settings', 'Settings'],
    ] as const

    for (const [url, active] of tabs) {
      const { container, root } = await renderApp(url)
      await waitFor(() => container.querySelector('[aria-current="page"]')?.textContent === active)
      expect(container.querySelector('a[href="/acme/game/tree/main"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/commits?branch=main"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/branches"]')).not.toBeNull()
      expect(container.querySelector('a[href="/acme/game/pulls"]')).not.toBeNull()
      act(() => root.unmount())
    }
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
