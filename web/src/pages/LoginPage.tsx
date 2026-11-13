import { useState } from 'react'
import { Banner, Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { SignInIcon } from '@primer/octicons-react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { login } from '../api/client'
import type { MFAChallengeResponse } from '../api/models'
import { getOnboardingCapabilities } from '../api/endpoints'
import { useAuth } from '../auth'
import { AuthAside, AuthCard, AuthLayout } from '../components/AuthLayout'
import { MFACodeStep } from '../components/MFACodeStep'
import { SocialButtons } from '../components/SocialButtons'
import { errorMessage, useAsync } from '../hooks'
import { useResendVerification } from '../useResendVerification'
import { APP_HOME } from '../routes'

export default function LoginPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? APP_HOME
  const { data: capabilities } = useAsync(getOnboardingCapabilities, [])
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [unconfirmed, setUnconfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [challenge, setChallenge] = useState<MFAChallengeResponse | null>(null)
  const [restartNotice, setRestartNotice] = useState<string | null>(null)
  const { resend, busy: resendBusy, notice, error: resendError, reset: resetResend } = useResendVerification()

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setUnconfirmed(false)
    setRestartNotice(null)
    resetResend()
    if (!email.trim() || !password) {
      setError('Enter your username/email and password.')
      return
    }
    setBusy(true)
    try {
      const pending = await login({ email: email.trim(), password })
      if (pending) {
        setChallenge(pending)
        return
      }
      finish()
    } catch (err) {
      const message = errorMessage(err)
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

  function finish() {
    signIn()
    navigate(from, { replace: true })
  }

  function restart(message: string | null) {
    setChallenge(null)
    setPassword('')
    setRestartNotice(message)
  }

  async function handleResend() {
    if (!email.trim()) {
      setError('Enter your email address first, then resend the confirmation email.')
      return
    }
    await resend(email)
  }

  const shownError = resendError ?? error

  if (challenge) {
    return (
      <AuthLayout title="Sign in to Nipa" footer={Boolean(capabilities)}>
        <MFACodeStep challenge={challenge} onVerified={finish} onRestart={restart} />
      </AuthLayout>
    )
  }

  return (
    <AuthLayout title="Sign in to Nipa" footer={Boolean(capabilities)}>
      <AuthCard onSubmit={handleSubmit}>
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

        {shownError && (
          <Banner variant={unconfirmed ? 'warning' : 'critical'} title="Sign in failed">
            <Stack direction="vertical" gap="condensed">
              <span>{shownError}</span>
              {unconfirmed && (
                <Button size="small" onClick={() => void handleResend()} loading={resendBusy}>
                  Resend confirmation email
                </Button>
              )}
            </Stack>
          </Banner>
        )}
        {notice && <Banner variant="success" title={notice} />}
        {restartNotice && <Banner variant="warning" title="Sign in again">{restartNotice}</Banner>}

        <Button type="submit" variant="primary" block loading={busy} trailingVisual={<SignInIcon />}>
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>

        {capabilities && capabilities.providers.length > 0 && (
          <>
            <div style={{ textAlign: 'center', color: 'var(--fgColor-muted)', fontSize: 12 }}>or</div>
            <SocialButtons providers={capabilities.providers} from={from} />
          </>
        )}
      </AuthCard>

      {capabilities?.registration && (
        <AuthAside>
          New to Nipa?{' '}
          <PrimerLink as={Link} to="/register" state={{ from }}>
            Create an account
          </PrimerLink>
        </AuthAside>
      )}
    </AuthLayout>
  )
}
