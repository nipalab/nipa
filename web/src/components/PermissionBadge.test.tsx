import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { PermissionBadge, permissionLabel } from './PermissionBadge'
import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'

async function render(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(node)
  })
  return container
}

describe('permissionLabel', () => {
  it('maps masks to labels', () => {
    expect(permissionLabel(0)).toBe('none')
    expect(permissionLabel(PERMISSION_READ)).toBe('read')
    expect(permissionLabel(PERMISSION_WRITE)).toBe('write')
    expect(permissionLabel(PERMISSION_LOCK)).toBe('lock')
    expect(permissionLabel(PERMISSION_ADMIN)).toBe('admin')
    expect(permissionLabel(PERMISSION_WRITE | PERMISSION_LOCK)).toBe('write-lock')
    expect(permissionLabel(PERMISSION_READ | PERMISSION_ADMIN)).toBe('admin')
  })
})

describe('PermissionBadge', () => {
  it('renders a label for each permission', async () => {
    expect((await render(<PermissionBadge permission={PERMISSION_READ} />)).textContent).toContain('Read')
    expect((await render(<PermissionBadge permission={PERMISSION_WRITE} />)).textContent).toContain('Write')
    expect((await render(<PermissionBadge permission={PERMISSION_LOCK} />)).textContent).toContain('Lock')
    expect((await render(<PermissionBadge permission={PERMISSION_ADMIN} />)).textContent).toContain('Admin')
    expect(
      (await render(<PermissionBadge permission={PERMISSION_WRITE | PERMISSION_LOCK} />)).textContent,
    ).toContain('Write + Lock')
    expect((await render(<PermissionBadge permission={0} />)).textContent).toContain('None')
  })
})
