import type { ReactNode } from 'react'
import { Label } from '@primer/react'

export type StateVariant = 'default' | 'accent' | 'success' | 'attention' | 'danger' | 'done'

const VARIANTS: Record<string, StateVariant> = {
  open: 'accent',
  merged: 'done',
  mergeable: 'success',
  success: 'success',
  succeeded: 'success',
  delivered: 'success',
  active: 'success',
  closed: 'danger',
  invalid: 'danger',
  failed: 'danger',
  failure: 'danger',
  error: 'danger',
  behind_target: 'attention',
  pending: 'attention',
  queued: 'attention',
  running: 'attention',
  draft: 'default',
}

export function stateVariant(status: string): StateVariant {
  return VARIANTS[status.toLowerCase()] ?? 'default'
}

export function stateLabel(status: string): string {
  const text = status.replace(/_/g, ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

export function StateBadge({ status, children }: { status: string; children?: ReactNode }) {
  return <Label variant={stateVariant(status)}>{children ?? stateLabel(status)}</Label>
}
