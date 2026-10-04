import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { UserGroupPicker } from './UserGroupPicker'
import { listOrgMembers, listUsers } from '../api/endpoints'
import type { UserResponse } from '../api/models'

vi.mock('../api/endpoints', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/endpoints')>()
  return {
    ...actual,
    listUsers: vi.fn(async () => [
      {
        id: '1',
        name: 'Alice',
        email: 'alice@example.com',
        photo_url: '',
        is_admin: false,
        is_super_admin: false,
        deleted: false,
      },
      {
        id: '2',
        name: 'Bob',
        email: 'bob@example.com',
        photo_url: '',
        is_admin: false,
        is_super_admin: false,
        deleted: false,
      },
    ]),
    listOrgMembers: vi.fn(async () => []),
    listGroups: vi.fn(async () => [
      { id: '9', org_id: '1', name: 'Artists', description: 'Texture artists' },
    ]),
  }
})

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(node)
  })
  return container
}

function type(el: Element | null | undefined, value: string) {
  act(() => {
    const target = el as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(target, value)
    target.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function waitFor(predicate: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for the UI to settle')
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('UserGroupPicker', () => {
  it('shows matching users and groups and selects one', async () => {
    const selected: { id: string; isGroup: boolean }[] = []
    const container = await render(
      <UserGroupPicker
        org="acme"
        kind="user-or-group"
        onSelect={(entry) => selected.push({ id: entry.id, isGroup: entry.isGroup })}
      />,
    )

    type(container.querySelector('input'), 'al')
    await waitFor(() => (container.textContent ?? '').includes('Alice'))

    const alice = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Alice'),
    )
    act(() => {
      alice?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(selected).toEqual([{ id: '1', isGroup: false }])
  })

  it('filters by group name when kind is group', async () => {
    const container = await render(<UserGroupPicker org="acme" kind="group" onSelect={() => undefined} />)

    type(container.querySelector('input'), 'art')
    await waitFor(() => (container.textContent ?? '').includes('Artists'))

    expect(container.textContent).toContain('Texture artists')
    expect(container.textContent).not.toContain('Alice')
  })

  it('reports when nothing matches', async () => {
    const container = await render(<UserGroupPicker org="acme" kind="user" onSelect={() => undefined} />)

    type(container.querySelector('input'), 'zzz')
    await waitFor(() => (container.textContent ?? '').includes('No matches found.'))
  })

  it('falls back to manual ID entry when search is unavailable', async () => {
    vi.mocked(listUsers).mockRejectedValue(new Error('forbidden'))
    vi.mocked(listOrgMembers).mockRejectedValue(new Error('forbidden'))
    const selected: { id: string; isGroup: boolean }[] = []
    const container = await render(
      <UserGroupPicker
        org="acme"
        kind="user"
        onSelect={(entry) => selected.push({ id: entry.id, isGroup: entry.isGroup })}
      />,
    )

    type(container.querySelector('input'), 'alice')
    await waitFor(() => (container.textContent ?? '').includes('Search is unavailable with your permissions.'))

    const manualLink = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Enter an ID manually'),
    )
    act(() => {
      manualLink?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await waitFor(() => container.querySelector('input[aria-label="Subject id"]') !== null)

    type(container.querySelector('input[aria-label="Subject id"]'), 'abc123')
    const useId = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Use ID'),
    )
    act(() => {
      useId?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(selected).toEqual([{ id: 'abc123', isGroup: false }])
  })

  it('ignores out-of-order responses from stale queries', async () => {
    const users: UserResponse[] = [
      {
        id: '1',
        name: 'Alice',
        email: 'alice@example.com',
        photo_url: '',
        is_admin: false,
        is_super_admin: false,
        deleted: false,
      },
      {
        id: '2',
        name: 'Abigail',
        email: 'abigail@example.com',
        photo_url: '',
        is_admin: false,
        is_super_admin: false,
        deleted: false,
      },
    ]
    let releaseFirst: (value: UserResponse[]) => void = () => undefined
    let calls = 0
    vi.mocked(listUsers).mockImplementation(async () => {
      calls += 1
      if (calls === 1) {
        return new Promise<UserResponse[]>((resolve) => {
          releaseFirst = resolve
        })
      }
      return users
    })

    const container = await render(<UserGroupPicker org="acme" kind="user" onSelect={() => undefined} />)

    type(container.querySelector('input'), 'al')
    await waitFor(() => calls === 1)

    type(container.querySelector('input'), 'ab')
    await waitFor(() => (container.textContent ?? '').includes('Abigail'))

    await act(async () => {
      releaseFirst(users)
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Abigail')
    expect(container.textContent).not.toContain('Alice')
  })

  it('allows manual group IDs', async () => {
    const selected: { id: string; isGroup: boolean }[] = []
    const container = await render(
      <UserGroupPicker
        org="acme"
        kind="group"
        onSelect={(entry) => selected.push({ id: entry.id, isGroup: entry.isGroup })}
      />,
    )

    const manualLink = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Enter an ID manually'),
    )
    act(() => {
      manualLink?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await waitFor(() => container.querySelector('input[aria-label="Subject id"]') !== null)

    type(container.querySelector('input[aria-label="Subject id"]'), 'grp42')
    const useId = Array.from(container.querySelectorAll('button')).find((button) =>
      button.textContent?.includes('Use ID'),
    )
    act(() => {
      useId?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(selected).toEqual([{ id: 'grp42', isGroup: true }])
  })
})
