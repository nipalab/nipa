import type { OwnerStorageResponse } from '../api/models'
import { formatBytes } from '../components/repo/format'

export const STORAGE_WARN_RATIO = 0.8

export type UsageAlertKind = 'storage_full' | 'storage_low' | 'seats_full'

export interface UsageAlert {
  kind: UsageAlertKind
  variant: 'warning' | 'critical'
  title: string
  text: string
  key: string
  dismissible: boolean
}

export function usageAlerts(data: OwnerStorageResponse | null | undefined): UsageAlert[] {
  if (!data) return []
  const alerts: UsageAlert[] = []
  const quota = data.quota_bytes
  if (quota !== null && quota > 0) {
    const usage = `${formatBytes(data.used_bytes)} of ${formatBytes(quota)} used`
    if (data.used_bytes >= quota) {
      alerts.push({
        kind: 'storage_full',
        variant: 'critical',
        title: 'Storage is full',
        text: `${usage}. New pushes are refused until you add storage or free space.`,
        key: `${data.user_id}:storage_full:${quota}`,
        dismissible: false,
      })
    } else if (data.used_bytes >= quota * STORAGE_WARN_RATIO) {
      alerts.push({
        kind: 'storage_low',
        variant: 'warning',
        title: 'Storage is almost full',
        text: `${usage}. Pushes will be refused once it is full.`,
        key: `${data.user_id}:storage_low:${quota}`,
        dismissible: true,
      })
    }
  }
  const limit = data.seat_limit ?? 0
  const used = data.seats_used ?? 0
  if (limit > 0 && used >= limit) {
    alerts.push({
      kind: 'seats_full',
      variant: 'warning',
      title: 'All seats are in use',
      text: `${used} of ${limit} seats in use. Adding another person to your organizations needs another seat.`,
      key: `${data.user_id}:seats_full:${limit}`,
      dismissible: true,
    })
  }
  return alerts
}
