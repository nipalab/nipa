import { useState } from 'react'
import { Banner, Button, Stack } from '@primer/react'
import { useSearchParams } from 'react-router-dom'
import { disconnectIdentity, getOnboardingCapabilities, listIdentities, startIdentityLink } from '../api/endpoints'
import type { IdentityResponse } from '../api/models'
import { errorMessage, useAction, useAsync } from '../hooks'
import { OAUTH_LINK_ERRORS, providerLabel } from '../oauth'
import { useConfirmAction } from './confirm'
import { Row } from './RowList'
import { SettingsCard } from './SettingsCard'
import { Timestamp } from './Timestamp'
import { EmptyState, ErrorBanner, Loading } from './ui'

export function IdentitiesCard() {
  const confirm = useConfirmAction()
  const { data: identities, error, loading, reload } = useAsync(listIdentities, [])
  const { data: capabilities } = useAsync(getOnboardingCapabilities, [])
  const { run, error: actionError, clearError } = useAction(reload)
  const [params, setParams] = useSearchParams()
  const [linkError, setLinkError] = useState<string | null>(null)
  const [linking, setLinking] = useState<string | null>(null)

  const linkedParam = params.get('oauth')
  const errorParam = params.get('oauth_error')
  const banner =
    linkedParam === 'linked'
      ? { variant: 'success' as const, text: 'Account connected.' }
      : errorParam
        ? { variant: 'critical' as const, text: OAUTH_LINK_ERRORS[errorParam] ?? `Connecting failed (${errorParam}).` }
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
    setLinkError(null)
    try {
      const { url } = await startIdentityLink(provider, '/settings/profile?tab=accounts')
      window.location.href = url
    } catch (err) {
      setLinkError(errorMessage(err))
      setLinking(null)
    }
  }

  async function disconnect(identity: IdentityResponse) {
    const ok = await confirm({
      title: `Disconnect ${providerLabel(identity.provider)}?`,
      content: 'You will no longer be able to sign in with this account.',
      confirmLabel: 'Disconnect',
    })
    if (ok) await run(() => disconnectIdentity(identity.id))
  }

  if (loading) return <Loading />
  if (identities === null) return error ? <ErrorBanner error={error} /> : null

  return (
    <SettingsCard
      title="Connected accounts"
      description="Social accounts can sign in to your Nipa account. They are matched by verified email address."
      footer={
        available.length > 0 ? (
          <Stack direction="horizontal" gap="condensed">
            {available.map((provider) => (
              <Button key={provider} disabled={linking === provider} onClick={() => void connect(provider)}>
                {linking === provider ? 'Redirecting…' : `Connect ${providerLabel(provider)}`}
              </Button>
            ))}
          </Stack>
        ) : undefined
      }
    >
      <ErrorBanner error={linkError ?? actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      {banner && (
        <Banner variant={banner.variant} title={banner.text} onDismiss={dismissBanner} />
      )}
      {identities.length === 0 ? (
        <EmptyState>No connected accounts.</EmptyState>
      ) : (
        identities.map((identity, index) => (
          <Row
            key={identity.id}
            first={index === 0}
            title={providerLabel(identity.provider)}
            meta={
              <>
                {identity.email || 'no email shared'} · connected <Timestamp value={identity.created_at} />
              </>
            }
            actions={
              <Button size="small" variant="danger" onClick={() => void disconnect(identity)}>
                Disconnect
              </Button>
            }
          />
        ))
      )}
    </SettingsCard>
  )
}
