import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type MaintenanceInfo } from '../api/client'
import type { MaintenanceWindow } from '../api/models'
import { getMaintenanceState, noteSessionPaused, resetMaintenance, storeOrg, storeProject, storeServer } from '../maintenance'
import { MaintenanceGate } from '../pages/MaintenancePage'
import { act, type ReactNode } from 'react'
import {
  buttonByText,
  choose,
  click,
  confirmDialog,
  render as baseRender,
  renderRoute as baseRenderRoute,
  type,
  waitFor,
} from '../test/render'
import { FormDialog } from './FormDialog'
import { MaintenanceBanner, MaintenanceErrorBanner, routeTarget } from './MaintenanceBanner'
import { fromLocalInput, MaintenanceForm, toLocalInput } from './MaintenanceForm'
import { TermsGate } from './TermsGate'

const auth = vi.hoisted(() => ({ me: null as null | { id: string; is_super_admin: boolean } }))
vi.mock('../auth', () => ({ useAuth: () => ({ me: auth.me }) }))

function jsonResponse(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, text: async () => JSON.stringify(body) } as Response
}

const text = () => document.body.textContent ?? ''

const mounted: { unmount: () => Promise<void> }[] = []

async function render(node: ReactNode, route?: string) {
  const view = await baseRender(node, route)
  mounted.push(view)
  return view
}

async function renderRoute(pattern: string, url: string, node: ReactNode) {
  const view = await baseRenderRoute(pattern, url, node)
  mounted.push(view)
  return view
}

async function inAct(update: () => void) {
  await act(async () => update())
}

beforeEach(() => {
  resetMaintenance()
  auth.me = null
  localStorage.clear()
  vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ server: null, source: 'db' })))
})

