import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { errorMessage, useAction, useAsync, useClipboard } from '../hooks'
import { clearOrgMembersCache, fetchOrgMembers, useOrgMembers, useOrgRole } from '../orgHooks'
import { useResendVerification } from '../useResendVerification'
import { flush, renderHook } from './render'

const endpoints = vi.hoisted(() => ({
  listOrgs: vi.fn(),
  listOrgMembers: vi.fn(),
  resendVerification: vi.fn(),
}))
const auth = vi.hoisted(() => ({ me: { is_admin: false, is_super_admin: false } as Record<string, boolean> | null }))

vi.mock('../api/endpoints', () => endpoints)
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me }) }))

beforeEach(() => {
  auth.me = { is_admin: false, is_super_admin: false }
  clearOrgMembersCache()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
  Object.values(endpoints).forEach((fn) => fn.mockReset())
})

describe('errorMessage', () => {
  it('reads Error messages and stringifies everything else', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom')
    expect(errorMessage('plain')).toBe('plain')
    expect(errorMessage(42)).toBe('42')
  })
})

describe('useAsync', () => {
  it('exposes data, then error text, then reloads', async () => {
    const fn = vi.fn().mockResolvedValueOnce('first').mockRejectedValueOnce(new Error('later failure'))
    const { result } = await renderHook(() => useAsync(fn, []))
    await flush()
    expect(result.current.data).toBe('first')
    expect(result.current.loading).toBe(false)
    await act(async () => result.current.reload())
    await flush()
    expect(result.current.error).toBe('later failure')
  })
})

describe('useAction', () => {
  it('runs the action, reports success and calls onSuccess', async () => {
    const onSuccess = vi.fn()
    const { result } = await renderHook(() => useAction(onSuccess))
    let value: string | undefined
    await act(async () => {
      value = await result.current.run(async () => 'done')
    })
    expect(value).toBe('done')
    expect(onSuccess).toHaveBeenCalledTimes(1)
    expect(result.current.busy).toBe(false)
    expect(result.current.error).toBeNull()
  })

  it('captures failures without calling onSuccess and can clear the error', async () => {
    const onSuccess = vi.fn()
    const { result } = await renderHook(() => useAction(onSuccess))
    let value: string | undefined = 'unset'
    await act(async () => {
      value = await result.current.run(async () => {
        throw new Error('nope')
      })
    })
    expect(value).toBeUndefined()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(result.current.error).toBe('nope')
    await act(async () => result.current.clearError())
    expect(result.current.error).toBeNull()
  })

  it('reports busy while the action is pending', async () => {
    const { result } = await renderHook(() => useAction())
    let release: () => void = () => undefined
    let pending: Promise<unknown> = Promise.resolve()
    await act(async () => {
      pending = result.current.run(() => new Promise<void>((resolve) => (release = resolve)))
    })
    expect(result.current.busy).toBe(true)
    await act(async () => {
      release()
      await pending
    })
    expect(result.current.busy).toBe(false)
  })
})

describe('useClipboard', () => {
  it('flags copied then resets after the timeout', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { result } = await renderHook(() => useClipboard(500))
    await act(async () => {
      expect(await result.current.copy('abc')).toBe(true)
    })
    expect(writeText).toHaveBeenCalledWith('abc')
    expect(result.current.copied).toBe(true)
    await act(async () => {
      vi.advanceTimersByTime(600)
    })
    expect(result.current.copied).toBe(false)
  })

  it('returns false when the clipboard is unavailable', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { result } = await renderHook(() => useClipboard())
    await act(async () => {
      expect(await result.current.copy('abc')).toBe(false)
    })
    expect(result.current.copied).toBe(false)
  })
})

