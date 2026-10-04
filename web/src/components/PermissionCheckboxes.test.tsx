import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { PermissionCheckboxes } from './PermissionCheckboxes'
import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'

async function renderCheckboxes(value: number, onChange: (mask: number) => void) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<PermissionCheckboxes value={value} onChange={onChange} />)
  })
  return container
}

function checkboxes(container: HTMLElement): HTMLInputElement[] {
  return Array.from(container.querySelectorAll('input[type="checkbox"]'))
}

function toggle(el: HTMLInputElement) {
  act(() => {
    el.click()
  })
}

describe('PermissionCheckboxes', () => {
  it('checks the boxes for the current mask', async () => {
    const container = await renderCheckboxes(PERMISSION_READ | PERMISSION_LOCK, () => undefined)
    const boxes = checkboxes(container)
    expect(boxes).toHaveLength(4)
    expect(boxes[0].checked).toBe(true)
    expect(boxes[1].checked).toBe(false)
    expect(boxes[2].checked).toBe(true)
    expect(boxes[3].checked).toBe(false)
  })

  it('adds a permission bit when unchecked box is clicked', async () => {
    const changes: number[] = []
    const container = await renderCheckboxes(PERMISSION_READ, (mask) => changes.push(mask))
    toggle(checkboxes(container)[1])
    expect(changes).toEqual([PERMISSION_READ | PERMISSION_WRITE])
  })

  it('removes a permission bit when checked box is clicked', async () => {
    const changes: number[] = []
    const container = await renderCheckboxes(PERMISSION_READ | PERMISSION_ADMIN, (mask) => changes.push(mask))
    toggle(checkboxes(container)[3])
    expect(changes).toEqual([PERMISSION_READ])
  })
})
