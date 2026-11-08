import { useState } from 'react'
import { Banner, Button, Checkbox, FormControl, Stack, Text, TextInput } from '@primer/react'
import { createServiceToken, listServiceTokens, revokeServiceToken } from '../../api/endpoints'
import type { CreateServiceTokenInput, ServiceTokenResponse } from '../../api/models'
import { useAction, useAsync } from '../../hooks'
import { useConfirmAction } from '../confirm'
import { DataTable, type Column } from '../DataTable'
import { FormDialog } from '../FormDialog'
import { SettingsCard } from '../SettingsCard'
import { Timestamp } from '../Timestamp'
import { ErrorBanner, Mono } from '../ui'

export const SERVICE_TOKEN_SCOPES: { value: string; label: string }[] = [
  { value: 'read', label: 'Read commits, tags, branches and merge requests' },
  { value: 'checks:write', label: 'Report merge request status checks' },
  { value: 'webhooks:write', label: 'Manage webhooks' },
]

export function endOfLocalDay(date: string): string {
  const [year, month, day] = date.split('-').map(Number)
  return new Date(year, month - 1, day, 23, 59, 59).toISOString()
}

function tokenState(token: ServiceTokenResponse): string {
  if (token.revoked_at) return 'Revoked'
  if (token.expires_at && new Date(token.expires_at).getTime() <= Date.now()) return 'Expired'
  return 'Active'
}

export function ServiceTokenSettings({ org, project }: { org: string; project: string }) {
  const confirm = useConfirmAction()
  const { data: tokens, error, loading, reload } = useAsync(() => listServiceTokens(org, project), [org, project])
  const { run, error: actionError, clearError } = useAction()
  const [creating, setCreating] = useState(false)
  const [revealed, setRevealed] = useState<{ name: string; token: string } | null>(null)

  async function handleRevoke(token: ServiceTokenResponse) {
    const ok = await confirm({
      title: 'Revoke service token?',
      content: `Integrations using ${token.name} stop working immediately. This cannot be undone.`,
      confirmLabel: 'Revoke',
    })
    if (!ok) return
    await run(async () => {
      await revokeServiceToken(org, project, token.id)
      reload()
    })
  }

  const columns: Column<ServiceTokenResponse>[] = [
    {
      id: 'name',
      header: 'Name',
      rowHeader: true,
      renderCell: (token) => (
        <>
          {token.name}
          <div>
            <Mono>{token.hint}…</Mono>
          </div>
        </>
      ),
    },
    { id: 'scopes', header: 'Scopes', renderCell: (token) => token.scopes.join(', ') },
    { id: 'state', header: 'State', renderCell: (token) => tokenState(token) },
    {
      id: 'used',
      header: 'Last used',
      renderCell: (token) => (token.last_used_at ? <Timestamp value={token.last_used_at} /> : 'Never'),
    },
    {
      id: 'expires',
      header: 'Expires',
      renderCell: (token) => (token.expires_at ? <Timestamp value={token.expires_at} /> : 'Never'),
    },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (token) =>
        token.revoked_at ? null : (
          <Button size="small" variant="danger" onClick={() => void handleRevoke(token)}>
            Revoke
          </Button>
        ),
    },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Service tokens"
        description="Project-scoped tokens for integrations such as BugBase. A token acts as the admin who created it, limited to this project and its scopes."
        actions={
          <Button size="small" onClick={() => setCreating(true)}>
            Create token
          </Button>
        }
      >
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
        {revealed && (
          <Banner variant="warning" title={`Token for ${revealed.name}`} onDismiss={() => setRevealed(null)}>
            <Stack direction="vertical" gap="condensed">
              <Mono>{revealed.token}</Mono>
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                Copy it now: the token is only shown once.
              </Text>
            </Stack>
          </Banner>
        )}
      </SettingsCard>

      <DataTable
        label="Service tokens"
        loading={loading}
        data={tokens ?? []}
        columns={columns}
        empty="No service tokens. Create one to connect an integration."
      />

      {creating && (
        <CreateServiceTokenDialog
          onClose={() => setCreating(false)}
          onCreate={async (input) => {
            const created = await createServiceToken(org, project, input)
            if (created.token) {
              setRevealed({ name: created.name, token: created.token })
            }
            reload()
          }}
        />
      )}
    </Stack>
  )
}

function CreateServiceTokenDialog({
  onClose,
  onCreate,
}: {
  onClose: () => void
  onCreate: (input: CreateServiceTokenInput) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>(['read'])
  const [expires, setExpires] = useState('')

  function toggleScope(value: string) {
    setScopes((current) => (current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value]))
  }

  return (
    <FormDialog
      title="Create service token"
      submitLabel="Create"
      submitDisabled={name.trim() === '' || scopes.length === 0}
      onClose={onClose}
      onSubmit={() =>
        onCreate({
          name,
          scopes,
          ...(expires ? { expires_at: endOfLocalDay(expires) } : {}),
        })
      }
    >
      <FormControl required>
        <FormControl.Label>Name</FormControl.Label>
        <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
      </FormControl>
      <FormControl>
        <FormControl.Label>Scopes</FormControl.Label>
        <Stack direction="vertical" gap="condensed">
          {SERVICE_TOKEN_SCOPES.map((entry) => (
            <label key={entry.value} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <Checkbox checked={scopes.includes(entry.value)} onChange={() => toggleScope(entry.value)} />
              <Mono>{entry.value}</Mono> {entry.label}
            </label>
          ))}
        </Stack>
      </FormControl>
      <FormControl>
        <FormControl.Label>Expires on (optional)</FormControl.Label>
        <TextInput block type="date" value={expires} onChange={(event) => setExpires(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}
