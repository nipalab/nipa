import { useState } from 'react'
import { Button, FormControl, ProgressBar, Text } from '@primer/react'
import { recountOrgStorage, setOrgBillingOwner } from '../api/endpoints'
import type { OrgStorageResponse } from '../api/models'
import { useAuth } from '../auth'
import { useAction } from '../hooks'
import { DataTable, type Column } from './DataTable'
import { FormDialog } from './FormDialog'
import { formatBytes } from './repo/format'
import { SettingsCard, SettingsRow } from './SettingsCard'
import { UserGroupPicker } from './UserGroupPicker'
import { ErrorBanner, Mono } from './ui'

type ProjectUsage = OrgStorageResponse['projects'][number]

export default function StorageCard({
  org,
  data,
  reload,
}: {
  org: string
  data: OrgStorageResponse
  reload: () => void
}) {
  const { me } = useAuth()
  const { run, busy, error, clearError } = useAction(reload)
  const [showTransfer, setShowTransfer] = useState(false)

  const quota = data.quota_bytes
  const percent = quota !== null && quota > 0 ? Math.min(100, Math.round((data.used_bytes / quota) * 100)) : 0

  const columns: Column<ProjectUsage>[] = [
    {
      id: 'project',
      header: 'Project',
      rowHeader: true,
      renderCell: (project) => (
        <>
          {project.name}
          {project.deleted && <Text style={{ color: 'var(--fgColor-muted)' }}> (deleted)</Text>}
        </>
      ),
    },
    { id: 'used', header: 'Used', align: 'end', renderCell: (project) => <Mono>{formatBytes(project.used_bytes)}</Mono> },
  ]

  return (
    <>
      <SettingsCard
        title="Storage"
        actions={
          me?.is_super_admin && (
            <div style={{ display: 'flex', gap: 'var(--base-size-8)' }}>
              <Button size="small" onClick={() => void run(() => recountOrgStorage(org))} disabled={busy}>
                {busy ? 'Recounting…' : 'Recount'}
              </Button>
              <Button size="small" onClick={() => setShowTransfer(true)}>
                Transfer billing owner
              </Button>
            </div>
          )
        }
      >
        <ErrorBanner error={error} onDismiss={clearError} />
        <SettingsRow label="Usage">
          <Text as="p" style={{ margin: 0 }}>
            {quota === null
              ? `${formatBytes(data.used_bytes)} used · unlimited quota`
              : `${formatBytes(data.used_bytes)} of ${formatBytes(quota)} used`}
          </Text>
          {quota !== null && (
            <div style={{ marginTop: 'var(--base-size-8)', maxWidth: 480 }}>
              <ProgressBar progress={percent} />
            </div>
          )}
        </SettingsRow>
        <SettingsRow label="Billed to">
          <Mono>{data.owner_id}</Mono>
        </SettingsRow>
      </SettingsCard>
      <DataTable label="Storage by project" data={data.projects} columns={columns} empty="No projects yet." />
      {showTransfer && <TransferOwnerDialog org={org} onClose={() => setShowTransfer(false)} onTransferred={reload} />}
    </>
  )
}

function TransferOwnerDialog({
  org,
  onClose,
  onTransferred,
}: {
  org: string
  onClose: () => void
  onTransferred: () => void
}) {
  const [userId, setUserId] = useState('')
  return (
    <FormDialog
      title="Transfer billing owner"
      submitLabel="Transfer"
      submitDisabled={!userId}
      onClose={onClose}
      onSubmit={async () => {
        await setOrgBillingOwner(org, userId)
        onTransferred()
      }}
    >
      <Text as="p">Usage stays with the organization; the new owner&apos;s quota covers it from now on.</Text>
      <FormControl required>
        <FormControl.Label>New owner</FormControl.Label>
        <UserGroupPicker
          org={org}
          kind="user"
          onSelect={(entry) => setUserId(entry.id)}
          placeholder="Search users by name or email…"
        />
      </FormControl>
    </FormDialog>
  )
}