describe('useOrgRole', () => {
  it('lets org owners manage', async () => {
    endpoints.listOrgs.mockResolvedValue([{ slug: 'acme', role: 'owner' }])
    const { result } = await renderHook(() => useOrgRole('acme'))
    await flush()
    expect(result.current.loading).toBe(false)
    expect(result.current.role).toBe('owner')
    expect(result.current.canManage).toBe(true)
  })

  it('denies plain members and unknown orgs', async () => {
    endpoints.listOrgs.mockResolvedValue([{ slug: 'acme', role: 'member' }])
    const member = await renderHook(() => useOrgRole('acme'))
    await flush()
    expect(member.result.current.canManage).toBe(false)
    const unknown = await renderHook(() => useOrgRole('other'))
    await flush()
    expect(unknown.result.current.role).toBeUndefined()
    expect(unknown.result.current.canManage).toBe(false)
  })

  it('lets admins manage regardless of role', async () => {
    auth.me = { is_admin: true }
    endpoints.listOrgs.mockResolvedValue([{ slug: 'acme', role: 'member' }])
    const { result } = await renderHook(() => useOrgRole('acme'))
    await flush()
    expect(result.current.canManage).toBe(true)
  })

  it('reports load errors', async () => {
    endpoints.listOrgs.mockRejectedValue(new Error('offline'))
    const { result } = await renderHook(() => useOrgRole('acme'))
    await flush()
    expect(result.current.error).toBe('offline')
  })
})

describe('org members cache', () => {
  it('shares one request per org until forced', async () => {
    endpoints.listOrgMembers.mockResolvedValue([{ user_id: '1' }])
    await Promise.all([fetchOrgMembers('acme'), fetchOrgMembers('acme')])
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(1)
    await fetchOrgMembers('acme', true)
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(2)
    await fetchOrgMembers('beta')
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(3)
  })

  it('drops failed requests so the next call retries', async () => {
    endpoints.listOrgMembers.mockRejectedValueOnce(new Error('down')).mockResolvedValueOnce([])
    await expect(fetchOrgMembers('acme')).rejects.toThrow('down')
    await flush()
    await expect(fetchOrgMembers('acme')).resolves.toEqual([])
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(2)
  })

  it('refetches after the cache is cleared', async () => {
    endpoints.listOrgMembers.mockResolvedValue([])
    await fetchOrgMembers('acme')
    clearOrgMembersCache()
    await fetchOrgMembers('acme')
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(2)
  })

  it('expires cached entries', async () => {
    vi.useFakeTimers()
    endpoints.listOrgMembers.mockResolvedValue([])
    await fetchOrgMembers('acme')
    vi.advanceTimersByTime(31_000)
    await fetchOrgMembers('acme')
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(2)
  })

  it('useOrgMembers reloads past the cache', async () => {
    endpoints.listOrgMembers.mockResolvedValue([{ user_id: '1' }])
    const { result } = await renderHook(() => useOrgMembers('acme'))
    await flush()
    expect(result.current.data).toEqual([{ user_id: '1' }])
    await act(async () => result.current.reload())
    await flush()
    expect(endpoints.listOrgMembers).toHaveBeenCalledTimes(2)
  })
})

describe('useResendVerification', () => {
  it('ignores a blank address', async () => {
    const { result } = await renderHook(() => useResendVerification())
    await act(async () => {
      expect(await result.current.resend('   ')).toBe(false)
    })
    expect(endpoints.resendVerification).not.toHaveBeenCalled()
  })

  it('sends the trimmed address and sets the notice', async () => {
    endpoints.resendVerification.mockResolvedValue(undefined)
    const { result } = await renderHook(() => useResendVerification())
    await act(async () => {
      expect(await result.current.resend(' a@b.c ')).toBe(true)
    })
    expect(endpoints.resendVerification).toHaveBeenCalledWith('a@b.c')
    expect(result.current.notice).toContain('new email is on its way')
    expect(result.current.error).toBeNull()
  })

  it('surfaces failures', async () => {
    endpoints.resendVerification.mockRejectedValue(new Error('rate limited'))
    const { result } = await renderHook(() => useResendVerification())
    await act(async () => {
      expect(await result.current.resend('a@b.c')).toBe(false)
    })
    expect(result.current.error).toBe('rate limited')
    expect(result.current.notice).toBeNull()
  })
})
