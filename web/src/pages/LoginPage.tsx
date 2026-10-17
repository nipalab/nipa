import { useState } from 'react'
import {
  Banner,
  Button,
  Flash,
  FormControl,
  Heading,
  Link as PrimerLink,
  Spinner,
  Stack,
  TextInput,
} from '@primer/react'
import { RepoIcon, SignInIcon } from '@primer/octicons-react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { login } from '../api/client'
import { getOnboardingCapabilities, resendVerification } from '../api/endpoints'
import { useAuth } from '../auth'
import { SocialButtons } from '../components/SocialButtons'
import { useAsync } from '../hooks'

export default function LoginPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const { data: capabilities } = useAsync(getOnboardingCapabilities, [])
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [unconfirmed, setUnconfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [resendBusy, setResendBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setNotice(null)
    setUnconfirmed(false)
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
      const message = err instanceof Error ? err.message : String(err)
      if (message === 'email not confirmed') {
        setUnconfirmed(true)
        setError('Your email address has not been confirmed yet. Open the confirmation link we sent you.')
      } else {
        setError(message)
      }
    } finally {
      setBusy(false)
    }
  }

  async function handleResend() {
    if (!email.trim()) {
      setError('Enter your email address first, then resend the confirmation email.')
      return
    }
    setResendBusy(true)
    setNotice(null)
    try {
      await resendVerification(email.trim())
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

            {error && (
              <Banner variant={unconfirmed ? 'warning' : 'critical'} title="Sign in failed">
                <Stack direction="vertical" gap="condensed">
                  <span>{error}</span>
                  {unconfirmed && (
                    <Button size="small" onClick={() => void handleResend()} disabled={resendBusy}>
                      {resendBusy ? 'Sending…' : 'Resend confirmation email'}
                    </Button>
                  )}
                </Stack>
              </Banner>
            )}
            {notice && <Flash variant="success">{notice}</Flash>}

            <Button
              type="submit"
              variant="primary"
              block
              disabled={busy}
              trailingVisual={busy ? <Spinner size="small" /> : <SignInIcon />}
            >
              {busy ? 'Signing in…' : 'Sign in'}
            </Button>

            {capabilities && capabilities.providers.length > 0 && (
              <>
                <div style={{ textAlign: 'center', color: 'var(--fgColor-muted)', fontSize: 12 }}>or</div>
                <SocialButtons providers={capabilities.providers} from={from} />
              </>
            )}
          </Stack>
        </form>

        {capabilities?.registration && (
          <p style={{ fontSize: 14, color: 'var(--fgColor-muted)', margin: 0 }}>
            New to Nipa?{' '}
            <PrimerLink as={Link} to="/register" state={{ from }}>
              Create an account
            </PrimerLink>
          </p>
        )}
      </Stack>
    </div>
  )
}
