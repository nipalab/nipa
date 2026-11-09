import { useState } from 'react'
import { Button, Checkbox, FormControl, Stack, TextInput } from '@primer/react'
import { useNavigate } from 'react-router-dom'
import { createUser, updateUserFlags } from '../api/endpoints'
import { useAuth } from '../auth'
import { PasswordFields, passwordProblem } from '../components/PasswordFields'
import { FormCard } from '../components/SettingsCard'
import { ErrorBanner, NoPermission, Page } from '../components/ui'
import { errorMessage } from '../hooks'

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export default function NewUserPage() {
  const { me } = useAuth()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [makeAdmin, setMakeAdmin] = useState(false)
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const isAdmin = Boolean(me?.is_admin || me?.is_super_admin)
  const emailInvalid = email.trim() !== '' && !EMAIL_PATTERN.test(email.trim())
  const canSubmit =
    name.trim() !== '' && EMAIL_PATTERN.test(email.trim()) && passwordProblem(password, confirm) === null && !saving
  const crumbs = [{ label: 'Users', to: '/admin/users' }, { label: 'New user' }]

  if (!isAdmin) {
    return (
      <Page title="New user" breadcrumbs={crumbs}>
        <NoPermission>You need global admin permission to create users.</NoPermission>
      </Page>
    )
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setActionError(null)
    try {
      const created = await createUser(name.trim(), email.trim(), password)
      if (makeAdmin) await updateUserFlags(created.id, true, false)
      navigate('/admin/users')
    } catch (err) {
      setActionError(errorMessage(err))
      setSaving(false)
    }
  }

  return (
    <Page title="New user" subtitle="Create an account that can sign in to this Nipa server." breadcrumbs={crumbs}>
      <ErrorBanner error={actionError} />
      <FormCard onSubmit={handleSubmit} maxWidth={640}>
        <FormControl required>
          <FormControl.Label>Full name</FormControl.Label>
          <TextInput block autoFocus placeholder="e.g. Dana Smith" value={name} onChange={(event) => setName(event.target.value)} />
          <FormControl.Caption>Shown on commits, merge requests and reviews.</FormControl.Caption>
        </FormControl>
        <FormControl required>
          <FormControl.Label>Email</FormControl.Label>
          <TextInput
            block
            type="email"
            placeholder="dana@example.com"
            value={email}
            validationStatus={emailInvalid ? 'error' : undefined}
            onChange={(event) => setEmail(event.target.value)}
          />
          {emailInvalid ? (
            <FormControl.Validation variant="error">Enter a valid email address.</FormControl.Validation>
          ) : (
            <FormControl.Caption>Used to sign in and for notifications.</FormControl.Caption>
          )}
        </FormControl>
        <PasswordFields
          password={password}
          confirm={confirm}
          onChange={(nextPassword, nextConfirm) => {
            setPassword(nextPassword)
            setConfirm(nextConfirm)
          }}
        />
        {me?.is_super_admin && (
          <FormControl>
            <Checkbox checked={makeAdmin} onChange={(event) => setMakeAdmin(event.target.checked)} />
            <FormControl.Label>Make this user an administrator</FormControl.Label>
            <FormControl.Caption>Admins can manage users and every organization on this server.</FormControl.Caption>
          </FormControl>
        )}
        <Stack direction="horizontal" gap="condensed">
          <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving}>
            Create user
          </Button>
          <Button onClick={() => navigate('/admin/users')} disabled={saving}>
            Cancel
          </Button>
        </Stack>
      </FormCard>
    </Page>
  )
}
