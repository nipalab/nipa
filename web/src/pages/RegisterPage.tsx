import { useState } from 'react'
import {
  Button,
  Flash,
  FormControl,
  Heading,
  Link as PrimerLink,
  Stack,
  TextInput,
} from '@primer/react'
import { PersonAddIcon, RepoIcon } from '@primer/octicons-react'
import { Link, useLocation } from 'react-router-dom'
import { getOnboardingCapabilities, registerAccount, resendVerification } from '../api/endpoints'
import { SocialButtons } from '../components/SocialButtons'
import { ErrorBanner } from '../components/ui'
import { useAsync } from '../hooks'

export default function RegisterPage() {
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const { data: capabilities, loading: loadingCapabilities } = useAsync(getOnboardingCapabilities, [])
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [resendBusy, setResendBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!name.trim() || !email.trim() || password.length < 8) {
      setError('Enter your name, email and a password of at least 8 characters.')
      return
    }
    setBusy(true)
    try {
      await registerAccount({ name: name.trim(), email: email.trim(), password })
      setSentTo(email.trim())
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleResend() {
    if (!sentTo) return
    setResendBusy(true)
    setNotice(null)
    setError(null)
    try {
      await resendVerification(sentTo)
      setNotice('If the account is unconfirmed, a new email is on its way.')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setResendBusy(false)
    }
  }

  return (
    <div style={{ maxWidth: 360, margin: '48px auto 0', padding: '0 16px' }}>
      <Stack direction="vertical" gap="spacious" align="center">
        <Heading as="h1">
          <RepoIcon size={24} verticalAlign="middle" /> Nipa
        </Heading>

        {!loadingCapabilities && capabilities === null && (
          <Flash>
            Registration is not enabled on this server.{' '}
            <PrimerLink as={Link} to="/login">
              Sign in instead
            </PrimerLink>
          </Flash>
        )}

        {sentTo ? (
          <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
            <Flash variant="success">
              We sent a confirmation link to <strong>{sentTo}</strong>. Open it to activate your account.
            </Flash>
            <ErrorBanner error={error} />
            {notice && <Flash variant="success">{notice}</Flash>}
            <Button block onClick={() => void handleResend()} disabled={resendBusy}>
              {resendBusy ? 'Sending…' : 'Resend confirmation email'}
            </Button>
            <PrimerLink as={Link} to="/login">
              Back to sign in
            </PrimerLink>
          </Stack>
        ) : (
          capabilities?.registration && (
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
                  <FormControl.Label>Name</FormControl.Label>
                  <TextInput block value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" />
                </FormControl>

                <FormControl required>
                  <FormControl.Label>Email</FormControl.Label>
                  <TextInput
                    block
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    autoComplete="email"
                  />
                </FormControl>

                <FormControl required>
                  <FormControl.Label>Password</FormControl.Label>
                  <TextInput
                    block
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="new-password"
                  />
                  <FormControl.Caption>At least 8 characters.</FormControl.Caption>
                </FormControl>

                {error && (
                  <Flash variant="danger">
                    <strong>Registration failed.</strong> {error}
                  </Flash>
                )}

                <Button
                  type="submit"
                  variant="primary"
                  block
                  disabled={busy}
                  leadingVisual={busy ? undefined : <PersonAddIcon />}
                >
                  {busy ? 'Creating account…' : 'Create account'}
                </Button>

                {capabilities.providers.length > 0 && (
                  <>
                    <div style={{ textAlign: 'center', color: 'var(--fgColor-muted)', fontSize: 12 }}>or</div>
                    <SocialButtons providers={capabilities.providers} redirect={from} />
                  </>
                )}
              </Stack>
            </form>
          )
        )}

        {!sentTo && (
          <p style={{ fontSize: 14, color: 'var(--fgColor-muted)' }}>
            Already have an account?{' '}
            <PrimerLink as={Link} to="/login">
              Sign in
            </PrimerLink>
          </p>
        )}
      </Stack>
    </div>
  )
}
