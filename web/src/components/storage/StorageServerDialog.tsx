import { useState } from 'react'
import { FormControl, TextInput } from '@primer/react'
import { createStorageServer, updateStorageServer } from '../../api/endpoints'
import type { StorageServerResponse, UpdateStorageServerInput } from '../../api/models'
import { errorMessage } from '../../hooks'
import { FormDialog } from '../FormDialog'
import { isOwnStorageError, OwnStorageBanner, probeCheckOf, ProbeFailureBanner } from './storage'

type Failure = { kind: 'own' } | { kind: 'probe'; check: string; detail: string }

export function StorageServerDialog({
  server,
  onClose,
  onSaved,
}: {
  server?: StorageServerResponse
  onClose: () => void
  onSaved: () => void
}) {
  const editing = server !== undefined
  const [name, setName] = useState(server?.name ?? '')
  const [endpoint, setEndpoint] = useState(server?.endpoint ?? '')
  const [region, setRegion] = useState(server?.region ?? '')
  const [bucket, setBucket] = useState(server?.bucket ?? '')
  const [prefix, setPrefix] = useState(server?.prefix ?? '')
  const [accessKeyID, setAccessKeyID] = useState(server?.access_key_id ?? '')
  const [secret, setSecret] = useState('')
  const [failure, setFailure] = useState<Failure | null>(null)

  const ready =
    name.trim() !== '' &&
    endpoint.trim() !== '' &&
    accessKeyID.trim() !== '' &&
    (editing || (bucket.trim() !== '' && secret !== ''))

  async function save() {
    if (!server) {
      await createStorageServer({
        name: name.trim(),
        endpoint: endpoint.trim(),
        region: region.trim(),
        bucket: bucket.trim(),
        prefix: prefix.trim(),
        access_key_id: accessKeyID.trim(),
        secret_access_key: secret,
      })
      return
    }
    const patch: UpdateStorageServerInput = {}
    if (name.trim() !== server.name) patch.name = name.trim()
    if (endpoint.trim() !== server.endpoint) patch.endpoint = endpoint.trim()
    if (region.trim() !== server.region) patch.region = region.trim()
    if (accessKeyID.trim() !== server.access_key_id) patch.access_key_id = accessKeyID.trim()
    if (secret !== '') patch.secret_access_key = secret
    if (Object.keys(patch).length > 0) await updateStorageServer(server.id, patch)
  }

  return (
    <FormDialog
      title={editing ? `Edit ${server.name}` : 'Add storage server'}
      width="large"
      submitLabel={editing ? 'Save' : 'Add storage server'}
      submitDisabled={!ready}
      onClose={onClose}
      onSubmit={async () => {
        setFailure(null)
        try {
          await save()
        } catch (err) {
          const check = probeCheckOf(err)
          if (isOwnStorageError(err)) setFailure({ kind: 'own' })
          else if (check) setFailure({ kind: 'probe', check, detail: errorMessage(err) })
          else throw err
          return false
        }
        onSaved()
      }}
    >
      {failure?.kind === 'own' && <OwnStorageBanner />}
      {failure?.kind === 'probe' && <ProbeFailureBanner check={failure.check} detail={failure.detail} />}
      <FormControl required>
        <FormControl.Label>Name</FormControl.Label>
        <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
      </FormControl>
      <FormControl required>
        <FormControl.Label>Endpoint</FormControl.Label>
        <TextInput
          block
          placeholder="https://s3.eu-west-1.amazonaws.com"
          value={endpoint}
          onChange={(event) => setEndpoint(event.target.value)}
        />
        <FormControl.Caption>
          A public HTTPS endpoint Nipa and your team&apos;s clients can reach. For Cloudflare R2 use
          https://&lt;account&gt;.r2.cloudflarestorage.com.
        </FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Region</FormControl.Label>
        <TextInput block placeholder="us-east-1" value={region} onChange={(event) => setRegion(event.target.value)} />
        <FormControl.Caption>Use auto for Cloudflare R2.</FormControl.Caption>
      </FormControl>
      <FormControl required={!editing} disabled={editing}>
        <FormControl.Label>Bucket</FormControl.Label>
        <TextInput block value={bucket} onChange={(event) => setBucket(event.target.value)} />
        {editing && <FormControl.Caption>The bucket cannot be changed once saved.</FormControl.Caption>}
      </FormControl>
      <FormControl disabled={editing}>
        <FormControl.Label>Prefix</FormControl.Label>
        <TextInput block placeholder="nipa" value={prefix} onChange={(event) => setPrefix(event.target.value)} />
        <FormControl.Caption>
          {editing ? 'The prefix cannot be changed once saved.' : 'Optional folder inside the bucket. It cannot be changed later.'}
        </FormControl.Caption>
      </FormControl>
      <FormControl required>
        <FormControl.Label>Access key ID</FormControl.Label>
        <TextInput block value={accessKeyID} onChange={(event) => setAccessKeyID(event.target.value)} />
      </FormControl>
      <FormControl required={!editing}>
        <FormControl.Label>Secret access key</FormControl.Label>
        <TextInput
          block
          type="password"
          autoComplete="new-password"
          placeholder={editing ? 'Leave empty to keep the current key' : undefined}
          value={secret}
          onChange={(event) => setSecret(event.target.value)}
        />
        <FormControl.Caption>
          Stored encrypted and never shown again. The key needs s3:ListBucket, s3:GetObject, s3:PutObject and
          s3:DeleteObject on the bucket and prefix.
        </FormControl.Caption>
      </FormControl>
    </FormDialog>
  )
}
