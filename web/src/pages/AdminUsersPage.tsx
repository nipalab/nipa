import { useState } from 'react'
import { ActionList, ActionMenu, Banner, Button, FormControl, IconButton, Label, Text, TextInput } from '@primer/react'
import {
  KebabHorizontalIcon,
  KeyIcon,
  MailIcon,
  PeopleIcon,
  PersonAddIcon,
  SearchIcon,
  ShieldCheckIcon,
  ShieldIcon,
  ShieldLockIcon,
  StopIcon,
} from '@primer/octicons-react'
import { useNavigate } from 'react-router-dom'
import { deactivateUser, listUsers, resetUserMFA, resetUserPassword, updateUserEmail, updateUserFlags } from '../api/endpoints'
import type { UserResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { PasswordFields, passwordProblem } from '../components/PasswordFields'
import { EmptyState, ErrorBanner, Loading, NoPermission, Page } from '../components/ui'
import { useAction, useAsync } from '../hooks'

type RoleFilter = 'all' | 'admins' | 'members'

const isAdmin = (user: UserResponse) => user.is_admin || user.is_super_admin

function RoleLabel({ user }: { user: UserResponse }) {
  if (user.is_super_admin) {
    return (
      <Label variant="done">
        <ShieldLockIcon size={12} /> Super admin
      </Label>
    )
  }
  if (user.is_admin) {
    return (
      <Label variant="accent">
        <ShieldIcon size={12} /> Admin
      </Label>
    )
  }
  return null
}

function ResetPasswordDialog({ user, onClose }: { user: UserResponse; onClose: () => void }) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  return (
    <FormDialog
      title="Reset password"
      submitLabel="Reset password"
      submitDisabled={passwordProblem(password, confirm) !== null}
      onClose={onClose}
      onSubmit={() => resetUserPassword(user.id, password)}
    >
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)', fontSize: 14 }}>
        Set a new password for <strong style={{ color: 'var(--fgColor-default)' }}>{user.name}</strong> ({user.email}).
      </Text>
      <PasswordFields
        label="New password"
        autoFocus
        password={password}
        confirm={confirm}
        onChange={(nextPassword, nextConfirm) => {
          setPassword(nextPassword)
          setConfirm(nextConfirm)
        }}
      />
    </FormDialog>
  )
}

