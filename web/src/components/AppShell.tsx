import { Button, Link as PrimerLink } from '@primer/react'
import { SignOutIcon } from '@primer/octicons-react'
import { Link, Outlet } from 'react-router-dom'
import { useAuth } from '../auth'

export default function AppShell() {
  const { me, signOut } = useAuth()

  return (
    <div>
      <header
        style={{
          borderBottom: '1px solid var(--borderColor-default)',
          padding: '12px 16px',
          display: 'flex',
          alignItems: 'center',
          gap: 16,
        }}
      >
        <PrimerLink as={Link} to="/" style={{ fontWeight: 700, fontSize: 18 }}>
          Nipa
        </PrimerLink>
        <div style={{ flex: 1 }} />
        {me?.is_admin || me?.is_super_admin ? (
          <PrimerLink as={Link} to="/admin/users">
            Users
          </PrimerLink>
        ) : null}
        <PrimerLink as={Link} to="/settings/profile">
          {me?.name ?? 'Profile'}
        </PrimerLink>
        <Button onClick={signOut} leadingVisual={<SignOutIcon />} size="small">
          Sign out
        </Button>
      </header>
      <Outlet />
    </div>
  )
}
