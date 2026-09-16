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
import { login } from '../api/client'
import type { LoginResponse } from '../api/models'

interface Props {
  onLogin: (tokens: LoginResponse) => void
}

export default function LoginPage({ onLogin }: Props) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const tokens = await login({ email, password })
      onLogin(tokens)
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
                type="email"
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