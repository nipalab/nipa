import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'
import { RepoPageShell } from './RepoPageShell'

const mounted: Array<ReturnType<typeof createRoot>> = []

async function render(actions?: React.ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  mounted.push(root)
  await act(async () => {
    root.render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <RepoPageShell org="acme" project="game" active="code" actions={actions}>
          <p>body</p>
        </RepoPageShell>
      </MemoryRouter>,
    )
  })
  return container
}

afterEach(async () => {
  await act(async () => {
    for (const root of mounted.splice(0)) root.unmount()
  })
  document.body.innerHTML = ''
})

describe('RepoPageShell', () => {
  it('links the organization and the project in the title', async () => {
    const container = await render()
    const heading = container.querySelector('h2') as HTMLElement
    expect(heading.textContent).toBe('acme / game')
    expect(heading.querySelector('a[href="/acme"]')?.textContent).toBe('acme')
    expect(heading.querySelector('a[href="/acme/game"]')?.textContent).toBe('game')
  })

  it('shows no access level and keeps page actions', async () => {
    const plain = await render()
    expect(plain.textContent).not.toContain('write access')
    expect(plain.textContent).not.toContain('read-only')

    const withActions = await render(<button type="button">Edit</button>)
    expect([...withActions.querySelectorAll('button')].some((button) => button.textContent === 'Edit')).toBe(true)
  })
})
