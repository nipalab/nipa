import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Route, Routes } from 'react-router-dom'
import * as endpoints from '../api/endpoints'
import type { OnboardingCapabilities, OwnerStorageResponse } from '../api/models'
import { click, render } from '../test/render'
import { UsageBanner } from './UsageBanner'

vi.mock('../api/endpoints', () => ({
  getMyBilling: vi.fn(),
  getOnboardingCapabilities: vi.fn(),
}))

const api = vi.mocked(endpoints)
const GIB = 1024 ** 3
const LOW: OwnerStorageResponse = { user_id: '7', used_bytes: 13 * GIB, quota_bytes: 15 * GIB, orgs: [] }
const FULL: OwnerStorageResponse = { ...LOW, used_bytes: 15 * GIB, seats_used: 5, seat_limit: 5 }
const BILLING = { billing: { provider: 'paddle' } } as unknown as OnboardingCapabilities

async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

async function show(route = '/dashboard', enabled = true) {
  const view = await render(
    <Routes>
      <Route path="*" element={<UsageBanner enabled={enabled} />} />
    </Routes>,
    route,
  )
  await settle()
  return view
}

describe('UsageBanner', () => {
  let unmount: (() => Promise<void>) | null = null

  beforeEach(() => {
    localStorage.clear()
    api.getMyBilling.mockReset()
    api.getOnboardingCapabilities.mockReset()
    api.getOnboardingCapabilities.mockResolvedValue(BILLING)
  })

  afterEach(async () => {
    await unmount?.()
    unmount = null
  })

  it('fetches nothing while signed out', async () => {
    unmount = (await show('/dashboard', false)).unmount
    expect(api.getMyBilling).not.toHaveBeenCalled()
    expect(document.body.textContent).toBe('')
  })

  it('stays hidden below the threshold and skips the capabilities call', async () => {
    api.getMyBilling.mockResolvedValue({ ...LOW, used_bytes: GIB })
    unmount = (await show()).unmount
    expect(document.body.textContent).toBe('')
    expect(api.getOnboardingCapabilities).not.toHaveBeenCalled()
  })

  it('links the billing owner to billing settings and remembers a dismissal', async () => {
    api.getMyBilling.mockResolvedValue(LOW)
    const view = await show()
    unmount = view.unmount
    expect(view.container.textContent).toContain('Storage is almost full')
    const link = view.container.querySelector('a[href="/settings/billing"]')
    expect(link?.textContent).toBe('Add storage in billing settings')

    await click(view.container.querySelector('button[class*="BannerDismiss"]'))
    expect(view.container.textContent).not.toContain('Storage is almost full')
    expect(JSON.parse(localStorage.getItem('nipa.usage.dismissed') ?? '[]')).toEqual([`storage_low:${15 * GIB}`])

    await view.unmount()
    const again = await show()
    unmount = again.unmount
    expect(again.container.textContent).toBe('')
  })

  it('shows full storage and full seats, without a link when billing is off', async () => {
    api.getMyBilling.mockResolvedValue(FULL)
    api.getOnboardingCapabilities.mockResolvedValue(null)
    const view = await show()
    unmount = view.unmount
    expect(view.container.textContent).toContain('Storage is full')
    expect(view.container.textContent).toContain('All seats are in use')
    expect(view.container.textContent).toContain('Contact your administrator to raise the limit.')
    expect(view.container.querySelector('a')).toBeNull()
  })

  it('offers seats when only seats are full', async () => {
    api.getMyBilling.mockResolvedValue({ ...FULL, used_bytes: 0 })
    const view = await show()
    unmount = view.unmount
    expect(view.container.querySelector('a')?.textContent).toBe('Add seats in billing settings')
  })

  it('is hidden on the billing page and ignores failures', async () => {
    api.getMyBilling.mockResolvedValue(FULL)
    unmount = (await show('/settings/billing')).unmount
    expect(document.body.textContent).toBe('')
    await unmount()
    api.getMyBilling.mockRejectedValue(new Error('boom'))
    unmount = (await show()).unmount
    expect(document.body.textContent).toBe('')
  })
})