function EditEmailDialog({ user, onClose, onSaved }: { user: UserResponse; onClose: () => void; onSaved: () => void }) {
  const [email, setEmail] = useState(user.email)
  const next = email.trim()
  return (
    <FormDialog
      title="Change email"
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
        <FormControl.Caption>{user.name} signs in with this address and receives notifications at it.</FormControl.Caption>
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
  const [role, setRole] = useState<RoleFilter>('all')
  const [resetting, setResetting] = useState<UserResponse | null>(null)
  const [editing, setEditing] = useState<UserResponse | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  if (me && !me.is_admin && !me.is_super_admin) {
    return (
      <Page title="Users">
        <NoPermission>You need global admin permission to manage users.</NoPermission>
      </Page>
    )
  }

  const all = users ?? []
  const counts: Record<RoleFilter, number> = {
    all: all.length,
    admins: all.filter(isAdmin).length,
    members: all.filter((user) => !isAdmin(user)).length,
  }
  const query = filter.trim().toLowerCase()
  const visible = all
    .filter((user) => role === 'all' || (role === 'admins') === isAdmin(user))
    .filter(
      (user) =>
        query === '' ||
        user.name.toLowerCase().includes(query) ||
        user.email.toLowerCase().includes(query) ||
        user.id.includes(query),
    )

  async function deactivate(user: UserResponse) {
    const ok = await confirm({
      title: `Deactivate ${user.name}?`,
      content: `${user.email} will be signed out everywhere and can no longer sign in. Their history stays.`,
      confirmLabel: 'Deactivate',
    })
    if (ok) await run(() => deactivateUser(user.id))
  }

  async function resetMFA(user: UserResponse) {
    const ok = await confirm({
      title: `Reset two-factor authentication for ${user.name}?`,
      content: `${user.email} will sign in with the password alone until they enable two-factor authentication again. Their recovery codes are deleted and they get an email about it.`,
      confirmLabel: 'Reset two-factor',
    })
    if (!ok) return
    setNotice(null)
    const done = await run(() => resetUserMFA(user.id))
    if (done) setNotice(`Two-factor authentication was reset for ${user.name}.`)
  }

  async function toggleAdmin(user: UserResponse) {
    const promote = !user.is_admin
    const ok = await confirm({
      title: promote ? `Make ${user.name} an admin?` : `Remove admin from ${user.name}?`,
      content: promote
        ? 'Admins can manage users and every organization on this server.'
        : `${user.name} keeps their organization and project access but can no longer manage users.`,
      confirmLabel: promote ? 'Make admin' : 'Remove admin',
    })
    if (ok) await run(() => updateUserFlags(user.id, promote, user.is_super_admin))
  }

  const tabs: { key: RoleFilter; label: string }[] = [
    { key: 'all', label: 'All' },
    { key: 'admins', label: 'Admins' },
    { key: 'members', label: 'Members' },
  ]

  return (
    <Page
      title="Users"
      subtitle="Everyone who can sign in to this Nipa server."
      actions={
        <Button variant="primary" leadingVisual={PersonAddIcon} onClick={() => navigate('/admin/users/new')}>
          New user
        </Button>
      }
    >
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
      <TextInput
        block
        leadingVisual={SearchIcon}
        placeholder="Find a user by name or email…"
        aria-label="Filter users"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
      />
      <div className="nipa-list-box">
        <div className="nipa-list-header">
          <nav className="nipa-state-tabs" aria-label="User role">
            {tabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                aria-pressed={role === tab.key}
                className={role === tab.key ? 'nipa-state-tab nipa-state-tab-active' : 'nipa-state-tab'}
                onClick={() => setRole(tab.key)}
              >
                {tab.key === 'admins' ? <ShieldIcon /> : <PeopleIcon />}
                {tab.label}
                <span className="nipa-count">{counts[tab.key]}</span>
              </button>
            ))}
          </nav>
        </div>
        {loading && <Loading />}
        {!loading && visible.length === 0 && (
          <EmptyState bordered={false}>{query || role !== 'all' ? 'No users match these filters.' : 'No users yet.'}</EmptyState>
        )}
        {visible.map((user) => {
          const self = me?.id === user.id
          const canToggleAdmin = Boolean(me?.is_super_admin) && !self && !user.is_super_admin
          const canManage = Boolean(me?.is_super_admin) || self || !isAdmin(user)
          return (
            <div key={user.id} className="nipa-list-row" style={{ alignItems: 'center' }}>
              <ActorAvatar actor={{ user_id: user.id, name: user.name, photo_url: user.photo_url }} size={32} />
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--base-size-8)', flexWrap: 'wrap' }}>
                  <span style={{ fontWeight: 600 }}>{user.name}</span>
                  {self && <Label variant="secondary">You</Label>}
                  <RoleLabel user={user} />
                </div>
                <div className="nipa-list-row-meta">{user.email}</div>
              </div>
              <ActionMenu>
                <ActionMenu.Anchor>
                  <IconButton icon={KebabHorizontalIcon} variant="invisible" aria-label={`Manage ${user.name}`} />
                </ActionMenu.Anchor>
                <ActionMenu.Overlay width="small" align="end">
                  <ActionList>
                    <ActionList.Item onSelect={() => setEditing(user)}>
                      <ActionList.LeadingVisual>
                        <MailIcon />
                      </ActionList.LeadingVisual>
                      Edit email
                    </ActionList.Item>
                    <ActionList.Item onSelect={() => setResetting(user)}>
                      <ActionList.LeadingVisual>
                        <KeyIcon />
                      </ActionList.LeadingVisual>
                      Reset password
                    </ActionList.Item>
                    {canManage && (
                      <ActionList.Item onSelect={() => void resetMFA(user)}>
                        <ActionList.LeadingVisual>
                          <ShieldLockIcon />
                        </ActionList.LeadingVisual>
                        Reset two-factor
                      </ActionList.Item>
                    )}
                    {canToggleAdmin && (
                      <ActionList.Item onSelect={() => void toggleAdmin(user)}>
                        <ActionList.LeadingVisual>
                          <ShieldCheckIcon />
                        </ActionList.LeadingVisual>
                        {user.is_admin ? 'Remove admin' : 'Make admin'}
                      </ActionList.Item>
                    )}
                    {!self && (
                      <>
                        <ActionList.Divider />
                        <ActionList.Item variant="danger" onSelect={() => void deactivate(user)}>
                          <ActionList.LeadingVisual>
                            <StopIcon />
                          </ActionList.LeadingVisual>
                          Deactivate
                        </ActionList.Item>
                      </>
                    )}
                  </ActionList>
                </ActionMenu.Overlay>
              </ActionMenu>
            </div>
          )
        })}
      </div>
      {resetting && <ResetPasswordDialog user={resetting} onClose={() => setResetting(null)} />}
      {editing && <EditEmailDialog user={editing} onClose={() => setEditing(null)} onSaved={reload} />}
    </Page>
  )
}
