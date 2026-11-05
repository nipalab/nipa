import { useState } from 'react'
import { Button, FormControl, ProgressBar, Stack, Text, TextInput } from '@primer/react'
import { Link } from 'react-router-dom'
import { getMyBilling, setOwnerQuota } from '../api/endpoints'
import type { OwnerOrgStorageResponse } from '../api/models'
import { useAuth } from '../auth'
import { DataTable, type Column } from '../components/DataTable'
import { formatBytes } from '../components/repo/format'
import { FormCard, SettingsCard, SettingsRow } from '../components/SettingsCard'
import { UserSettingsLayout } from '../components/UserSettingsLayout'
import { EmptyState, ErrorBanner, Loading, Mono, SectionHeading } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'

export default function BillingPage() {
  const { me } = useAuth()
  const { data, error, loading, reload } = useAsync(getMyBilling, [])
  const [actionError, setActionError] = useState<string | null>(null)

  const quota = data?.quota_bytes ?? null
  const percent = quota !== null && quota > 0 && data ? Math.min(100, Math.round((data.used_bytes / quota) * 100)) : 0

  const columns: Column<OwnerOrgStorageResponse>[] = [
    {
      id: 'org',
      header: 'Organization',
      rowHeader: true,
      renderCell: (org) => <Link to={`/${org.slug}`}>{org.name}</Link>,
    },
    { id: 'used', header: 'Used', align: 'end', renderCell: (org) => <Mono>{formatBytes(org.used_bytes)}</Mono> },
  ]

  return (
    <UserSettingsLayout active="billing">
      <Stack direction="vertical" gap="normal">
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? () => setActionError(null) : undefined} />
        {loading && <Loading />}

        {!loading && !error && data === null && <EmptyState>Storage billing is not enabled on this server.</EmptyState>}

        {data && (
          <>
            <SettingsCard title="Storage" description="Storage quota and usage for the organizations you own">
              <SettingsRow label="Usage">
                <Text as="p" style={{ margin: 0 }}>
                  {quota === null
                    ? `${formatBytes(data.used_bytes)} used · unlimited quota`
                    : `${formatBytes(data.used_bytes)} of ${formatBytes(quota)} used`}
                </Text>
                {quota !== null && (
                  <div style={{ maxWidth: 480, marginTop: 'var(--base-size-8)' }}>
                    <ProgressBar progress={percent} />
                  </div>
                )}
              </SettingsRow>
              <SettingsRow label="Billing owner">
                <Mono>{data.user_id}</Mono>
              </SettingsRow>
            </SettingsCard>

            <SectionHeading>Organizations</SectionHeading>
            <DataTable
              label="Organizations"
              data={data.orgs}
              columns={columns}
              empty="You are not the billing owner of any organization."
            />

            <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
              Need more storage? Contact your administrator to raise the quota.
            </Text>
          </>
        )}

        {me?.is_super_admin && <AdminQuotaCard onChanged={reload} onError={setActionError} />}
      </Stack>
    </UserSettingsLayout>
  )
}

function AdminQuotaCard({ onChanged, onError }: { onChanged: () => void; onError: (message: string) => void }) {
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
      onError(errorMessage(err))
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
    <FormCard onSubmit={handleSet} maxWidth={560}>
      <SectionHeading description="A cleared quota is unlimited; a quota applies to all organizations of the owner.">
        Set owner quota (super admin)
      </SectionHeading>
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
        <Button variant="primary" type="submit" loading={saving} disabled={saving || !userId.trim()}>
          Set quota
        </Button>
        <Button type="button" disabled={saving || !userId.trim()} onClick={() => void apply(null)}>
          Clear (unlimited)
        </Button>
      </Stack>
    </FormCard>
  )
}
