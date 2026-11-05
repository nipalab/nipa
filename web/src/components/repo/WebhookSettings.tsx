import { useState } from 'react'
import { Banner, Button, Checkbox, FormControl, Stack, Text, TextInput } from '@primer/react'
import {
  createWebhook,
  deleteWebhook,
  listWebhookDeliveries,
  listWebhooks,
  redeliverWebhookDelivery,
  rotateWebhookSecret,
  testWebhook,
  updateWebhook,
} from '../../api/endpoints'
import type { WebhookDeliveryResponse, WebhookResponse } from '../../api/models'
import { useAction, useAsync } from '../../hooks'
import { useConfirmAction } from '../confirm'
import { DataTable, type Column } from '../DataTable'
import { FormDialog } from '../FormDialog'
import { SettingsCard } from '../SettingsCard'
import { StateBadge } from '../StateBadge'
import { Timestamp } from '../Timestamp'
import { ErrorBanner, Mono } from '../ui'

export const WEBHOOK_EVENTS: { value: string; label: string }[] = [
  { value: 'push', label: 'Push' },
  { value: 'branch.created', label: 'Branch created' },
  { value: 'branch.deleted', label: 'Branch deleted' },
  { value: 'mr.created', label: 'Merge request created' },
  { value: 'mr.updated', label: 'Merge request updated' },
  { value: 'mr.synchronized', label: 'Merge request synchronized' },
  { value: 'mr.merged', label: 'Merge request merged' },
  { value: 'mr.closed', label: 'Merge request closed' },
  { value: 'mr.reopened', label: 'Merge request reopened' },
  { value: 'mr.review_submitted', label: 'Merge request review submitted' },
  { value: 'mr.review_dismissed', label: 'Merge request review dismissed' },
  { value: 'mr.review_requested', label: 'Merge request review requested' },
  { value: 'mr.review_request_removed', label: 'Merge request review request removed' },
  { value: 'mr.comment_created', label: 'Merge request comment created' },
]

export function webhookEventLabel(event: string): string {
  return WEBHOOK_EVENTS.find((entry) => entry.value === event)?.label ?? event
}

const ellipsis = {
  display: 'inline-block',
  maxWidth: 260,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'bottom',
} as const

