import { useState, type ReactNode } from 'react'
import { Button, Heading, Link as PrimerLink, Stack, Text } from '@primer/react'
import { ToolsIcon } from '@primer/octicons-react'
import { Link, useLocation, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { MaintenanceTimes, maintenanceTitle, useRouteTarget } from '../components/MaintenanceBanner'
import {
  closesApp,
  effectiveWindow,
  modeDescription,
  recheckMaintenance,
  useMaintenance,
  windowsFor,
  type ScopedWindow,
} from '../maintenance'

export function MaintenancePage({ item }: { item: ScopedWindow }) {
  const { me } = useAuth()
  const [busy, setBusy] = useState(false)

  async function recheck() {
    setBusy(true)
    try {
      await recheckMaintenance()
    } finally {
      setBusy(false)
    }
  }

  const manage =
    item.scope !== 'server' ? `/${item.target}/settings?tab=maintenance` : me?.is_super_admin ? '/admin/maintenance' : null

  return (
    <div style={{ maxWidth: 640, margin: '64px auto', padding: '0 16px' }} role="main" aria-label="Maintenance">
      <Stack direction="vertical" gap="normal">
        <ToolsIcon size={32} />
        <Heading as="h1" style={{ fontSize: 24 }}>
          {maintenanceTitle(item)}
        </Heading>
        {item.window.message && <Text as="p" style={{ margin: 0 }}>{item.window.message}</Text>}
        <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
          {modeDescription(item.window.mode)} <MaintenanceTimes item={item} /> This page checks again every minute.
        </Text>
        <Stack direction="horizontal" gap="condensed" align="center">
          <Button onClick={() => void recheck()} loading={busy}>
            Check again
          </Button>
          {me && manage && (
            <PrimerLink as={Link} to={manage}>
              Manage maintenance
            </PrimerLink>
          )}
        </Stack>
      </Stack>
    </div>
  )
}

function exempt(pathname: string, tab: string | null, item: ScopedWindow): boolean {
  if (pathname === '/admin/maintenance') return true
  if (item.scope === 'server' || tab !== 'maintenance') return false
  const parts = pathname.split('/').filter(Boolean)
  return parts[parts.length - 1] === 'settings'
}

export function useClosedWindow(): ScopedWindow | null {
  const state = useMaintenance()
  const { org, project } = useRouteTarget()
  const { pathname } = useLocation()
  const [params] = useSearchParams()
  const item = effectiveWindow(windowsFor(state, org, project))
  if (!closesApp(item, state.sessionPaused) || !item) return null
  return exempt(pathname, params.get('tab'), item) ? null : item
}

export function MaintenanceGate({ children }: { children: ReactNode }) {
  const closed = useClosedWindow()
  if (closed) return <MaintenancePage item={closed} />
  return <>{children}</>
}
