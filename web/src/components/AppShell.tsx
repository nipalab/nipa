import { Link as PrimerLink } from '@primer/react'
import { Link, Outlet } from 'react-router-dom'
import { useAuth } from '../auth'
import { MaintenanceGate } from '../pages/MaintenancePage'
import { MaintenanceBanner, useRouteTarget } from './MaintenanceBanner'
import { APP_HOME } from '../routes'
import { NipaLogo } from './NipaLogo'
import { TermsGate } from './TermsGate'
import { UsageBanner } from './UsageBanner'
import { UserMenu } from './UserMenu'

export default function AppShell() {
  const { me, signOut } = useAuth()
  const { org, project } = useRouteTarget()

  return (
    <div>
      <header
        style={{
          borderBottom: '1px solid var(--borderColor-default)',
          padding: 'var(--base-size-12) var(--base-size-16)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--base-size-16)',
        }}
      >
        <PrimerLink
          as={Link}
          to={APP_HOME}
          aria-label="Nipa home"
          style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--base-size-8)', fontWeight: 700, fontSize: 18, color: 'var(--fgColor-default)' }}
        >
          <NipaLogo size={32} />
          Nipa
        </PrimerLink>
        <div style={{ flex: 1 }} />
        <UserMenu me={me} onSignOut={signOut} />
      </header>
      <MaintenanceBanner org={org} project={project} />
      <UsageBanner enabled={Boolean(me)} />
      <MaintenanceGate>
        <TermsGate>
          <Outlet />
        </TermsGate>
      </MaintenanceGate>
    </div>
  )
}
