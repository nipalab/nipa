import type { ReactNode } from 'react'
import { Button, Link as PrimerLink } from '@primer/react'
import { SignOutIcon } from '@primer/octicons-react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { useAuth } from '../auth'
import { APP_HOME } from '../routes'
import { ActorAvatar } from './ActorAvatar'

function NavLink({ to, children }: { to: string; children: ReactNode }) {
  const { pathname } = useLocation()
  const active = pathname === to || pathname.startsWith(`${to}/`)
  return (
    <PrimerLink
      as={Link}
      to={to}
      aria-current={active ? 'page' : undefined}
      style={{ fontWeight: active ? 600 : undefined }}
    >
      {children}
    </PrimerLink>
  )
}

export default function AppShell() {
  const { me, signOut } = useAuth()

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
        <PrimerLink as={Link} to={APP_HOME} style={{ fontWeight: 700, fontSize: 18 }}>
          Nipa
        </PrimerLink>
        <div style={{ flex: 1 }} />
        {me?.is_admin || me?.is_super_admin ? <NavLink to="/admin/users">Users</NavLink> : null}
        <NavLink to="/settings/billing">Billing</NavLink>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--base-size-8)' }}>
          {me && <ActorAvatar actor={{ user_id: me.id, name: me.name, photo_url: me.photo_url }} size={20} />}
          <NavLink to="/settings/profile">{me?.name ?? 'Profile'}</NavLink>
        </span>
        <Button onClick={signOut} leadingVisual={<SignOutIcon />} size="small">
          Sign out
        </Button>
      </header>
      <Outlet />
    </div>
  )
}
