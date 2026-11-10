import { useState, type ReactNode } from 'react'
import { Button, Heading, Stack } from '@primer/react'
import { acceptTerms, getTermsStatus } from '../api/endpoints'
import { errorMessage, useAsync } from '../hooks'
import { TermsCheckbox } from './TermsCheckbox'
import { ErrorBanner, Loading } from './ui'

// TermsGate holds the app back until the signed-in user accepted the current
// terms. Servers that do not ask (free edition) answer 404 and pass through;
// any other failure keeps the app blocked behind a retry.
export function TermsGate({ children }: { children: ReactNode }) {
  const { data: status, loading, error: loadError, reload } = useAsync(getTermsStatus, [])
  const [accepted, setAccepted] = useState(false)
  const [checked, setChecked] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (loading) return <Loading />
  if (loadError) {
    return (
      <div style={{ maxWidth: 560, margin: '48px auto', padding: '0 16px' }}>
        <Stack direction="vertical" gap="normal">
          <ErrorBanner error={loadError} title="Could not check the terms you accepted" />
          <Button onClick={reload}>Retry</Button>
        </Stack>
      </div>
    )
  }
  if (!status?.required || accepted) return <>{children}</>

  async function handleAccept() {
    if (!status) return
    setBusy(true)
    setError(null)
    try {
      await acceptTerms(status.version)
      setAccepted(true)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ maxWidth: 560, margin: '48px auto', padding: '0 16px' }}>
      <Stack direction="vertical" gap="normal">
        <Heading as="h1" style={{ fontSize: 24 }}>
          Review our terms
        </Heading>
        <p style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
          Before you continue, please read and accept the Terms of Service and Privacy Policy.
        </p>
        <TermsCheckbox checked={checked} onChange={setChecked} />
        <ErrorBanner error={error} title="Could not save your acceptance" />
        <Button variant="primary" disabled={!checked} loading={busy} onClick={() => void handleAccept()}>
          Accept and continue
        </Button>
      </Stack>
    </div>
  )
}
