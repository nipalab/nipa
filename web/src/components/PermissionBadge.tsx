import { Label } from '@primer/react'
import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'

export type PermissionLabelVariant = 'none' | 'read' | 'lock' | 'write-lock' | 'write' | 'admin'

export function permissionLabel(permission: number): PermissionLabelVariant {
  if (permission === 0) return 'none'
  if ((permission & PERMISSION_ADMIN) !== 0) return 'admin'
  if ((permission & PERMISSION_WRITE) !== 0 && (permission & PERMISSION_LOCK) !== 0) return 'write-lock'
  if ((permission & PERMISSION_WRITE) !== 0) return 'write'
  if ((permission & PERMISSION_LOCK) !== 0) return 'lock'
  if ((permission & PERMISSION_READ) !== 0) return 'read'
  return 'none'
}

export function PermissionBadge({ permission }: { permission: number }) {
  switch (permissionLabel(permission)) {
    case 'admin':
      return <Label variant="danger">Admin</Label>
    case 'write-lock':
      return <Label variant="success">Write + Lock</Label>
    case 'write':
      return <Label variant="success">Write</Label>
    case 'lock':
      return <Label variant="attention">Lock</Label>
    case 'read':
      return <Label variant="accent">Read</Label>
    default:
      return <Label variant="done">None</Label>
  }
}
