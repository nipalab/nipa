import { useEffect, useRef, useState } from 'react'
import { Flash, Heading, Link as PrimerLink, Spinner, Stack } from '@primer/react'
import { RepoIcon } from '@primer/octicons-react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { establishSession } from '../api/client'
import { useAuth } from '../auth'
import { OAUTH_SIGNIN_ERRORS } from '../oauth'
import { APP_HOME } from '../routes'

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
        setError(err instanceof Error ? err.message : String(err))
      })
  }, [code, isNew, oauthError, from, signIn, navigate])

  return (
    <div style={{ maxWidth: 420, margin: '48px auto 0', padding: '0 16px' }}>
      <Stack direction="vertical" gap="spacious" align="center">
        <Heading as="h1">
          <RepoIcon size={24} verticalAlign="middle" /> Nipa
        </Heading>

        {!error && (
          <Stack direction="vertical" align="center" gap="normal">
            <Spinner size="large" />
            <span>Completing sign-in…</span>
          </Stack>
        )}

        {error && (
          <Stack direction="vertical" gap="normal" style={{ width: '100%' }}>
            <Flash variant="danger">
              <strong>Sign-in failed.</strong> {error}
            </Flash>
            <PrimerLink as={Link} to="/login">
              Back to sign in
            </PrimerLink>
          </Stack>
        )}
      </Stack>
    </div>
  )
}
