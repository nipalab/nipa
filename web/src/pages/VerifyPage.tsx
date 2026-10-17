import { useEffect, useRef, useState } from 'react'
import { Button, Flash, FormControl, Heading, Link as PrimerLink, Spinner, Stack, TextInput } from '@primer/react'
import { RepoIcon } from '@primer/octicons-react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { establishSession } from '../api/client'
import { resendVerification } from '../api/endpoints'
import { useAuth } from '../auth'

export default function VerifyPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
  const [error, setError] = useState<string | null>(null)
  const [verifying, setVerifying] = useState(true)
  const [email, setEmail] = useState('')
  const [resendBusy, setResendBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    if (!token) {
      setError('This confirmation link is missing its token.')
      setVerifying(false)
      return
    }
    establishSession('/api/v1/auth/verify', { token })
      .then(() => {
        signIn()
        navigate('/onboarding', { replace: true })
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err))
        setVerifying(false)
      })
  }, [token, signIn, navigate])

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

        {verifying && (
          <Stack direction="vertical" align="center" gap="normal">
            <Spinner size="large" />
            <span>Confirming your email…</span>
          </Stack>
        )}

        {!verifying && error && (
          <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
            <Flash variant="danger">
              <strong>Confirmation failed.</strong> {error}
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
