import { useState } from 'react'
import { Button, Dialog, FormControl, ProgressBar, Stack, Text } from '@primer/react'
import { getOrgStorage, recountOrgStorage, setOrgBillingOwner } from '../api/endpoints'
import { useAuth } from '../auth'
import { useAsync } from '../hooks'
import { UserGroupPicker } from './UserGroupPicker'
import { EmptyState, Mono } from './ui'

const CELL = { padding: '8px 12px' }
const HEADER_CELL = { ...CELL, fontWeight: 600, textAlign: 'left' as const, fontSize: 12, color: 'var(--fgColor-muted)' }

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`
}

export default function StorageCard({ org, onError }: { org: string; onError: (message: string) => void }) {
  const { me } = useAuth()
  const { data, loading, reload } = useAsync(() => getOrgStorage(org), [org])
  const [showTransfer, setShowTransfer] = useState(false)
  const [recounting, setRecounting] = useState(false)

  if (loading || data === null) return null

  const quota = data.quota_bytes
  const percent = quota !== null && quota > 0 ? Math.min(100, Math.round((data.used_bytes / quota) * 100)) : 0

  async function handleRecount() {
    setRecounting(true)
    try {
      await recountOrgStorage(org)
      reload()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setRecounting(false)
    }
  }

  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Storage</Text>
        {me?.is_super_admin && (
          <Stack direction="horizontal" gap="condensed">
            <Button size="small" onClick={handleRecount} disabled={recounting}>
              {recounting ? 'Recounting…' : 'Recount'}
            </Button>
            <Button size="small" onClick={() => setShowTransfer(true)}>
              Transfer billing owner
            </Button>
          </Stack>
        )}
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        {quota === null
          ? `${formatBytes(data.used_bytes)} used · unlimited quota`
          : `${formatBytes(data.used_bytes)} of ${formatBytes(quota)} used`}
      </Text>
      {quota !== null && (
        <div style={{ marginBottom: 8 }}>
          <ProgressBar progress={percent} />
        </div>
      )}
      <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
        Billed to user <Mono>{data.owner_id}</Mono>
      </Text>
      {data.projects.length === 0 ? (
        <EmptyState>No projects yet.</EmptyState>
      ) : (
        <div
          style={{
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            overflow: 'hidden',
            marginTop: 8,
          }}
        >
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)' }}>
                <th style={HEADER_CELL}>Project</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Used</th>
              </tr>
            </thead>
            <tbody>
              {data.projects.map((project) => (
                <tr key={project.id} style={{ borderTop: '1px solid var(--borderColor-muted)' }}>
                  <td style={CELL}>
                    {project.name}
                    {project.deleted && (
                      <Text style={{ color: 'var(--fgColor-muted)' }}> (deleted)</Text>
                    )}
                  </td>
                  <td style={{ ...CELL, textAlign: 'right' }}>
                    <Mono>{formatBytes(project.used_bytes)}</Mono>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showTransfer && (
        <TransferOwnerDialog
          org={org}
          onClose={() => setShowTransfer(false)}
          onTransferred={() => {
            setShowTransfer(false)
            reload()
          }}
          onError={onError}
        />
      )}
    </div>
  )
}

function TransferOwnerDialog({
  org,
  onClose,
  onTransferred,
  onError,
}: {
  org: string
  onClose: () => void
  onTransferred: () => void
  onError: (message: string) => void
}) {
  const [userId, setUserId] = useState('')
  const [saving, setSaving] = useState(false)

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!userId) return
    setSaving(true)
    try {
      await setOrgBillingOwner(org, userId)
      onTransferred()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog title="Transfer billing owner" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Usage stays with the organization; the new owner's quota covers it from now on.
        </Text>
        <form onSubmit={handleSubmit}>
          <FormControl required>
            <FormControl.Label>New owner</FormControl.Label>
            <UserGroupPicker
              org={org}
              kind="user"
              onSelect={(entry) => setUserId(entry.id)}
              placeholder="Search users by name or email…"
            />
          </FormControl>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!userId || saving} onClick={handleSubmit}>
          {saving ? 'Transferring…' : 'Transfer'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
