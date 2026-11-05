import { useState } from 'react'
import { Button } from '@primer/react'
import { listMySSHKeys, removeMySSHKey } from '../api/endpoints'
import type { SSHKeyResponse } from '../api/models'
import { useAction, useAsync } from '../hooks'
import { AddSSHKeyDialog } from './AddSSHKeyDialog'
import { useConfirmAction } from './confirm'
import { Row } from './RowList'
import { SettingsCard } from './SettingsCard'
import { Timestamp } from './Timestamp'
import { EmptyState, ErrorBanner, Loading, Mono } from './ui'

export function SSHKeysCard() {
  const confirm = useConfirmAction()
  const { data, error, loading, reload } = useAsync(() => listMySSHKeys(), [])
  const { run, error: actionError, clearError } = useAction(reload)
  const [adding, setAdding] = useState(false)

  async function remove(key: SSHKeyResponse) {
    const ok = await confirm({
      title: 'Remove SSH key?',
      content: `${key.name || key.comment || 'This key'} will no longer be able to log in.`,
      confirmLabel: 'Remove key',
    })
    if (ok) await run(() => removeMySSHKey(key.id))
  }

  return (
    <SettingsCard
      title="SSH keys"
      description="Registered public keys can log in without a password. Your private key never leaves your machine."
      actions={<Button onClick={() => setAdding(true)}>Add SSH key</Button>}
    >
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      {loading ? (
        <Loading />
      ) : !data || data.length === 0 ? (
        <EmptyState>No SSH keys yet.</EmptyState>
      ) : (
        data.map((key, index) => (
          <Row
            key={key.id}
            first={index === 0}
            title={key.name || key.comment || 'SSH key'}
            meta={
              <>
                <Mono>{key.fingerprint}</Mono> · {key.key_type} · added <Timestamp value={key.created_at} />
              </>
            }
            actions={
              <Button size="small" variant="danger" onClick={() => void remove(key)}>
                Remove
              </Button>
            }
          />
        ))
      )}
      {adding && (
        <AddSSHKeyDialog
          onClose={() => setAdding(false)}
          onAdded={() => {
            reload()
          }}
        />
      )}
    </SettingsCard>
  )
}
