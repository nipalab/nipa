import { useState } from 'react'
import type { CSSProperties } from 'react'
import { Button, Heading, Stack, Text } from '@primer/react'
import { listMySSHKeys } from '../api/endpoints'
import type { SSHKeyResponse } from '../api/models'
import { useAsync } from '../hooks'
import { AddSSHKeyDialog } from './AddSSHKeyDialog'
import { DeleteSSHKeyDialog } from './DeleteSSHKeyDialog'
import { EmptyState, ErrorBanner, Loading, Mono } from './ui'

const ROW: CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  gap: 16,
  padding: '12px 0',
  borderTop: '1px solid var(--borderColor-muted)',
}

export function SSHKeysCard() {
  const { data, error, loading, reload } = useAsync(() => listMySSHKeys(), [])
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<SSHKeyResponse | null>(null)

  return (
    <Stack direction="vertical" gap="normal">
      <Heading as="h3" style={{ margin: 0, fontSize: 16 }}>
        SSH keys
      </Heading>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Registered public keys can log in without a password. Your private key never leaves your machine.
      </Text>
      <ErrorBanner error={error} />
      {loading ? (
        <Loading />
      ) : !data || data.length === 0 ? (
        <EmptyState>No SSH keys yet.</EmptyState>
      ) : (
        <div>
          {data.map((key) => (
            <div key={key.id} style={ROW}>
              <div>
                <Text as="p" style={{ margin: 0, fontWeight: 600 }}>
                  {key.name || key.comment || 'SSH key'}
                </Text>
                <Text as="p" style={{ margin: '2px 0 0', color: 'var(--fgColor-muted)', fontSize: 12 }}>
                  <Mono>{key.fingerprint}</Mono> · {key.key_type} · added{' '}
                  {new Date(key.created_at).toLocaleDateString()}
                </Text>
              </div>
              <Button size="small" variant="danger" onClick={() => setRemoving(key)}>
                Remove
              </Button>
            </div>
          ))}
        </div>
      )}
      <div>
        <Button onClick={() => setAdding(true)}>Add SSH key</Button>
      </div>
      {adding && (
        <AddSSHKeyDialog
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false)
            reload()
          }}
        />
      )}
      <DeleteSSHKeyDialog
        sshKey={removing}
        onClose={() => setRemoving(null)}
        onDeleted={() => {
          setRemoving(null)
          reload()
        }}
      />
    </Stack>
  )
}
