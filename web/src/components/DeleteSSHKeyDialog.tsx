import { useEffect, useState } from 'react'
import { Banner, Button, Dialog, Stack, Text } from '@primer/react'
import { removeMySSHKey } from '../api/endpoints'
import type { SSHKeyResponse } from '../api/models'
import { Mono } from './ui'

export function DeleteSSHKeyDialog({
  sshKey,
  onClose,
  onDeleted,
}: {
  sshKey: SSHKeyResponse | null
  onClose: () => void
  onDeleted: () => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setError(null)
    setLoading(false)
  }, [sshKey])

  if (!sshKey) return null

  const current = sshKey

  async function handleDelete() {
    if (loading) return
    setLoading(true)
    setError(null)
    try {
      await removeMySSHKey(current.id)
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setLoading(false)
    }
  }

  return (
    <Dialog title="Remove SSH key" onClose={onClose} width="medium">
      <Dialog.Body>
        <Stack direction="vertical" gap="normal">
          {error && (
            <Banner variant="critical" title="Remove failed">
              {error}
            </Banner>
          )}
          <Text as="p">
            This removes <strong>{current.name || current.comment || 'this key'}</strong> (
            <Mono>{current.fingerprint}</Mono>). You can no longer log in with this key.
          </Text>
        </Stack>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button variant="danger" loading={loading} onClick={() => void handleDelete()}>
          Remove key
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
