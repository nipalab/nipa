import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'

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

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>
}

export async function renderRoute(pattern: string, url: string, element: ReactNode) {
  const view = await render(
    <Routes>
      <Route path={pattern} element={element} />
      <Route path="*" element={<LocationProbe />} />
    </Routes>,
    url,
  )
  return { ...view, location: () => view.container.querySelector('[data-testid="location"]')?.textContent ?? null }
}

export async function waitFor(predicate: () => boolean, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  throw new Error('timed out waiting for the UI to settle')
}

export function buttonsByText(root: ParentNode, text: string): HTMLButtonElement[] {
  return Array.from(root.querySelectorAll('button')).filter((button) => button.textContent?.trim() === text)
}

export async function confirmDialog(label: string) {
  await waitFor(() => document.querySelector('[role="alertdialog"]') !== null)
  const dialog = document.querySelector('[role="alertdialog"]') as HTMLElement
  await click(buttonByText(dialog, label))
  await flush()
}

export async function cancelDialog() {
  await waitFor(() => document.querySelector('[role="alertdialog"]') !== null)
  const dialog = document.querySelector('[role="alertdialog"]') as HTMLElement
  await click(buttonByText(dialog, 'Cancel'))
  await flush()
}

export async function choose(select: HTMLSelectElement | null | undefined, value: string) {
  if (!select) throw new Error('select not found')
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
    setter?.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

export async function typeInto(root: ParentNode, selector: string, value: string) {
  await type(root.querySelector<HTMLInputElement>(selector), value)
}
