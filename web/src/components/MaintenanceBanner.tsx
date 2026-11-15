import { useState } from 'react'
import { Banner, Stack } from '@primer/react'
import { useLocation } from 'react-router-dom'
import type { MaintenanceInfo } from '../api/client'
import {
  formatLocal,
  modeDescription,
  modeLabel,
  scopeName,
  useMaintenance,
  windowsFor,
  type ScopedWindow,
} from '../maintenance'

const RESERVED = new Set(['dashboard', 'onboarding', 'admin', 'settings', 'login', 'register', 'verify', 'oauth'])
const NON_PROJECT = new Set(['settings', 'new'])
const DISMISSED_KEY = 'nipa.maintenance.dismissed'

export function routeTarget(pathname: string): { org?: string; project?: string } {
  const [org, project] = pathname.split('/').filter(Boolean).map((part) => decodeURIComponent(part))
  if (!org || RESERVED.has(org)) return {}
  if (!project || NON_PROJECT.has(project)) return { org }
  return { org, project }
}

export function useRouteTarget() {
  return routeTarget(useLocation().pathname)
}

function windowKey(item: ScopedWindow): string {
  return [item.scope, item.target, item.window.mode, item.window.starts_at ?? '', item.window.message].join('|')
}

function readDismissed(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? '[]') as unknown
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function writeDismissed(keys: string[]) {
  try {
    localStorage.setItem(DISMISSED_KEY, JSON.stringify(keys.slice(-20)))
  } catch {
    // storage unavailable: the dismissal lasts for this page only
  }
}

export function maintenanceTitle(item: ScopedWindow): string {
  const name = scopeName(item)
  if (!item.window.active) return `${name} has maintenance scheduled (${modeLabel(item.window.mode)})`
  return `${name} is ${modeLabel(item.window.mode)} for maintenance`
}

export function MaintenanceTimes({ item }: { item: ScopedWindow }) {
  const start = formatLocal(item.window.starts_at)
  const end = formatLocal(item.window.ends_at)
  const parts: string[] = []
  if (start) parts.push(`${item.window.active ? 'Started' : 'Starts'} ${start}`)
  parts.push(end ? `estimated end ${end}` : 'no end estimate')
  const text = parts.join(', ')
  return <span>{text.charAt(0).toUpperCase() + text.slice(1)}.</span>
}

function variantFor(item: ScopedWindow): 'info' | 'warning' | 'critical' {
  if (!item.window.active) return 'info'
  return item.window.mode === 'read_only' ? 'warning' : 'critical'
}

export function MaintenanceWindowBanner({ item, onDismiss }: { item: ScopedWindow; onDismiss?: () => void }) {
  return (
    <Banner variant={variantFor(item)} title={maintenanceTitle(item)} onDismiss={onDismiss}>
      <Stack direction="vertical" gap="condensed">
        {item.window.message && <span>{item.window.message}</span>}
        <span>
          {item.window.active ? modeDescription(item.window.mode) : `Once it starts: ${modeDescription(item.window.mode)}`}{' '}
          <MaintenanceTimes item={item} />
        </span>
      </Stack>
    </Banner>
  )
}

export function MaintenanceBanner({ org, project }: { org?: string; project?: string }) {
  const state = useMaintenance()
  const [dismissed, setDismissed] = useState<string[]>(readDismissed)
  const items = windowsFor(state, org, project).filter((item) => item.window.active || !dismissed.includes(windowKey(item)))
  if (items.length === 0) return null

  function dismiss(item: ScopedWindow) {
    const next = [...dismissed, windowKey(item)]
    setDismissed(next)
    writeDismissed(next)
  }

  return (
    <div style={{ padding: 'var(--base-size-8) var(--base-size-16) 0' }}>
      <Stack direction="vertical" gap="condensed">
        {items.map((item) => (
          <MaintenanceWindowBanner
            key={windowKey(item)}
            item={item}
            onDismiss={item.window.active ? undefined : () => dismiss(item)}
          />
        ))}
      </Stack>
    </div>
  )
}

export function infoToScoped(info: MaintenanceInfo): ScopedWindow {
  const target = info.scope === 'project' ? info.project ?? '' : info.scope === 'org' ? info.org ?? '' : ''
  return {
    scope: info.scope,
    target,
    window: {
      mode: info.mode,
      active: true,
      message: info.message,
      starts_at: info.starts_at,
      ends_at: info.ends_at,
    },
  }
}

export function MaintenanceErrorBanner({ info, title }: { info: MaintenanceInfo; title?: string }) {
  const item = infoToScoped(info)
  return (
    <Banner variant={info.mode === 'read_only' ? 'warning' : 'critical'} title={title ?? maintenanceTitle(item)}>
      <Stack direction="vertical" gap="condensed">
        {info.message && <span>{info.message}</span>}
        <span>
          {modeDescription(info.mode)} <MaintenanceTimes item={item} />
        </span>
      </Stack>
    </Banner>
  )
}
