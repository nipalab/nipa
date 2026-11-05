import { useState } from 'react'
import { Button, FormControl, TextInput } from '@primer/react'
import { useNavigate } from 'react-router-dom'
import { createUser } from '../api/endpoints'
import { useAuth } from '../auth'
import { FormCard } from '../components/SettingsCard'
import { ErrorBanner, NoPermission, Page } from '../components/ui'
import { errorMessage } from '../hooks'

export default function NewUserPage() {
  const { me } = useAuth()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const isAdmin = Boolean(me?.is_admin || me?.is_super_admin)
  const canSubmit = name.trim() !== '' && email.trim() !== '' && password !== '' && !saving
  const crumbs = [{ label: 'Users', to: '/admin/users' }, { label: 'New user' }]

  if (!isAdmin) {
    return (
      <Page title="New user" subtitle="Global administration" breadcrumbs={crumbs}>
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
      await createUser(name.trim(), email.trim(), password)
      navigate('/admin/users')
    } catch (err) {
      setActionError(errorMessage(err))
      setSaving(false)
    }
  }

  return (
    <Page title="New user" subtitle="Global administration" breadcrumbs={crumbs}>
      <ErrorBanner error={actionError} />
      <FormCard onSubmit={handleSubmit} maxWidth={560}>
        <FormControl required>
          <FormControl.Label>Name</FormControl.Label>
          <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
        </FormControl>
        <FormControl required>
          <FormControl.Label>Email</FormControl.Label>
          <TextInput block type="email" value={email} onChange={(event) => setEmail(event.target.value)} />
        </FormControl>
        <FormControl required>
          <FormControl.Label>Password</FormControl.Label>
          <TextInput block type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </FormControl>
        <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving} style={{ alignSelf: 'flex-start' }}>
          Create user
        </Button>
      </FormCard>
    </Page>
  )
}
