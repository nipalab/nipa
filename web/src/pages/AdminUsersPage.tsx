import { useState } from 'react'
import { Button, FormControl, Stack, TextInput } from '@primer/react'
import {
  createUser,
  deactivateUser,
  listUsers,
  resetUserPassword,
  updateUserFlags,
} from '../api/endpoints'
import { useAuth } from '../auth'
import { ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function AdminUsersPage() {
  const { me } = useAuth()
  const { data: users, error, loading, reload } = useAsync(listUsers, [])
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
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

  return (
    <Page title="Users" subtitle="Global administration">
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          {users?.map((user) => (
            <tr key={user.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
              <td style={{ padding: '6px 4px' }}>
                {user.name} · {user.email}
                {user.is_super_admin ? ' · superadmin' : user.is_admin ? ' · admin' : ''}
              </td>
              <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                <Mono>{user.id}</Mono>
              </td>
              <td style={{ padding: '6px 4px', textAlign: 'right' }}>
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

      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await createUser(name, email, password)
            setName('')
            setEmail('')
            setPassword('')
          })
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Create user</strong>
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Email</FormControl.Label>
            <TextInput block value={email} onChange={(event) => setEmail(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Create user
          </Button>
        </Stack>
      </form>
    </Page>
  )
}
