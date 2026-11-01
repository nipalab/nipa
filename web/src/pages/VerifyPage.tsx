import { useState } from 'react'
import { Button, Flash, FormControl, Heading, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { RepoIcon } from '@primer/octicons-react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, establishSession } from '../api/client'
import { resendVerification } from '../api/endpoints'
import { useAuth } from '../auth'

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
  const [resendBusy, setResendBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

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
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  async function handleResend(e: React.FormEvent) {
    e.preventDefault()
    if (!email.trim()) return
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

        {!linkInvalid && (
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

              {error && (
                <Flash variant="danger">
                  <strong>Confirmation failed.</strong> {error}
                </Flash>
              )}

              <Button type="submit" variant="primary" block disabled={busy}>
                {busy ? 'Confirming…' : 'Confirm and continue'}
              </Button>
            </Stack>
          </form>
        )}

        {linkInvalid && (
          <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
            <Flash variant="danger">
              <strong>Confirmation failed.</strong> {error ?? 'This confirmation link is missing its token.'}
            </Flash>
            {notice && <Flash variant="success">{notice}</Flash>}
            <form
              onSubmit={handleResend}
              style={{
                border: '1px solid var(--borderColor-default)',
                borderRadius: 6,
                padding: 24,
              }}
            >
              <Stack direction="vertical" gap="normal">
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
                <Button type="submit" block disabled={resendBusy}>
                  {resendBusy ? 'Sending…' : 'Resend confirmation email'}
                </Button>
              </Stack>
            </form>
            <PrimerLink as={Link} to="/login">
              Back to sign in
            </PrimerLink>
          </Stack>
        )}
      </Stack>
    </div>
  )
}
