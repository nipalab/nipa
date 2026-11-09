import { useState } from 'react'
import { ActionList, ActionMenu, Button, Checkbox, CheckboxGroup, FormControl, Stack, TextInput } from '@primer/react'
import { createServiceToken, listServiceTokens, revokeServiceToken } from '../../api/endpoints'
import type { CreateServiceTokenInput, ServiceTokenResponse } from '../../api/models'
import { useAction, useAsync } from '../../hooks'
import { useConfirmAction } from '../confirm'
import { DataTable, type Column } from '../DataTable'
import { FormDialog } from '../FormDialog'
import { SecretReveal } from '../SecretReveal'
import { SettingsCard } from '../SettingsCard'
import { Timestamp } from '../Timestamp'
import { ErrorBanner, Mono } from '../ui'

export const SERVICE_TOKEN_SCOPES: { value: string; title: string; label: string }[] = [
  { value: 'read', title: 'Read', label: 'Read commits, tags, branches and merge requests.' },
  { value: 'checks:write', title: 'Status checks', label: 'Report merge request status checks, for example from CI.' },
  { value: 'webhooks:write', title: 'Webhooks', label: 'Create, update and delete the project webhooks.' },
]

const EXPIRATION_OPTIONS: { value: string; label: string; days?: number }[] = [
  { value: '7', label: '7 days', days: 7 },
  { value: '30', label: '30 days', days: 30 },
  { value: '60', label: '60 days', days: 60 },
  { value: '90', label: '90 days', days: 90 },
  { value: 'custom', label: 'Custom…' },
  { value: 'never', label: 'No expiration' },
]

function localDate(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

// expiryDay turns an expiration choice into the local YYYY-MM-DD day the token
// stops working on, or '' when it never expires (or no custom day is picked).
export function expiryDay(choice: string, custom: string, now = new Date()): string {
  if (choice === 'never') return ''
  if (choice === 'custom') return custom
  const days = EXPIRATION_OPTIONS.find((option) => option.value === choice)?.days ?? 30
  const end = new Date(now.getFullYear(), now.getMonth(), now.getDate() + days)
  return localDate(end)
}

function formatDay(day: string): string {
  const [year, month, date] = day.split('-').map(Number)
  return new Date(year, month - 1, date).toLocaleDateString(undefined, {
    weekday: 'short',
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

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
          <SecretReveal
            title={`Token ${revealed.name} created`}
            message="Make sure to copy your service token now. You won't be able to see it again."
            secret={revealed.token}
            copyLabel="Copy token"
            onDismiss={() => setRevealed(null)}
          />
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
  const [expiration, setExpiration] = useState('30')
  const [custom, setCustom] = useState('')
  const day = expiryDay(expiration, custom)
  const today = localDate(new Date())
  const customInvalid = expiration === 'custom' && (custom === '' || custom < today)
  const expirationLabel = EXPIRATION_OPTIONS.find((option) => option.value === expiration)?.label ?? '30 days'

  function toggleScope(value: string) {
    setScopes((current) => (current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value]))
  }

  return (
    <FormDialog
      title="New service token"
      submitLabel="Generate token"
      submitDisabled={name.trim() === '' || scopes.length === 0 || customInvalid}
      onClose={onClose}
      onSubmit={() =>
        onCreate({
          name: name.trim(),
          scopes: SERVICE_TOKEN_SCOPES.map((entry) => entry.value).filter((value) => scopes.includes(value)),
          ...(day ? { expires_at: endOfLocalDay(day) } : {}),
        })
      }
    >
      <FormControl required>
        <FormControl.Label>Token name</FormControl.Label>
        <TextInput
          block
          autoFocus
          placeholder="e.g. BugBase integration"
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
        <FormControl.Caption>A unique name that tells you what this token is used for.</FormControl.Caption>
      </FormControl>

      <FormControl>
        <FormControl.Label>Expiration</FormControl.Label>
        <Stack direction="horizontal" gap="condensed" align="center" wrap="wrap">
          <ActionMenu>
            <ActionMenu.Button aria-label="Token expiration">{expirationLabel}</ActionMenu.Button>
            <ActionMenu.Overlay width="small">
              <ActionList selectionVariant="single" aria-label="Expiration">
                {EXPIRATION_OPTIONS.map((option) => (
                  <ActionList.Item key={option.value} selected={option.value === expiration} onSelect={() => setExpiration(option.value)}>
                    {option.label}
                  </ActionList.Item>
                ))}
              </ActionList>
            </ActionMenu.Overlay>
          </ActionMenu>
          {expiration === 'custom' && (
            <TextInput
              type="date"
              aria-label="Expiration date"
              min={today}
              value={custom}
              onChange={(event) => setCustom(event.target.value)}
            />
          )}
        </Stack>
        {expiration === 'never' ? (
          <FormControl.Caption>
            <span style={{ color: 'var(--fgColor-attention)' }}>
              The token never expires and stays valid until revoked; an expiration date is recommended.
            </span>
          </FormControl.Caption>
        ) : day ? (
          <FormControl.Caption>The token will expire on {formatDay(day)}.</FormControl.Caption>
        ) : (
          <FormControl.Caption>Pick the last day the token works.</FormControl.Caption>
        )}
      </FormControl>

      <CheckboxGroup>
        <CheckboxGroup.Label>Scopes</CheckboxGroup.Label>
        <CheckboxGroup.Caption>The token acts as you, limited to this project and the scopes below.</CheckboxGroup.Caption>
        {SERVICE_TOKEN_SCOPES.map((entry) => (
          <FormControl key={entry.value}>
            <Checkbox value={entry.value} checked={scopes.includes(entry.value)} onChange={() => toggleScope(entry.value)} />
            <FormControl.Label>
              {entry.title} <Mono style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}>{entry.value}</Mono>
            </FormControl.Label>
            <FormControl.Caption>{entry.label}</FormControl.Caption>
          </FormControl>
        ))}
        {scopes.length === 0 && <CheckboxGroup.Validation variant="error">Choose at least one scope.</CheckboxGroup.Validation>}
      </CheckboxGroup>
    </FormDialog>
  )
}