afterEach(async () => {
  while (mounted.length) await mounted.pop()?.unmount()
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('routeTarget', () => {
  it('reads the org and project from the path', () => {
    expect(routeTarget('/')).toEqual({})
    expect(routeTarget('/dashboard')).toEqual({})
    expect(routeTarget('/admin/maintenance')).toEqual({})
    expect(routeTarget('/acme')).toEqual({ org: 'acme' })
    expect(routeTarget('/acme/settings')).toEqual({ org: 'acme' })
    expect(routeTarget('/acme/new')).toEqual({ org: 'acme' })
    expect(routeTarget('/acme/my%20game/tree/main')).toEqual({ org: 'acme', project: 'my game' })
  })

  it('survives malformed escapes', () => {
    expect(routeTarget('/acme/game/blob/main/report%')).toEqual({ org: 'acme', project: 'game' })
    expect(routeTarget('/acme/%E0%A4%A')).toEqual({ org: 'acme', project: '%E0%A4%A' })
  })
})

describe('MaintenanceBanner', () => {
  it('renders nothing without a window', async () => {
    const view = await render(<MaintenanceBanner />)
    expect(view.container.textContent).toBe('')
  })

  it('shows active windows of every scope and dismisses only upcoming ones', async () => {
    storeServer({ mode: 'full', active: false, message: 'upgrade', starts_at: '2030-01-01T10:00:00Z' })
    storeOrg('acme', { mode: 'read_only', active: true, message: 'backup', ends_at: '2030-01-01T12:00:00Z' })
    storeProject('acme/game', { mode: 'full', active: true, message: '' })
    const view = await render(<MaintenanceBanner org="acme" project="game" />)
    expect(text()).toContain('The server has maintenance scheduled (closed)')
    expect(text()).toContain('Once it starts')
    expect(text()).toContain('Starts')
    expect(text()).toContain('Organization acme is read-only for maintenance')
    expect(text()).toContain('Estimated end')
    expect(text()).toContain('Project acme/game is closed for maintenance')
    expect(text()).toContain('No end estimate')

    const dismiss = view.container.querySelectorAll('button')
    expect(dismiss).toHaveLength(1)
    await click(dismiss[0])
    expect(text()).not.toContain('scheduled')
    expect(localStorage.getItem('nipa.maintenance.dismissed')).toContain('server')
    await view.unmount()

    await render(<MaintenanceBanner org="acme" project="game" />)
    expect(text()).not.toContain('scheduled')
  })

  it('survives broken storage', async () => {
    localStorage.setItem('nipa.maintenance.dismissed', '{bad')
    storeServer({ mode: 'read_only', active: false, message: '' })
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    const view = await render(<MaintenanceBanner />)
    await click(view.container.querySelector('button'))
    expect(text()).toBe('')
    spy.mockRestore()
  })

  it('renders a refusal with its scope', async () => {
    const info: MaintenanceInfo = { scope: 'org', mode: 'read_only', message: 'backup', org: 'acme' }
    await render(<MaintenanceErrorBanner info={info} />)
    expect(text()).toContain('Organization acme is read-only for maintenance')
    expect(text()).toContain('backup')
  })
})

describe('MaintenanceGate', () => {
  const gate = (
    <MaintenanceGate>
      <span>content</span>
    </MaintenanceGate>
  )

  it('replaces closed routes with the maintenance page', async () => {
    storeServer({ mode: 'full', active: true, message: 'moving racks' })
    await renderRoute('*', '/dashboard', gate)
    expect(text()).toContain('The server is closed for maintenance')
    expect(text()).toContain('moving racks')
    expect(text()).not.toContain('content')
    expect(text()).not.toContain('Manage maintenance')
  })

  it('keeps the admin page and links super admins to it', async () => {
    auth.me = { id: '1', is_super_admin: true }
    storeServer({ mode: 'full', active: true, message: '' })
    await renderRoute('*', '/dashboard', gate)
    expect(text()).toContain('Manage maintenance')
    await mounted.pop()?.unmount()
    await renderRoute('*', '/admin/maintenance', gate)
    expect(text()).toContain('content')
  })

  it('closes project routes but keeps the maintenance tab', async () => {
    auth.me = { id: '1', is_super_admin: false }
    storeProject('acme/game', { mode: 'full', active: true, message: '' })
    await renderRoute('*', '/acme/game/tree', gate)
    expect(text()).toContain('Project acme/game is closed')
    expect(document.querySelector('a[href="/acme/game/settings?tab=maintenance"]')).not.toBeNull()
    await mounted.pop()?.unmount()
    await renderRoute('*', '/acme/game/settings?tab=maintenance', gate)
    expect(text()).toContain('content')
    await mounted.pop()?.unmount()
    await renderRoute('*', '/acme/other', gate)
    expect(text()).toContain('content')
  })

  it('leaves read-only and frozen with a live session open', async () => {
    storeServer({ mode: 'read_only', active: true, message: '' })
    await renderRoute('*', '/dashboard', gate)
    expect(text()).toContain('content')
    await inAct(() => storeServer({ mode: 'frozen', active: true, message: '' }))
    expect(text()).toContain('content')
    await inAct(() => noteSessionPaused(true))
    expect(text()).toContain('The server is frozen for maintenance')
  })

  it('checks again on demand', async () => {
    storeServer({ mode: 'full', active: true, message: '' })
    await renderRoute('*', '/dashboard', gate)
    await click(buttonByText(document.body, 'Check again'))
    await waitFor(() => text().includes('content'))
    expect(getMaintenanceState().server).toBeNull()
  })
})

describe('FormDialog', () => {
  it('renders maintenance refusals with their details', async () => {
    const err = new ApiError('server is in maintenance (read-only): backup', 503, {
      scope: 'server',
      mode: 'read_only',
      message: 'backup',
      ends_at: '2030-01-01T12:00:00Z',
    })
    await render(
      <FormDialog title="Edit" onClose={() => undefined} onSubmit={() => Promise.reject(err)}>
        <span>fields</span>
      </FormDialog>,
    )
    await click(buttonByText(document.body, 'Save'))
    await waitFor(() => text().includes('Could not save during maintenance'))
    expect(text()).toContain('backup')
  })
})

describe('TermsGate', () => {
  it('lets the app through when the terms check is refused for maintenance', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 503,
        headers: new Headers({ 'X-Nipa-Maintenance': 'server;full' }),
        text: async () => JSON.stringify({ error: 'closed', maintenance: { scope: 'server', mode: 'full', message: '' } }),
      })),
    )
    await render(
      <TermsGate>
        <span>inside</span>
      </TermsGate>,
    )
    await waitFor(() => text().includes('inside'))
  })

  it('still blocks on other failures', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'boom' }, 500)))
    await render(
      <TermsGate>
        <span>inside</span>
      </TermsGate>,
    )
    await waitFor(() => text().includes('Could not check the terms'))
  })
})

