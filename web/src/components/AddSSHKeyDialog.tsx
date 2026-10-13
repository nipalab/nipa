import { useState } from 'react'
import { Button, Dialog, FormControl, Stack, Textarea, TextInput } from '@primer/react'
import { addMySSHKey } from '../api/endpoints'
import { ErrorBanner } from './ui'

export function AddSSHKeyDialog({
  onClose,
  onAdded,
}: {
  onClose: () => void
  onAdded: () => void
}) {
  const [name, setName] = useState('')
  const [publicKey, setPublicKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const canSubmit = publicKey.trim() !== '' && !loading

  async function handleAdd() {
    if (!canSubmit) return
    setLoading(true)
    setError(null)
    try {
      await addMySSHKey(name.trim(), publicKey.trim())
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setLoading(false)
    }
  }

  return (
    <Dialog title="Add SSH key" onClose={onClose} width="large">
      <Dialog.Body>
        <Stack direction="vertical" gap="normal">
          <ErrorBanner error={error} />
          <FormControl required>
            <FormControl.Label>Public key</FormControl.Label>
            <Textarea
              block
              rows={4}
              autoFocus
              placeholder="ssh-ed25519 AAAAC3Nza… alice@laptop"
              value={publicKey}
              onChange={(event) => setPublicKey(event.target.value)}
            />
            <FormControl.Caption>
              Paste your OpenSSH public key (for example the contents of <code>~/.ssh/id_ed25519.pub</code>).
              Registered keys can log in without a password.
            </FormControl.Caption>
          </FormControl>
          <FormControl>
            <FormControl.Label>Name (optional)</FormControl.Label>
            <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
            <FormControl.Caption>Shown in this list; defaults to the comment at the end of the key.</FormControl.Caption>
          </FormControl>
        </Stack>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button variant="primary" disabled={!canSubmit} loading={loading} onClick={() => void handleAdd()}>
          Add SSH key
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