export function WebhookSettings({ org, project }: { org: string; project: string }) {
  const confirm = useConfirmAction()
  const { data: webhooks, error, loading, reload } = useAsync(() => listWebhooks(org, project), [org, project])
  const { run, error: actionError, clearError } = useAction()
  const [creating, setCreating] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [deliveriesNonce, setDeliveriesNonce] = useState(0)
  const [revealedSecret, setRevealedSecret] = useState<{ name: string; secret: string } | null>(null)

  const expandedHook = webhooks?.find((webhook) => webhook.id === expanded)

  function handleToggleActive(webhook: WebhookResponse) {
    void run(async () => {
      await updateWebhook(org, project, webhook.id, { is_active: !webhook.is_active })
      reload()
    })
  }

  async function handleRotate(webhook: WebhookResponse) {
    const ok = await confirm({
      title: 'Rotate signing secret?',
      content: 'The current secret stops working immediately. Update the receiver with the new one.',
      confirmLabel: 'Rotate secret',
    })
    if (!ok) return
    await run(async () => {
      const rotated = await rotateWebhookSecret(org, project, webhook.id)
      if (rotated.secret) {
        setRevealedSecret({ name: rotated.name || rotated.url, secret: rotated.secret })
      }
    })
  }

  function handleTest(webhook: WebhookResponse) {
    void run(async () => {
      await testWebhook(org, project, webhook.id)
      setExpanded(webhook.id)
      setDeliveriesNonce((value) => value + 1)
    })
  }

  async function handleDelete(webhook: WebhookResponse) {
    const ok = await confirm({
      title: 'Delete webhook?',
      content: `${webhook.name || webhook.url} will stop receiving events. This cannot be undone.`,
      confirmLabel: 'Delete',
    })
    if (!ok) return
    await run(async () => {
      await deleteWebhook(org, project, webhook.id)
      if (expanded === webhook.id) {
        setExpanded(null)
      }
      reload()
    })
  }

  const columns: Column<WebhookResponse>[] = [
    {
      id: 'name',
      header: 'Name',
      rowHeader: true,
      renderCell: (webhook) => (
        <>
          {webhook.name || '(unnamed)'}
          {webhook.path_prefix && (
            <div style={{ color: 'var(--fgColor-muted)' }}>
              only <Mono>{webhook.path_prefix}</Mono> pushes
            </div>
          )}
        </>
      ),
    },
    {
      id: 'url',
      header: 'URL',
      renderCell: (webhook) => (
        <>
          <Mono>{webhook.url}</Mono>
          {webhook.insecure_tls && <div style={{ color: 'var(--fgColor-attention)' }}>insecure TLS</div>}
        </>
      ),
    },
    { id: 'events', header: 'Events', renderCell: (webhook) => webhook.events.map(webhookEventLabel).join(', ') },
    {
      id: 'active',
      header: 'Active',
      renderCell: (webhook) => (
        <Checkbox
          aria-label={`Toggle ${webhook.name || webhook.url}`}
          checked={webhook.is_active}
          onChange={() => handleToggleActive(webhook)}
        />
      ),
    },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (webhook) => (
        <Stack direction="horizontal" gap="condensed" justify="end">
          <Button size="small" onClick={() => handleTest(webhook)}>
            Send test
          </Button>
          <Button
            size="small"
            onClick={() => {
              setExpanded((current) => (current === webhook.id ? null : webhook.id))
              setDeliveriesNonce((value) => value + 1)
            }}
          >
            {expanded === webhook.id ? 'Hide deliveries' : 'Deliveries'}
          </Button>
          <Button size="small" onClick={() => void handleRotate(webhook)}>
            Rotate secret
          </Button>
          <Button size="small" variant="danger" onClick={() => void handleDelete(webhook)}>
            Delete
          </Button>
        </Stack>
      ),
    },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Webhooks"
        description="Signed HTTP POST notifications for pushes, branches and merge requests."
        actions={
          <Button size="small" onClick={() => setCreating(true)}>
            Create webhook
          </Button>
        }
      >
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
        {revealedSecret && (
          <Banner variant="warning" title={`Signing secret for ${revealedSecret.name}`} onDismiss={() => setRevealedSecret(null)}>
            <Stack direction="vertical" gap="condensed">
              <Mono>{revealedSecret.secret}</Mono>
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                Copy it now: the secret is only shown when it is created or rotated.
              </Text>
            </Stack>
          </Banner>
        )}
      </SettingsCard>

      <DataTable
        label="Webhooks"
        loading={loading}
        data={webhooks ?? []}
        columns={columns}
        empty="No webhooks. Create one to notify an external service about repository events."
      />

      {expandedHook && (
        <SettingsCard title={`Deliveries for ${expandedHook.name || expandedHook.url}`}>
          <WebhookDeliveries
            key={`${expandedHook.id}:${deliveriesNonce}`}
            org={org}
            project={project}
            webhookId={expandedHook.id}
          />
        </SettingsCard>
      )}

      {creating && (
        <CreateWebhookDialog
          onClose={() => setCreating(false)}
          onCreate={async (input) => {
            const created = await createWebhook(org, project, input)
            if (created.secret) {
              setRevealedSecret({ name: created.name || created.url, secret: created.secret })
            }
            reload()
          }}
        />
      )}
    </Stack>
  )
}

