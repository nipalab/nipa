import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from '../auth'
import { memberCache } from '../orgMembersCache'
import { click, flush, render } from './render'

const client = vi.hoisted(() => ({
  bootstrap: vi.fn(),
  clearSession: vi.fn(),
  logout: vi.fn(),
  subscribeSession: vi.fn(() => () => {}),
}))
const endpoints = vi.hoisted(() => ({ getMe: vi.fn() }))

vi.mock('../api/client', () => client)
vi.mock('../api/endpoints', () => endpoints)

afterEach(() => {
  vi.restoreAllMocks()
  memberCache.clear()
  Object.values(client).forEach((fn) => fn.mockReset())
  endpoints.getMe.mockReset()
})

function SignOut() {
  const { signOut } = useAuth()
  return <button onClick={signOut}>out</button>
}

describe('AuthProvider', () => {
  it('drops cached org members on sign-out', async () => {
    client.bootstrap.mockResolvedValue(true)
    client.logout.mockResolvedValue(undefined)
    client.subscribeSession.mockReturnValue(() => {})
    endpoints.getMe.mockResolvedValue({ id: '1' })
    memberCache.set('acme', { at: Date.now(), request: Promise.resolve([]) })

    const { container } = await render(
      <AuthProvider>
        <SignOut />
      </AuthProvider>,
    )
    await flush()
    expect(memberCache.size).toBe(1)
    await click(container.querySelector('button'))
    await flush()
    expect(memberCache.size).toBe(0)
  })
})
