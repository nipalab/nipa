import { useState } from 'react'
import { FormControl, Textarea, TextInput } from '@primer/react'
import { addMySSHKey } from '../api/endpoints'
import { FormDialog } from './FormDialog'

export function AddSSHKeyDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [name, setName] = useState('')
  const [publicKey, setPublicKey] = useState('')

  return (
    <FormDialog
      title="Add SSH key"
      width="large"
      submitLabel="Add SSH key"
      submitDisabled={publicKey.trim() === ''}
      onClose={onClose}
      onSubmit={async () => {
        await addMySSHKey(name.trim(), publicKey.trim())
        onAdded()
      }}
    >
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
          Paste your OpenSSH public key (for example the contents of <code>~/.ssh/id_ed25519.pub</code>). Registered
          keys can log in without a password.
        </FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Name (optional)</FormControl.Label>
        <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
        <FormControl.Caption>Shown in this list; defaults to the comment at the end of the key.</FormControl.Caption>
      </FormControl>
    </FormDialog>
  )
}