function CreateWebhookDialog({
  onClose,
  onCreate,
}: {
  onClose: () => void
  onCreate: (input: {
    name: string
    url: string
    events: string[]
    path_prefix: string
    is_active: boolean
    insecure_tls: boolean
  }) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [events, setEvents] = useState<string[]>(['push'])
  const [pathPrefix, setPathPrefix] = useState('')
  const [isActive, setIsActive] = useState(true)
  const [insecureTLS, setInsecureTLS] = useState(false)

  function toggleEvent(value: string) {
    setEvents((current) => (current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value]))
  }

  return (
    <FormDialog
      title="Create webhook"
      width="large"
      submitLabel="Create"
      submitDisabled={url.trim() === ''}
      onClose={onClose}
      onSubmit={() =>
        onCreate({
          name,
          url,
          events,
          path_prefix: pathPrefix,
          is_active: isActive,
          insecure_tls: insecureTLS,
        })
      }
    >
      <FormControl>
        <FormControl.Label>Name</FormControl.Label>
        <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
      </FormControl>
      <FormControl required>
        <FormControl.Label>Payload URL</FormControl.Label>
        <TextInput
          block
          type="url"
          placeholder="https://ci.example.com/hooks/nipa"
          value={url}
          onChange={(event) => setUrl(event.target.value)}
        />
      </FormControl>
      <FormControl>
        <FormControl.Label>Events</FormControl.Label>
        <Stack direction="vertical" gap="condensed">
          {WEBHOOK_EVENTS.map((entry) => (
            <label key={entry.value} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <Checkbox checked={events.includes(entry.value)} onChange={() => toggleEvent(entry.value)} />
              {entry.label}
            </label>
          ))}
        </Stack>
      </FormControl>
      <FormControl>
        <FormControl.Label>Path prefix (only filters push events)</FormControl.Label>
        <TextInput block placeholder="assets" value={pathPrefix} onChange={(event) => setPathPrefix(event.target.value)} />
      </FormControl>
      <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <Checkbox checked={isActive} onChange={() => setIsActive((value) => !value)} />
        Active
      </label>
      <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <Checkbox checked={insecureTLS} onChange={() => setInsecureTLS((value) => !value)} />
        Skip TLS verification (self-signed receiver)
      </label>
    </FormDialog>
  )
}

function WebhookDeliveries({ org, project, webhookId }: { org: string; project: string; webhookId: string }) {
  const { data: deliveries, error, loading, reload } = useAsync(
    () => listWebhookDeliveries(org, project, webhookId),
    [org, project, webhookId],
  )
  const { run, error: actionError, clearError } = useAction(reload)
  const [redelivering, setRedelivering] = useState<string | null>(null)

  async function redeliver(delivery: WebhookDeliveryResponse) {
    setRedelivering(delivery.id)
    await run(() => redeliverWebhookDelivery(org, project, webhookId, delivery.id))
    setRedelivering(null)
  }

  const columns: Column<WebhookDeliveryResponse>[] = [
    { id: 'event', header: 'Event', rowHeader: true, renderCell: (delivery) => delivery.event_type },
    { id: 'state', header: 'State', renderCell: (delivery) => <StateBadge status={delivery.state} /> },
    { id: 'attempt', header: 'Attempt', renderCell: (delivery) => delivery.attempt },
    { id: 'response', header: 'Response', renderCell: (delivery) => delivery.response_status ?? '—' },
    {
      id: 'error',
      header: 'Last error',
      renderCell: (delivery) => (
        <span style={ellipsis} title={delivery.last_error}>
          {delivery.last_error || '—'}
        </span>
      ),
    },
    { id: 'created', header: 'Created', renderCell: (delivery) => <Timestamp value={delivery.created_at} /> },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (delivery) => (
        <Button size="small" disabled={redelivering === delivery.id} onClick={() => void redeliver(delivery)}>
          Redeliver
        </Button>
      ),
    },
  ]

  return (
    <>
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <DataTable label="Webhook deliveries" loading={loading} data={deliveries ?? []} columns={columns} empty="No deliveries yet." />
    </>
  )
}
