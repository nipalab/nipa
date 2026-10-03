import { useState } from 'react'
import { Button, Label, Stack, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { useNavigate } from 'react-router-dom'
import { deactivateUser, listUsers, resetUserPassword, updateUserFlags } from '../api/endpoints'
import type { UserResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/repo/ActorAvatar'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'

const HEADER_CELL = { padding: '8px 12px', fontWeight: 600, textAlign: 'left' as const }

function roleLabel(user: UserResponse) {
  if (user.is_super_admin) return <Label variant="danger">Super admin</Label>
  if (user.is_admin) return <Label variant="attention">Admin</Label>
  return <Label>Member</Label>
}

export default function AdminUsersPage() {
  const { me } = useAuth()
  const navigate = useNavigate()
  const { data: users, error, loading, reload } = useAsync(listUsers, [])
  const [filter, setFilter] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  const query = filter.trim().toLowerCase()
  const visible = (users ?? []).filter(
    (user) =>
      query === '' ||
      user.name.toLowerCase().includes(query) ||
      user.email.toLowerCase().includes(query) ||
      user.id.includes(query),
  )

  return (
    <Page
      title="Users"
      subtitle="Global administration"
      actions={
        <Stack direction="horizontal" gap="condensed" align="center">
          <TextInput
            leadingVisual={SearchIcon}
            placeholder="Filter users…"
            aria-label="Filter users"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <Button variant="primary" onClick={() => navigate('/admin/users/new')}>
            New user
          </Button>
        </Stack>
      }
    >
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}
      {!loading && visible.length === 0 && (
        <EmptyState>{query ? 'No users match the filter.' : 'No users.'}</EmptyState>
      )}
      {visible.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)', fontSize: 12 }}>
                <th style={HEADER_CELL}>User</th>
                <th style={HEADER_CELL}>Role</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((user) => (
                <tr
                  key={user.id}
                  className="nipa-user-row"
                  style={{ borderTop: '1px solid var(--borderColor-muted)' }}
                >
                  <td style={{ padding: '8px 12px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <ActorAvatar
                        actor={{ user_id: user.id, name: user.name, photo_url: user.photo_url }}
                        size={28}
                      />
                      <div>
                        <div style={{ fontWeight: 600 }}>
                          {user.name}
                          {me?.id === user.id && (
                            <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}> (you)</span>
                          )}
                        </div>
                        <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{user.email}</div>
                      </div>
                    </div>
                  </td>
                  <td style={{ padding: '8px 12px' }}>{roleLabel(user)}</td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>
                    <Stack direction="horizontal" gap="condensed" justify="end">
                      {me?.is_super_admin && (
                        <Button
                          size="small"
                          onClick={() => run(() => updateUserFlags(user.id, !user.is_admin, user.is_super_admin))}
                        >
                          {user.is_admin ? 'Remove admin' : 'Make admin'}
                        </Button>
                      )}
                      <Button
                        size="small"
                        onClick={() => {
                          const next = window.prompt(`New password for ${user.email}`)
                          if (next) {
                            run(() => resetUserPassword(user.id, next))
                          }
                        }}
                      >
                        Reset password
                      </Button>
                      <Button size="small" variant="danger" onClick={() => run(() => deactivateUser(user.id))}>
                        Deactivate
                      </Button>
                    </Stack>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  )
}
