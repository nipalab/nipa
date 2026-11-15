import { useEffect, useSyncExternalStore } from 'react'
import type { MaintenanceInfo } from './api/client'
import { getMaintenance, getOrgMaintenance, getProjectMaintenance } from './api/endpoints'
import type { MaintenanceMode, MaintenanceWindow } from './api/models'

export const MAINTENANCE_POLL_MS = 60_000

export type MaintenanceScope = 'server' | 'org' | 'project'

export interface MaintenanceState {
  server: MaintenanceWindow | null
  source: string
  orgs: Record<string, MaintenanceWindow | null>
  projects: Record<string, MaintenanceWindow | null>
  sessionPaused: boolean
}

export interface ScopedWindow {
  scope: MaintenanceScope
  target: string
  window: MaintenanceWindow
}

export interface MaintenanceTarget {
  org?: string
  project?: string
  authenticated: boolean
}

const MODES: MaintenanceMode[] = ['read_only', 'full', 'frozen']
const RANK: Record<MaintenanceMode, number> = { read_only: 1, full: 2, frozen: 3 }
const SCOPE_RANK: Record<MaintenanceScope, number> = { server: 3, org: 2, project: 1 }

const EMPTY: MaintenanceState = { server: null, source: 'db', orgs: {}, projects: {}, sessionPaused: false }

let state: MaintenanceState = EMPTY
const listeners = new Set<() => void>()
let lastTarget: MaintenanceTarget = { authenticated: false }

function publish(next: MaintenanceState) {
  state = next
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function getMaintenanceState(): MaintenanceState {
  return state
}

export function resetMaintenance() {
  lastTarget = { authenticated: false }
  publish(EMPTY)
}

export function projectKey(org: string, project: string): string {
  return `${org}/${project}`
}

export function asWindow(value: unknown): MaintenanceWindow | null {
  if (!value || typeof value !== 'object') return null
  const candidate = value as Partial<MaintenanceWindow>
  if (!candidate.mode || !MODES.includes(candidate.mode)) return null
  return {
    ...candidate,
    mode: candidate.mode,
    active: Boolean(candidate.active),
    message: typeof candidate.message === 'string' ? candidate.message : '',
  }
}

export function noteMaintenance(info: MaintenanceInfo | undefined) {
  const window = asWindow({ ...info, active: true })
  if (!info || !window) return
  if (info.scope === 'project' && info.project) {
    publish({ ...state, projects: { ...state.projects, [info.project]: window } })
  } else if (info.scope === 'org' && info.org) {
    publish({ ...state, orgs: { ...state.orgs, [info.org]: window } })
  } else {
    publish({ ...state, server: window })
  }
}

export function noteSessionPaused(paused: boolean) {
  if (state.sessionPaused !== paused) publish({ ...state, sessionPaused: paused })
}

export function storeServer(server: unknown, source?: string) {
  publish({ ...state, server: asWindow(server), source: source ?? state.source })
}

export function storeOrg(org: string, window: unknown) {
  publish({ ...state, orgs: { ...state.orgs, [org]: asWindow(window) } })
}

export function storeProject(key: string, window: unknown) {
  publish({ ...state, projects: { ...state.projects, [key]: asWindow(window) } })
}

async function load(target: MaintenanceTarget) {
  try {
    const status = await getMaintenance()
    storeServer(status?.server, typeof status?.source === 'string' ? status.source : undefined)
  } catch {
    return
  }
  if (!target.authenticated || !target.org) return
  try {
    if (target.project) {
      const status = await getProjectMaintenance(target.org, target.project)
      storeOrg(target.org, status?.org)
      storeProject(projectKey(target.org, target.project), status?.project)
    } else {
      const status = await getOrgMaintenance(target.org)
      storeOrg(target.org, status?.org)
    }
  } catch {
    // keep the last known state
  }
}

export function recheckMaintenance(): Promise<void> {
  return load(lastTarget)
}

export function useMaintenance(): MaintenanceState {
  return useSyncExternalStore(subscribe, getMaintenanceState, getMaintenanceState)
}

export function useMaintenancePolling(target: MaintenanceTarget, intervalMs = MAINTENANCE_POLL_MS) {
  const { org, project, authenticated } = target
  useEffect(() => {
    lastTarget = { org, project, authenticated }
    let active = true
    const run = () => {
      if (active) void load({ org, project, authenticated })
    }
    run()
    const timer = setInterval(run, intervalMs)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [org, project, authenticated, intervalMs])
}

export function windowsFor(current: MaintenanceState, org?: string, project?: string): ScopedWindow[] {
  const out: ScopedWindow[] = []
  if (current.server) out.push({ scope: 'server', target: '', window: current.server })
  const orgWindow = org ? current.orgs[org] : null
  if (org && orgWindow) out.push({ scope: 'org', target: org, window: orgWindow })
  const key = org && project ? projectKey(org, project) : ''
  const projectWindow = key ? current.projects[key] : null
  if (projectWindow) out.push({ scope: 'project', target: key, window: projectWindow })
  return out
}

export function effectiveWindow(windows: ScopedWindow[]): ScopedWindow | null {
  let best: ScopedWindow | null = null
  for (const item of windows) {
    if (!item.window.active) continue
    if (
      !best ||
      RANK[item.window.mode] > RANK[best.window.mode] ||
      (RANK[item.window.mode] === RANK[best.window.mode] && SCOPE_RANK[item.scope] > SCOPE_RANK[best.scope])
    ) {
      best = item
    }
  }
  return best
}

export function closesApp(item: ScopedWindow | null, sessionPaused: boolean): boolean {
  if (!item) return false
  if (item.window.mode === 'full') return true
  return item.window.mode === 'frozen' && sessionPaused
}

export function useEffectiveMaintenance(org?: string, project?: string): ScopedWindow | null {
  return effectiveWindow(windowsFor(useMaintenance(), org, project))
}

export function modeLabel(mode: MaintenanceMode): string {
  switch (mode) {
    case 'read_only':
      return 'read-only'
    case 'full':
      return 'closed'
    default:
      return 'frozen'
  }
}

export function modeDescription(mode: MaintenanceMode): string {
  switch (mode) {
    case 'read_only':
      return 'You can browse and download, but changes are refused until maintenance ends.'
    case 'full':
      return 'Repositories cannot be opened until maintenance ends.'
    default:
      return 'Sign-in and changes are paused. Browsing keeps working while your session lasts.'
  }
}

export function scopeName(item: { scope: MaintenanceScope; target: string }): string {
  if (item.scope === 'project') return `Project ${item.target}`
  if (item.scope === 'org') return `Organization ${item.target}`
  return 'The server'
}

export function formatLocal(iso?: string): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}
