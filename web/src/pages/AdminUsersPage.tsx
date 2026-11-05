import { useState } from 'react'
import { Button, FormControl, Label, Stack, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { useNavigate } from 'react-router-dom'
import { deactivateUser, listUsers, resetUserPassword, updateUserEmail, updateUserFlags } from '../api/endpoints'
import type { UserResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { DataTable, type Column } from '../components/DataTable'
import { FormDialog } from '../components/FormDialog'
import { ErrorBanner, Page } from '../components/ui'
import { useAction, useAsync } from '../hooks'

function roleLabel(user: UserResponse) {
  if (user.is_super_admin) return <Label variant="danger">Super admin</Label>
  if (user.is_admin) return <Label variant="attention">Admin</Label>
  return <Label>Member</Label>
}

function ResetPasswordDialog({ user, onClose }: { user: UserResponse; onClose: () => void }) {
  const [password, setPassword] = useState('')
  return (
    <FormDialog
      title={`Reset password for ${user.email}`}
      submitLabel="Reset password"
      submitDisabled={password === ''}
      onClose={onClose}
      onSubmit={() => resetUserPassword(user.id, password)}
    >
      <FormControl required>
        <FormControl.Label>New password</FormControl.Label>
        <TextInput block autoFocus type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

function EditEmailDialog({ user, onClose, onSaved }: { user: UserResponse; onClose: () => void; onSaved: () => void }) {
  const [email, setEmail] = useState(user.email)
  const next = email.trim()
  return (
    <FormDialog
      title={`Change email for ${user.name}`}
      submitLabel="Save email"
      submitDisabled={next === '' || next === user.email}
      onClose={onClose}
      onSubmit={async () => {
        await updateUserEmail(user.id, next)
        onSaved()
      }}
    >
      <FormControl required>
        <FormControl.Label>Email</FormControl.Label>
        <TextInput block autoFocus type="email" value={email} onChange={(event) => setEmail(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

export default function AdminUsersPage() {
  const { me } = useAuth()
  const navigate = useNavigate()
  const confirm = useConfirmAction()
  const { data: users, error, loading, reload } = useAsync(listUsers, [])
  const { run, error: actionError, clearError } = useAction(reload)
  const [filter, setFilter] = useState('')
  const [resetting, setResetting] = useState<UserResponse | null>(null)
  const [editing, setEditing] = useState<UserResponse | null>(null)

  const query = filter.trim().toLowerCase()
  const visible = (users ?? []).filter(
    (user) =>
      query === '' ||
      user.name.toLowerCase().includes(query) ||
      user.email.toLowerCase().includes(query) ||
      user.id.includes(query),
  )

  async function deactivate(user: UserResponse) {
    const ok = await confirm({
      title: `Deactivate ${user.name}?`,
      content: `${user.email} will no longer be able to sign in.`,
      confirmLabel: 'Deactivate',
    })
    if (ok) await run(() => deactivateUser(user.id))
  }

  const columns: Column<UserResponse>[] = [
    {
      id: 'user',
      header: 'User',
      rowHeader: true,
      renderCell: (user) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--base-size-12)' }}>
          <ActorAvatar actor={{ user_id: user.id, name: user.name, photo_url: user.photo_url }} size={28} />
          <div>
            <div style={{ fontWeight: 600 }}>
              {user.name}
              {me?.id === user.id && <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}> (you)</span>}
            </div>
            <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{user.email}</div>
          </div>
        </div>
      ),
    },
    { id: 'role', header: 'Role', renderCell: roleLabel },
    {
      id: 'actions',
      header: 'Actions',
      align: 'end',
      renderCell: (user) => (
        <Stack direction="horizontal" gap="condensed" justify="end">
          {me?.is_super_admin && (
            <Button size="small" onClick={() => void run(() => updateUserFlags(user.id, !user.is_admin, user.is_super_admin))}>
              {user.is_admin ? 'Remove admin' : 'Make admin'}
            </Button>
          )}
          <Button size="small" onClick={() => setEditing(user)}>
            Edit email
          </Button>
          <Button size="small" onClick={() => setResetting(user)}>
            Reset password
          </Button>
          <Button size="small" variant="danger" onClick={() => void deactivate(user)}>
            Deactivate
          </Button>
        </Stack>
      ),
    },
  ]

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
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <DataTable
        label="Users"
        loading={loading}
        data={visible}
        columns={columns}
        empty={query ? 'No users match the filter.' : 'No users.'}
      />
      {resetting && <ResetPasswordDialog user={resetting} onClose={() => setResetting(null)} />}
      {editing && <EditEmailDialog user={editing} onClose={() => setEditing(null)} onSaved={reload} />}
    </Page>
  )
}
