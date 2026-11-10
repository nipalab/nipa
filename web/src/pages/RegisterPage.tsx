import { useState } from 'react'
import { Banner, Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { PersonAddIcon } from '@primer/octicons-react'
import { Link, useLocation } from 'react-router-dom'
import { getOnboardingCapabilities, registerAccount } from '../api/endpoints'
import { AuthAside, AuthCard, AuthLayout } from '../components/AuthLayout'
import { SocialButtons } from '../components/SocialButtons'
import { PRIVACY_URL, TERMS_URL } from '../components/TermsCheckbox'
import { ErrorBanner } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'
import { useResendVerification } from '../useResendVerification'

export default function RegisterPage() {
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const { data: capabilities, loading: loadingCapabilities } = useAsync(getOnboardingCapabilities, [])
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [sentTo, setSentTo] = useState<string | null>(null)
  const { resend, busy: resendBusy, notice, error: resendError } = useResendVerification()
  const termsRequired = Boolean(capabilities?.terms)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!name.trim() || !email.trim()) {
      setError('Enter your name and email.')
      return
    }

    setBusy(true)
    try {
      await registerAccount({ name: name.trim(), email: email.trim(), accept_terms: termsRequired })
      setSentTo(email.trim())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthLayout title="Sign up for Nipa" footer={Boolean(capabilities)}>
      {!loadingCapabilities && capabilities === null && (
        <Banner variant="info" title="Registration is not enabled on this server.">
          <PrimerLink as={Link} to="/login">
            Sign in instead
          </PrimerLink>
        </Banner>
      )}

      {sentTo ? (
        <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
          <Banner variant="success" title="Check your email">
            We sent a confirmation link to <strong>{sentTo}</strong>. Open it to choose your password and activate
            your account.
          </Banner>
          <ErrorBanner error={resendError} />
          {notice && <Banner variant="success" title={notice} />}
          <Button block onClick={() => void resend(sentTo)} loading={resendBusy}>
            Resend confirmation email
          </Button>
          <PrimerLink as={Link} to="/login">
            Back to sign in
          </PrimerLink>
        </Stack>
      ) : (
        capabilities?.registration && (
          <AuthCard onSubmit={handleSubmit}>
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

            <p style={{ margin: 0, fontSize: 12, color: 'var(--fgColor-muted)' }}>
              You will choose your password when you confirm your email.
            </p>

            <ErrorBanner error={error} title="Registration failed" />

            <Button
              type="submit"
              variant="primary"
              block
              loading={busy}
              leadingVisual={busy ? undefined : <PersonAddIcon />}
            >
              {busy ? 'Creating account…' : 'Create account'}
            </Button>

            {capabilities.providers.length > 0 && (
              <>
                <div style={{ textAlign: 'center', color: 'var(--fgColor-muted)', fontSize: 12 }}>or</div>
                <SocialButtons providers={capabilities.providers} from={from} acceptTerms={termsRequired} />
              </>
            )}

            {termsRequired && (
              <p style={{ margin: 0, fontSize: 12, color: 'var(--fgColor-muted)' }}>
                By creating an account, you agree to the{' '}
                <PrimerLink href={TERMS_URL} target="_blank" rel="noopener">
                  Terms of Service
                </PrimerLink>
                . For more information about our privacy practices, see the{' '}
                <PrimerLink href={PRIVACY_URL} target="_blank" rel="noopener">
                  Privacy Policy
                </PrimerLink>
                .
              </p>
            )}
          </AuthCard>
        )
      )}

      {!sentTo && (
        <AuthAside>
          Already have an account?{' '}
          <PrimerLink as={Link} to="/login">
            Sign in
          </PrimerLink>
        </AuthAside>
      )}
    </AuthLayout>
  )
}
