import { useState } from 'react'
import { Banner, Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, establishSession } from '../api/client'
import { useAuth } from '../auth'
import { AuthCard, AuthLayout } from '../components/AuthLayout'
import { ErrorBanner } from '../components/ui'
import { errorMessage } from '../hooks'
import { useResendVerification } from '../useResendVerification'

const MIN_PASSWORD_LENGTH = 8

export default function VerifyPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [linkInvalid, setLinkInvalid] = useState(!token)
  const [email, setEmail] = useState('')
  const { resend, busy: resendBusy, notice, error: resendError } = useResendVerification()

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (password.length < MIN_PASSWORD_LENGTH) {
      setError(`Choose a password of at least ${MIN_PASSWORD_LENGTH} characters.`)
      return
    }
    if (password !== confirm) {
      setError('The passwords do not match.')
      return
    }
    setBusy(true)
    try {
      await establishSession('/api/v1/auth/verify', { token, password })
      signIn()
      navigate('/onboarding', { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setLinkInvalid(true)
      }
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  async function handleResend(e: React.FormEvent) {
    e.preventDefault()
    await resend(email)
  }

  return (
    <AuthLayout>
      {!linkInvalid && (
        <AuthCard onSubmit={handleSubmit}>
          <p style={{ margin: 0 }}>Your email is confirmed once you choose a password.</p>

          <FormControl required>
            <FormControl.Label>Password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
            />
            <FormControl.Caption>At least {MIN_PASSWORD_LENGTH} characters.</FormControl.Caption>
          </FormControl>

          <FormControl required>
            <FormControl.Label>Confirm password</FormControl.Label>
            <TextInput
              block
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
            />
          </FormControl>

          <ErrorBanner error={error} title="Confirmation failed" />

          <Button type="submit" variant="primary" block loading={busy}>
            {busy ? 'Confirming…' : 'Confirm and continue'}
          </Button>
        </AuthCard>
      )}

      {linkInvalid && (
        <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
          <ErrorBanner
            error={error ?? 'This confirmation link is missing its token.'}
            title="Confirmation failed"
          />
          <ErrorBanner error={resendError} />
          {notice && <Banner variant="success" title={notice} />}
          <AuthCard onSubmit={handleResend} noValidate={false}>
            <FormControl>
              <FormControl.Label>Email</FormControl.Label>
              <TextInput
                block
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
              />
              <FormControl.Caption>Request a new confirmation link.</FormControl.Caption>
            </FormControl>
            <Button type="submit" block loading={resendBusy}>
              Resend confirmation email
            </Button>
          </AuthCard>
          <PrimerLink as={Link} to="/login">
            Back to sign in
          </PrimerLink>
        </Stack>
      )}
    </AuthLayout>
  )
}
