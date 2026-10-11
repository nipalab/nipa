import { useState } from 'react'
import { Button, FormControl, ProgressBar, Stack, Text, TextInput } from '@primer/react'
import { Link } from 'react-router-dom'
import { getMyBilling, setOwnerQuota } from '../api/endpoints'
import { useAuth } from '../auth'
import { formatBytes } from '../components/StorageCard'
import { EmptyState, ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'

const CELL = { padding: '8px 12px' }
const HEADER_CELL = { ...CELL, fontWeight: 600, textAlign: 'left' as const, fontSize: 12, color: 'var(--fgColor-muted)' }

export default function BillingPage() {
  const { me } = useAuth()
  const { data, error, loading, reload } = useAsync(getMyBilling, [])
  const [actionError, setActionError] = useState<string | null>(null)

  const quota = data?.quota_bytes ?? null
  const percent = quota !== null && quota > 0 && data ? Math.min(100, Math.round((data.used_bytes / quota) * 100)) : 0

  return (
    <Page
      title="Billing"
      subtitle="Storage quota and usage for the organizations you own"
      actions={<Link to="/">back to home</Link>}
    >
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}

      {!loading && !error && data === null && (
        <EmptyState>Storage billing is not enabled on this server.</EmptyState>
      )}

      {data && (
        <>
          <Text as="p">
            {quota === null
              ? `${formatBytes(data.used_bytes)} used · unlimited quota`
              : `${formatBytes(data.used_bytes)} of ${formatBytes(quota)} used`}
          </Text>
          {quota !== null && (
            <div style={{ maxWidth: 480 }}>
              <ProgressBar progress={percent} />
            </div>
          )}
          <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
            Billing owner <Mono>{data.user_id}</Mono>
          </Text>

          <Text as="h3">Organizations</Text>
          {data.orgs.length === 0 ? (
            <EmptyState>You are not the billing owner of any organization.</EmptyState>
          ) : (
            <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ background: 'var(--bgColor-muted)' }}>
                    <th style={HEADER_CELL}>Organization</th>
                    <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Used</th>
                  </tr>
                </thead>
                <tbody>
                  {data.orgs.map((org) => (
                    <tr key={org.id} style={{ borderTop: '1px solid var(--borderColor-muted)' }}>
                      <td style={CELL}>
                        <Link to={`/${org.slug}`}>{org.name}</Link>
                      </td>
                      <td style={{ ...CELL, textAlign: 'right' }}>
                        <Mono>{formatBytes(org.used_bytes)}</Mono>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
            Need more storage? Contact your administrator to raise the quota.
          </Text>
        </>
      )}

      {me?.is_super_admin && <AdminQuotaCard onChanged={reload} onError={setActionError} />}
    </Page>
  )
}

function AdminQuotaCard({
  onChanged,
  onError,
}: {
  onChanged: () => void
  onError: (message: string) => void
}) {
  const [userId, setUserId] = useState('')
  const [quotaMiB, setQuotaMiB] = useState('')
  const [saving, setSaving] = useState(false)

  async function apply(quotaBytes: number | null) {
    if (!userId.trim()) return
    setSaving(true)
    try {
      await setOwnerQuota(userId.trim(), quotaBytes)
      setQuotaMiB('')
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  function handleSet(event: React.FormEvent) {
    event.preventDefault()
    const value = Number(quotaMiB)
    if (!Number.isFinite(value) || value <= 0) {
      onError('Enter the quota in MiB (a positive number).')
      return
    }
    void apply(Math.round(value * 1024 * 1024))
  }

  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}>
      <Text as="h3">Set owner quota (super admin)</Text>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        A cleared quota is unlimited; a quota applies to all organizations of the owner.
      </Text>
      <form onSubmit={handleSet}>
        <Stack direction="vertical" gap="normal" style={{ maxWidth: 480 }}>
          <FormControl required>
            <FormControl.Label>Owner user id</FormControl.Label>
            <TextInput block value={userId} onChange={(e) => setUserId(e.target.value)} placeholder="e.g. 73" />
          </FormControl>
          <FormControl>
            <FormControl.Label>Quota (MiB)</FormControl.Label>
            <TextInput
              block
              type="number"
              min="1"
              value={quotaMiB}
              onChange={(e) => setQuotaMiB(e.target.value)}
              placeholder="e.g. 1024"
            />
          </FormControl>
          <Stack direction="horizontal" gap="condensed">
            <Button variant="primary" type="submit" disabled={saving || !userId.trim()}>
              {saving ? 'Saving…' : 'Set quota'}
            </Button>
            <Button type="button" disabled={saving || !userId.trim()} onClick={() => void apply(null)}>
              Clear (unlimited)
            </Button>
          </Stack>
        </Stack>
      </form>
    </div>
  )
}
