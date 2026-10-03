import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, TextInput } from '@primer/react'
import { Link, useNavigate } from 'react-router-dom'
import { createUser } from '../api/endpoints'
import { useAuth } from '../auth'
import { ErrorBanner, Page } from '../components/ui'

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
  const backLink = (
    <PrimerLink as={Link} to="/admin/users">
      back to users
    </PrimerLink>
  )

  if (!isAdmin) {
    return (
      <Page title="New user" subtitle="Global administration">
        <Text>You need global admin permission to create users.</Text>
        {backLink}
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
      setActionError(err instanceof Error ? err.message : String(err))
      setSaving(false)
    }
  }

  return (
    <Page title="New user" subtitle="Global administration" actions={backLink}>
      <ErrorBanner error={actionError} />
      <form
        onSubmit={handleSubmit}
        style={{
          border: '1px solid var(--borderColor-default)',
          borderRadius: 6,
          padding: 16,
          maxWidth: 560,
        }}
      >
        <Stack direction="vertical" gap="normal">
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Email</FormControl.Label>
            <TextInput
              block
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
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
          <Button
            type="submit"
            variant="primary"
            disabled={!canSubmit}
            loading={saving}
            style={{ alignSelf: 'flex-start' }}
          >
            Create user
          </Button>
        </Stack>
      </form>
    </Page>
  )
}
