import { describe, expect, it } from 'vitest'
import { hasEntitlementSizes, planQuotaBytes, planStorageLabel } from './entitlement'

const GIB = 1024 ** 3
const SIZES = { free_seats: 3, free_quota_bytes: 50 * GIB, seat_included_bytes: 50 * GIB, storage_pack_bytes: 100 * GIB }

describe('entitlement', () => {
  it('adds the free quota, seat storage and packs', () => {
    expect(planQuotaBytes(SIZES, 2, 1)).toBe(250 * GIB)
    expect(planStorageLabel(SIZES, 2, 1)).toBe('250 GiB')
    expect(planStorageLabel(SIZES, 0, 0)).toBe('50 GiB')
  })

  it('is unlimited without a free quota', () => {
    const unlimited = { ...SIZES, free_quota_bytes: 0 }
    expect(planQuotaBytes(unlimited, 5, 2)).toBe(0)
    expect(planStorageLabel(unlimited, 5, 2)).toBe('Unlimited')
  })

  it('detects servers that do not send the sizes', () => {
    expect(hasEntitlementSizes(SIZES)).toBe(true)
    expect(hasEntitlementSizes({ free_seats: 3 })).toBe(false)
  })
})
