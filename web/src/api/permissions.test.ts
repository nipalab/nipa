import { describe, expect, it } from 'vitest'
import { formatPermission, parsePermission, PERMISSION_ADMIN, PERMISSION_READ, PERMISSION_WRITE } from './permissions'

describe('permissions', () => {
  it('parses comma separated permissions', () => {
    expect(parsePermission('read,write')).toBe(PERMISSION_READ | PERMISSION_WRITE)
    expect(parsePermission(' ADMIN ')).toBe(PERMISSION_ADMIN)
  })

  it('rejects empty and unknown permissions', () => {
    expect(() => parsePermission('')).toThrow()
    expect(() => parsePermission('execute')).toThrow()
  })

  it('formats masks', () => {
    expect(formatPermission(PERMISSION_READ | PERMISSION_WRITE)).toBe('read,write')
    expect(formatPermission(0)).toBe('none')
  })
})
