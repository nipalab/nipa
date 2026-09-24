import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BookIcon, FileBinaryIcon, FileCodeIcon, FileDirectoryFillIcon, FileIcon, FileMediaIcon } from '@primer/octicons-react'
import { BranchSelector } from './BranchSelector'
import { RepoBreadcrumb } from './RepoBreadcrumb'
import { RepoNav } from './RepoNav'
import { fileIcon } from './fileIcon'
import { commitTitle, formatBytes, relativeTime, shortSha } from './format'
import { blobUrl, commitsUrl, parentPath, repoUrl, treeUrl } from './repoPaths'
import type { BranchResponse } from '../../api/models'

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>{node}</MemoryRouter>,
    )
  })
  return { container, root }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('repoPaths', () => {
  it('builds tree and blob URLs with encoded revisions and paths', () => {
    expect(repoUrl('acme', 'game')).toBe('/acme/game')
    expect(treeUrl('acme', 'game', 'main', 'src/models')).toBe('/acme/game/tree/main/src/models')
    expect(treeUrl('acme', 'game', 'feature/x', 'a b/c')).toBe('/acme/game/tree/feature%2Fx/a%20b/c')
    expect(treeUrl('acme', 'game', '')).toBe('/acme/game/tree')
    expect(treeUrl('acme', 'game', '', 'src')).toBe('/acme/game/tree?path=src')
    expect(blobUrl('acme', 'game', 'main', 'src/main.ts')).toBe('/acme/game/blob/main/src/main.ts')
    expect(blobUrl('acme', 'game', '', 'src/main.ts')).toBe('/acme/game/blob?path=src%2Fmain.ts')
  })

  it('builds commit URLs with optional path filter', () => {
    expect(commitsUrl('acme', 'game')).toBe('/acme/game/commits')
    expect(commitsUrl('acme', 'game', 'main')).toBe('/acme/game/commits?branch=main')
    expect(commitsUrl('acme', 'game', 'main', 'src/a.ts')).toBe('/acme/game/commits?branch=main&path=src%2Fa.ts')
  })

  it('returns the parent path', () => {
    expect(parentPath('a/b/c')).toBe('a/b')
    expect(parentPath('top.txt')).toBe('')
  })
})

describe('format', () => {
  it('formats byte sizes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1023)).toBe('1023 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(10 * 1024 * 1024)).toBe('10 MB')
    expect(formatBytes(undefined)).toBe('')
  })

  it('formats relative time', () => {
    const threeDaysAgo = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString()
    expect(relativeTime(threeDaysAgo)).toMatch(/3 days ago/)
    expect(relativeTime('not-a-date')).toBe('')
    expect(relativeTime(undefined)).toBe('')
  })

  it('normalizes commit messages and hashes', () => {
    expect(commitTitle('fix: thing\n\nbody text')).toBe('fix: thing')
    expect(commitTitle('   ')).toBe('Untitled commit')
    expect(shortSha('abcdef123456')).toBe('abcdef1')
    expect(shortSha(undefined)).toBe('')
  })
})

describe('fileIcon', () => {
  it('picks icons by entry type and extension', () => {
    expect(fileIcon('src', 'tree').Icon).toBe(FileDirectoryFillIcon)
    expect(fileIcon('README.md', 'file').Icon).toBe(BookIcon)
    expect(fileIcon('main.go', 'file').Icon).toBe(FileCodeIcon)
    expect(fileIcon('photo.png', 'file').Icon).toBe(FileMediaIcon)
    expect(fileIcon('blob.bin', 'file').Icon).toBe(FileBinaryIcon)
    expect(fileIcon('LICENSE', 'file').Icon).toBe(FileIcon)
  })
})

describe('RepoNav', () => {
  it('renders the tabs and marks the active one', async () => {
    const { container, root } = await render(
      <RepoNav org="acme" project="game" active="code" rev="main" canAdmin />,
    )
    expect(container.textContent).toContain('Code')
    expect(container.textContent).toContain('Commits')
    expect(container.textContent).toContain('Branches')
    expect(container.textContent).toContain('Merge requests')
    expect(container.textContent).toContain('Settings')
    const current = container.querySelector('[aria-current="page"]')
    expect(current?.textContent).toContain('Code')
    expect(container.querySelector('a[href="/acme/game/tree/main"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/branches"]')).not.toBeNull()
    act(() => root.unmount())
  })

  it('hides settings for non-admins', async () => {
    const { container, root } = await render(<RepoNav org="acme" project="game" active="commits" />)
    expect(container.textContent).not.toContain('Settings')
    act(() => root.unmount())
  })
})

describe('RepoBreadcrumb', () => {
  it('links every directory and leaves the file leaf plain', async () => {
    const { container, root } = await render(
      <RepoBreadcrumb org="acme" project="game" rev="main" path="src/lib/a.ts" leafIsFile />,
    )
    expect(container.querySelector('a[href="/acme"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/tree/main"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/tree/main/src"]')).not.toBeNull()
    expect(container.querySelector('a[href="/acme/game/tree/main/src/lib"]')).not.toBeNull()
    expect(container.textContent).toContain('a.ts')
    expect(container.querySelector('a[href$="a.ts"]')).toBeNull()
    act(() => root.unmount())
  })
})

describe('BranchSelector', () => {
  it('shows the default branch until a revision is selected', async () => {
    const branches: BranchResponse[] = [
      { id: '1', name: 'main', is_default: true, is_protected: false, updated_at: '' },
      { id: '2', name: 'feature/login', is_default: false, is_protected: false, updated_at: '' },
    ]
    const { container, root } = await render(
      <BranchSelector branches={branches} rev="" onSelect={() => {}} />,
    )
    expect(container.textContent).toContain('main')
    act(() => root.unmount())
  })
})
