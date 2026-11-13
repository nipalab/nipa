import { useRef, useState, type FormEvent } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, TextInput } from '@primer/react'
import { ShieldLockIcon } from '@primer/octicons-react'
import { verifyMFA } from '../api/client'
import type { MFAChallengeResponse } from '../api/models'
import { errorMessage } from '../hooks'
import { AuthCard } from './AuthLayout'
import { ErrorBanner } from './ui'

export const MFA_MAX_ATTEMPTS = 5

export const MFA_RESTART_MESSAGE = 'Your sign-in expired or had too many wrong codes. Sign in again.'

export function normalizeCode(code: string): string {
  return code.replace(/\s+/g, '')
}

export function MFACodeStep({
  challenge,
  onVerified,
  onRestart,
  restartLabel = 'Back to sign in',
}: {
  challenge: MFAChallengeResponse
  onVerified: () => void
  onRestart: (message: string | null) => void
  restartLabel?: string
}) {
  const deadline = useRef(Date.now() + challenge.expires_in * 1000)
  const failures = useRef(0)
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const value = normalizeCode(code)
    if (value === '' || busy) return
    if (Date.now() >= deadline.current) {
      onRestart(MFA_RESTART_MESSAGE)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await verifyMFA(challenge.mfa_challenge, value)
      onVerified()
    } catch (err) {
      setBusy(false)
      const invalid = errorMessage(err) === 'invalid code'
      if (invalid) failures.current += 1
      if (failures.current >= MFA_MAX_ATTEMPTS || Date.now() >= deadline.current) {
        onRestart(MFA_RESTART_MESSAGE)
        return
      }
      setCode('')
      setError(invalid ? 'Invalid code. Try again.' : errorMessage(err))
    }
  }

  return (
    <AuthCard onSubmit={(event) => void submit(event)}>
      <Stack direction="horizontal" align="center" gap="condensed">
        <ShieldLockIcon size={24} />
        <Text as="h2" style={{ margin: 0, fontSize: 16 }}>
          Two-factor authentication
        </Text>
      </Stack>
      <FormControl required>
        <FormControl.Label>Authentication code</FormControl.Label>
        <TextInput
          block
          autoFocus
          name="otp"
          value={code}
          onChange={(event) => setCode(event.target.value)}
          autoComplete="one-time-code"
          placeholder="123456"
          spellCheck={false}
        />
        <FormControl.Caption>
          Enter the 6-digit code from your authenticator app, or one of your recovery codes (XXXXX-XXXXX).
        </FormControl.Caption>
      </FormControl>
      <ErrorBanner error={error} title="Verification failed" />
      <Button type="submit" variant="primary" block loading={busy} disabled={normalizeCode(code) === ''}>
        Verify
      </Button>
      <PrimerLink as="button" type="button" onClick={() => onRestart(null)}>
        {restartLabel}
      </PrimerLink>
    </AuthCard>
  )
}
