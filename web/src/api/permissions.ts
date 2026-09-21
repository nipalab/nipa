import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE, formatPermission } from './models'

export { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE, formatPermission }

export function parsePermission(raw: string): number {
  let mask = 0
  for (const part of raw.split(',')) {
    switch (part.trim().toLowerCase()) {
      case 'read':
        mask |= PERMISSION_READ
        break
      case 'write':
        mask |= PERMISSION_WRITE
        break
      case 'lock':
        mask |= PERMISSION_LOCK
        break
      case 'admin':
        mask |= PERMISSION_ADMIN
        break
      case '':
        break
      default:
        throw new Error(`unknown permission ${part.trim()}`)
    }
  }
  if (mask === 0) {
    throw new Error('at least one permission is required')
  }
  return mask
}
