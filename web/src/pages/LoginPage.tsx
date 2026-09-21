import { useState } from 'react'
import {
  Banner,
  Button,
  FormControl,
  Heading,
  Spinner,
  Stack,
  TextInput,
} from '@primer/react'
import { RepoIcon, SignInIcon } from '@primer/octicons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { login } from '../api/client'
import { useAuth } from '../auth'

export default function LoginPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!email.trim() || !password) {
      setError('Enter your username/email and password.')
      return
    }
    setBusy(true)
    try {
      await login({ email: email.trim(), password })
      signIn()
      navigate(from, { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ maxWidth: 360, margin: '48px auto 0', padding: '0 16px' }}>
      <Stack direction="vertical" gap="spacious" align="center">
        <Heading as="h1">
          <RepoIcon size={24} verticalAlign="middle" /> Nipa
        </Heading>

        <form
          onSubmit={handleSubmit}
          noValidate
          style={{
            width: '100%',
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            padding: 24,
          }}
        >
          <Stack direction="vertical" gap="normal">
            <FormControl required>
              <FormControl.Label>Username/Email</FormControl.Label>
              <TextInput
                block
                type="text"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="username"
              />
            </FormControl>

            <FormControl required>
              <FormControl.Label>Password</FormControl.Label>
              <TextInput
                block
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
              />
            </FormControl>

            {error && <Banner variant="critical" title="Sign in failed">{error}</Banner>}

            <Button
              type="submit"
              variant="primary"
              block
              disabled={busy}
              trailingVisual={busy ? <Spinner size="small" /> : <SignInIcon />}
            >
              {busy ? 'Signing in…' : 'Sign in'}
            </Button>
          </Stack>
        </form>

        <p style={{ fontSize: 14, color: 'var(--fgColor-muted)' }}>
          Primer React (GitHub&apos;s design system) powers this UI.
        </p>
      </Stack>
    </div>
  )
}