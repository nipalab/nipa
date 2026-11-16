import { describe, expect, it } from 'vitest'
import type { OwnerStorageResponse } from '../api/models'
import { usageAlerts } from './usage'

const GIB = 1024 ** 3

function owner(extra: Partial<OwnerStorageResponse>): OwnerStorageResponse {
  return { user_id: '7', used_bytes: 0, quota_bytes: 15 * GIB, orgs: [], ...extra }
}

describe('usageAlerts', () => {
  it('returns nothing without data, without a quota or below the threshold', () => {
    expect(usageAlerts(null)).toEqual([])
    expect(usageAlerts(undefined)).toEqual([])
    expect(usageAlerts(owner({ used_bytes: 100 * GIB, quota_bytes: null }))).toEqual([])
    expect(usageAlerts(owner({ used_bytes: 11 * GIB }))).toEqual([])
  })

  it('warns from 80% and is dismissible', () => {
    const [alert] = usageAlerts(owner({ used_bytes: 12 * GIB }))
    expect(alert).toMatchObject({ kind: 'storage_low', variant: 'warning', dismissible: true, key: `storage_low:${15 * GIB}` })
    expect(alert.text).toContain('12 GiB of 15 GiB used')
  })

  it('reports a full quota as critical and not dismissible', () => {
    const alerts = usageAlerts(owner({ used_bytes: 15 * GIB }))
    expect(alerts).toHaveLength(1)
    expect(alerts[0]).toMatchObject({ kind: 'storage_full', variant: 'critical', dismissible: false })
    expect(alerts[0].text).toContain('New pushes are refused')
  })

  it('reports full seats only when a seat limit is known', () => {
    expect(usageAlerts(owner({ seats_used: 4, seat_limit: 5 }))).toEqual([])
    expect(usageAlerts(owner({ seats_used: 5 }))).toEqual([])
    const alerts = usageAlerts(owner({ used_bytes: 15 * GIB, seats_used: 6, seat_limit: 5 }))
    expect(alerts.map((alert) => alert.kind)).toEqual(['storage_full', 'seats_full'])
    expect(alerts[1].text).toContain('6 of 5 seats in use')
  })
})
