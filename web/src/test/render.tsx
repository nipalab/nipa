import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'

export async function render(node: ReactNode, route = '/') {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        {node}
      </MemoryRouter>,
    )
  })
  return {
    container,
    async unmount() {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

export async function renderHook<T>(hook: () => T) {
  const result = { current: undefined as T }
  function Probe() {
    result.current = hook()
    return null
  }
  const view = await render(<Probe />)
  return { result, unmount: view.unmount }
}

export async function click(element: Element | null | undefined) {
  if (!element) throw new Error('click target not found')
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
  })
}

export async function type(input: HTMLInputElement | null | undefined, value: string) {
  if (!input) throw new Error('input not found')
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

export function buttonByText(root: ParentNode, text: string): HTMLButtonElement | null {
  return (
    Array.from(root.querySelectorAll('button')).find((button) => button.textContent?.trim() === text) ?? null
  )
}

export async function flush() {
  await act(async () => {
    await Promise.resolve()
  })
}
