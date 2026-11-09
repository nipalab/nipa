import { formatBytes } from '../components/repo/format'

export interface EntitlementSizes {
  free_seats: number
  free_quota_bytes: number
  seat_included_bytes: number
  storage_pack_bytes: number
}

export function hasEntitlementSizes(config: Partial<EntitlementSizes>): config is EntitlementSizes {
  return (
    typeof config.free_seats === 'number' &&
    typeof config.free_quota_bytes === 'number' &&
    typeof config.seat_included_bytes === 'number' &&
    typeof config.storage_pack_bytes === 'number'
  )
}

export function planQuotaBytes(sizes: EntitlementSizes, seats: number, packs: number): number {
  if (sizes.free_quota_bytes <= 0) return 0
  return sizes.free_quota_bytes + seats * sizes.seat_included_bytes + packs * sizes.storage_pack_bytes
}

export function planStorageLabel(sizes: EntitlementSizes, seats: number, packs: number): string {
  const quota = planQuotaBytes(sizes, seats, packs)
  return quota === 0 ? 'Unlimited' : formatBytes(quota)
}
