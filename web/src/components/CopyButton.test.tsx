import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CopyButton } from './CopyButton'

function labelOf(button: HTMLElement): string {
  const ids = (button.getAttribute('aria-labelledby') ?? '').split(' ')
  return button.getAttribute('aria-label') ?? ids.map((id) => document.getElementById(id)?.textContent ?? '').join('')
}

async function render() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<CopyButton value="abc123" label="Copy commit ID" />)
  })
  return { container, root, button: () => container.querySelector('button') as HTMLElement }
}

afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('CopyButton', () => {
  it('copies the value and confirms it briefly', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { button, root } = await render()
    expect(labelOf(button())).toBe('Copy commit ID')
    await act(async () => {
      button().click()
    })
    expect(writeText).toHaveBeenCalledWith('abc123')
    expect(labelOf(button())).toBe('Copied')
    await act(async () => {
      vi.advanceTimersByTime(1600)
    })
    expect(labelOf(button())).toBe('Copy commit ID')
    act(() => root.unmount())
  })

  it('stays ready when the clipboard is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn(async () => Promise.reject(new Error('denied'))) },
      configurable: true,
    })
    const { button, root } = await render()
    await act(async () => {
      button().click()
    })
    expect(labelOf(button())).toBe('Copy commit ID')
    act(() => root.unmount())
  })
})
