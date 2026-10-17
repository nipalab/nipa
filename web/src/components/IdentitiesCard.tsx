import { useState } from 'react'
import type { CSSProperties } from 'react'
import { Button, Flash, Heading, Stack, Text } from '@primer/react'
import { useSearchParams } from 'react-router-dom'
import {
  disconnectIdentity,
  getOnboardingCapabilities,
  listIdentities,
  startIdentityLink,
} from '../api/endpoints'
import type { IdentityResponse } from '../api/models'
import { useAsync } from '../hooks'
import { EmptyState, ErrorBanner, Loading } from './ui'

const ROW: CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  gap: 16,
  padding: '12px 0',
  borderTop: '1px solid var(--borderColor-muted)',
}

const PROVIDER_LABEL: Record<string, string> = {
  github: 'GitHub',
  google: 'Google',
}

const LINK_ERRORS: Record<string, string> = {
  email_conflict: 'An account with this email already exists.',
  email_required: 'The provider did not share a verified email address.',
  identity_taken: 'This provider account is already linked to another Nipa account.',
}

export function IdentitiesCard() {
  const { data: identities, error, loading, reload } = useAsync(listIdentities, [])
  const { data: capabilities } = useAsync(getOnboardingCapabilities, [])
  const [params, setParams] = useSearchParams()
  const [actionError, setActionError] = useState<string | null>(null)
  const [linking, setLinking] = useState<string | null>(null)
  const [disconnecting, setDisconnecting] = useState<string | null>(null)

  const linkedParam = params.get('oauth')
  const errorParam = params.get('oauth_error')
  const banner = linkedParam === 'linked'
    ? { variant: 'success' as const, text: 'Account connected.' }
    : errorParam
      ? { variant: 'danger' as const, text: LINK_ERRORS[errorParam] ?? `Connecting failed (${errorParam}).` }
      : null
  const connected = new Set((identities ?? []).map((identity) => identity.provider))
  const available = (capabilities?.providers ?? []).filter((provider) => !connected.has(provider))

  function dismissBanner() {
    const next = new URLSearchParams(params)
    next.delete('oauth')
    next.delete('oauth_error')
    setParams(next, { replace: true })
  }

  async function connect(provider: string) {
    setLinking(provider)
    setActionError(null)
    try {
      const { url } = await startIdentityLink(provider, '/settings/profile')
      window.location.href = url
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
      setLinking(null)
    }
  }

  async function disconnect(identity: IdentityResponse) {
    setDisconnecting(identity.id)
    setActionError(null)
    try {
      await disconnectIdentity(identity.id)
      reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setDisconnecting(null)
    }
  }

  if (loading || identities === null) {
    return loading ? <Loading /> : null
  }

  return (
    <Stack direction="vertical" gap="normal">
      <Heading as="h3" style={{ margin: 0, fontSize: 16 }}>
        Connected accounts
      </Heading>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Social accounts can sign in to your Nipa account. They are matched by verified email address.
      </Text>
      <ErrorBanner error={actionError ?? error} />
      {banner && (
        <Flash variant={banner.variant}>
          {banner.text}{' '}
          <Button size="small" variant="invisible" onClick={dismissBanner}>
            Dismiss
          </Button>
        </Flash>
      )}
      {identities.length === 0 ? (
        <EmptyState>No connected accounts.</EmptyState>
      ) : (
        <div>
          {identities.map((identity) => (
            <div key={identity.id} style={ROW}>
              <div>
                <Text as="p" style={{ margin: 0, fontWeight: 600 }}>
                  {PROVIDER_LABEL[identity.provider] ?? identity.provider}
                </Text>
                <Text as="p" style={{ margin: '2px 0 0', color: 'var(--fgColor-muted)', fontSize: 12 }}>
                  {identity.email || 'no email shared'} · connected{' '}
                  {new Date(identity.created_at).toLocaleDateString()}
                </Text>
              </div>
              <Button
                size="small"
                variant="danger"
                disabled={disconnecting === identity.id}
                onClick={() => void disconnect(identity)}
              >
                {disconnecting === identity.id ? 'Disconnecting…' : 'Disconnect'}
              </Button>
            </div>
          ))}
        </div>
      )}
      {available.length > 0 && (
        <Stack direction="horizontal" gap="condensed">
          {available.map((provider) => (
            <Button key={provider} disabled={linking === provider} onClick={() => void connect(provider)}>
              {linking === provider ? 'Redirecting…' : `Connect ${PROVIDER_LABEL[provider] ?? provider}`}
            </Button>
          ))}
        </Stack>
      )}
    </Stack>
  )
}