describe('MaintenanceForm', () => {
  const window: MaintenanceWindow = {
    mode: 'read_only',
    active: false,
    message: 'backup',
    starts_at: '2030-01-01T10:00:00Z',
    ends_at: '2030-01-01T12:00:00Z',
  }

  function form(props: Partial<Parameters<typeof MaintenanceForm>[0]> = {}) {
    const onSave = vi.fn(async (): Promise<MaintenanceWindow> => ({ ...window, notified: true }))
    const onEnd = vi.fn(async () => undefined)
    const onChanged = vi.fn()
    const node = (
      <MaintenanceForm
        scope="project"
        target="acme/game"
        window={null}
        notifyLabel="Email organization owners and project admins"
        onSave={onSave}
        onEnd={onEnd}
        onChanged={onChanged}
        {...props}
      />
    )
    return { node, onSave, onEnd, onChanged }
  }

  it('converts local datetime inputs', () => {
    expect(toLocalInput(undefined)).toBe('')
    expect(toLocalInput('bad')).toBe('')
    expect(fromLocalInput('')).toBeUndefined()
    expect(fromLocalInput('bad')).toBeUndefined()
    const local = toLocalInput('2030-01-01T10:00:00Z')
    expect(fromLocalInput(local)).toBe('2030-01-01T10:00:00.000Z')
  })

  it('schedules a window with notify and reports the email', async () => {
    const { node, onSave, onChanged } = form()
    await render(node)
    expect(text()).toContain('No maintenance is scheduled')
    expect(document.querySelector('option[value="frozen"]')).toBeNull()
    await choose(document.querySelector('select'), 'full')
    await type(document.querySelector('textarea'), ' disk swap ')
    expect(text()).toContain('11/500')
    await click(buttonByText(document.body, 'Now'))
    await type(document.querySelector<HTMLInputElement>('input[name="ends_at"]'), '2099-01-01T10:00')
    await click(document.querySelector('input[type="checkbox"]'))
    await click(buttonByText(document.body, 'Schedule maintenance'))
    await waitFor(() => text().includes('announcement email is on its way'))
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ mode: 'full', message: 'disk swap', notify: true, ends_at: fromLocalInput('2099-01-01T10:00') }),
    )
    expect(onChanged).toHaveBeenCalled()
  })

  it('says when no email was queued', async () => {
    const { node, onSave } = form({ window })
    onSave.mockResolvedValue({ ...window, notified: false })
    await render(node)
    await click(document.querySelector('input[type="checkbox"]'))
    await click(buttonByText(document.body, 'Update maintenance'))
    await waitFor(() => text().includes('No announcement email was sent'))
    onSave.mockResolvedValue({ ...window })
    await click(buttonByText(document.body, 'Update maintenance'))
    await waitFor(() => text().includes('Saved.') && !text().includes('No announcement'))
  })

  it('refuses an end before the start and shows save errors', async () => {
    const { node, onSave } = form({ window })
    onSave.mockRejectedValue(new Error('maintenance is controlled by MAINTENANCE_FILE'))
    await render(node)
    await type(document.querySelector<HTMLInputElement>('input[name="ends_at"]'), '2000-01-01T10:00')
    expect(text()).toContain('The estimated end must be after the start.')
    expect(buttonByText(document.body, 'Update maintenance')?.disabled).toBe(true)
    await type(document.querySelector<HTMLInputElement>('input[name="ends_at"]'), '')
    await click(buttonByText(document.body, 'Update maintenance'))
    await waitFor(() => text().includes('controlled by MAINTENANCE_FILE'))
  })

  it('ends a window after confirmation', async () => {
    const { node, onEnd, onChanged } = form({ window: { ...window, active: true } })
    await render(node)
    await click(buttonByText(document.body, 'End maintenance'))
    await confirmDialog('End maintenance')
    await waitFor(() => onChanged.mock.calls.length > 0)
    expect(onEnd).toHaveBeenCalled()
  })

  it('reports a failed end', async () => {
    const { node, onEnd } = form({ window })
    onEnd.mockRejectedValue(new Error('not found'))
    await render(node)
    await click(buttonByText(document.body, 'End maintenance'))
    await confirmDialog('End maintenance')
    await waitFor(() => text().includes('not found'))
  })

  it('offers frozen for the server and locks the form for the override file or an active freeze', async () => {
    const server = form({ scope: 'server', target: '', notifyLabel: 'Email organization owners' })
    const view = await render(server.node)
    await choose(document.querySelector('select'), 'frozen')
    expect(text()).toContain('nipad maintenance end')
    await view.unmount()

    const file = form({ scope: 'server', target: '', source: 'file', window: { ...window, active: true } })
    const fileView = await render(file.node)
    expect(text()).toContain('Controlled by MAINTENANCE_FILE')
    expect(buttonByText(document.body, 'Update maintenance')?.disabled).toBe(true)
    await fileView.unmount()

    const frozen = form({ scope: 'server', target: '', window: { ...window, mode: 'frozen', active: true } })
    await render(frozen.node)
    expect(text()).toContain('The server is frozen')
    expect(buttonByText(document.body, 'End maintenance')?.disabled).toBe(true)
  })
})
