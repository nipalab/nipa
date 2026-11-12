import { useEffect, useRef, useState } from 'react'
import { Link as PrimerLink, Spinner, Stack } from '@primer/react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, establishSession } from '../api/client'
import { useAuth } from '../auth'
import { AuthLayout } from '../components/AuthLayout'
import { ErrorBanner } from '../components/ui'
import { errorMessage } from '../hooks'
import { OAUTH_SIGNIN_ERRORS } from '../oauth'
import { APP_HOME } from '../routes'

const EXPIRED_CODE =
  'This sign-in link has expired, was already used or was opened in a different browser. Start the sign-in again from this browser.'

export default function OAuthCallbackPage() {
  const { signIn } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const code = params.get('code') ?? ''
  const isNew = params.get('new') === '1'
  const oauthError = params.get('oauth_error') ?? ''
  const fromParam = params.get('from') ?? APP_HOME
  // The destination rides inside the callback URL's query, so only same-origin
  // SPA paths are honored no matter what the browser sends.
  const from = fromParam.startsWith('/') && !fromParam.startsWith('//') ? fromParam : APP_HOME
  const [error, setError] = useState<string | null>(null)
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    if (oauthError) {
      setError(OAUTH_SIGNIN_ERRORS[oauthError] ?? `Sign-in failed (${oauthError}).`)
      return
    }
    if (!code) {
      setError('This sign-in link is missing its code.')
      return
    }
    establishSession('/api/v1/auth/oauth/complete', { code })
      .then(() => {
        signIn()
        navigate(isNew ? '/onboarding' : from, { replace: true })
      })
      .catch((err: unknown) => {
        setError(err instanceof ApiError && err.status === 404 ? EXPIRED_CODE : errorMessage(err))
      })
  }, [code, isNew, oauthError, from, signIn, navigate])

  return (
    <AuthLayout width={420}>
      {!error && (
        <Stack direction="vertical" align="center" gap="normal">
          <Spinner size="large" />
          <span>Completing sign-in…</span>
        </Stack>
      )}

      {error && (
        <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
          <ErrorBanner error={error} title="Sign-in failed" />
          <PrimerLink as={Link} to="/login">
            Back to sign in
          </PrimerLink>
        </Stack>
      )}
    </AuthLayout>
  )
}
