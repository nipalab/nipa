import { useState } from 'react'
import { Button, Checkbox, CheckboxGroup, FormControl, Radio, RadioGroup, Stack, Text, TextInput } from '@primer/react'
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
import { SecretReveal } from '../SecretReveal'
import { SettingsCard } from '../SettingsCard'
import { StateBadge } from '../StateBadge'
import { Timestamp } from '../Timestamp'
import { ErrorBanner, Mono } from '../ui'

export const WEBHOOK_EVENTS: { value: string; label: string; group: 'Repository' | 'Merge requests'; short: string }[] = [
  { value: 'push', label: 'Push', group: 'Repository', short: 'Push' },
  { value: 'branch.created', label: 'Branch created', group: 'Repository', short: 'Branch created' },
  { value: 'branch.deleted', label: 'Branch deleted', group: 'Repository', short: 'Branch deleted' },
  { value: 'mr.created', label: 'Merge request created', group: 'Merge requests', short: 'Created' },
  { value: 'mr.updated', label: 'Merge request updated', group: 'Merge requests', short: 'Updated' },
  { value: 'mr.synchronized', label: 'Merge request synchronized', group: 'Merge requests', short: 'New commits pushed' },
  { value: 'mr.merged', label: 'Merge request merged', group: 'Merge requests', short: 'Merged' },
  { value: 'mr.closed', label: 'Merge request closed', group: 'Merge requests', short: 'Closed' },
  { value: 'mr.reopened', label: 'Merge request reopened', group: 'Merge requests', short: 'Reopened' },
  { value: 'mr.review_submitted', label: 'Merge request review submitted', group: 'Merge requests', short: 'Review submitted' },
  { value: 'mr.review_dismissed', label: 'Merge request review dismissed', group: 'Merge requests', short: 'Review dismissed' },
  { value: 'mr.review_requested', label: 'Merge request review requested', group: 'Merge requests', short: 'Review requested' },
  {
    value: 'mr.review_request_removed',
    label: 'Merge request review request removed',
    group: 'Merge requests',
    short: 'Review request removed',
  },
  { value: 'mr.comment_created', label: 'Merge request comment created', group: 'Merge requests', short: 'Comment created' },
]

const EVENT_GROUPS = ['Repository', 'Merge requests'] as const

type EventMode = 'push' | 'all' | 'custom'

export function isPayloadURL(value: string): boolean {
  try {
    const url = new URL(value.trim())
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.host !== ''
  } catch {
    return false
  }
}

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
          <SecretReveal
            title={`Signing secret for ${revealedSecret.name}`}
            message="Copy it now and configure your receiver with it. The secret is only shown when it is created or rotated."
            secret={revealedSecret.secret}
            copyLabel="Copy secret"
            onDismiss={() => setRevealedSecret(null)}
          />
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
  const [mode, setMode] = useState<EventMode>('push')
  const [picked, setPicked] = useState<string[]>(['push'])
  const [pathPrefix, setPathPrefix] = useState('')
  const [isActive, setIsActive] = useState(true)
  const [insecureTLS, setInsecureTLS] = useState(false)

  const events = mode === 'push' ? ['push'] : mode === 'all' ? WEBHOOK_EVENTS.map((entry) => entry.value) : picked
  const urlInvalid = url.trim() !== '' && !isPayloadURL(url)
  const pushSelected = events.includes('push')

  function toggleEvent(value: string) {
    setPicked((current) => (current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value]))
  }

  return (
    <FormDialog
      title="Add webhook"
      width="large"
      submitLabel="Add webhook"
      submitDisabled={!isPayloadURL(url) || events.length === 0}
      onClose={onClose}
      onSubmit={() =>
        onCreate({
          name: name.trim(),
          url: url.trim(),
          events: WEBHOOK_EVENTS.map((entry) => entry.value).filter((value) => events.includes(value)),
          path_prefix: pushSelected ? pathPrefix.trim() : '',
          is_active: isActive,
          insecure_tls: insecureTLS,
        })
      }
    >
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)', fontSize: 14 }}>
        We'll send a signed POST request to the URL below with details of any subscribed events.
      </Text>
      <FormControl required>
        <FormControl.Label>Payload URL</FormControl.Label>
        <TextInput
          block
          autoFocus
          type="url"
          placeholder="https://ci.example.com/hooks/nipa"
          value={url}
          validationStatus={urlInvalid ? 'error' : undefined}
          onChange={(event) => setUrl(event.target.value)}
        />
        {urlInvalid ? (
          <FormControl.Validation variant="error">Enter a full http:// or https:// URL.</FormControl.Validation>
        ) : (
          <FormControl.Caption>Requests carry an HMAC signature made with a secret shown after creation.</FormControl.Caption>
        )}
      </FormControl>
      <FormControl>
        <FormControl.Label>Name</FormControl.Label>
        <TextInput block placeholder="e.g. CI notifications" value={name} onChange={(event) => setName(event.target.value)} />
        <FormControl.Caption>Optional; shown in the webhook list instead of the URL.</FormControl.Caption>
      </FormControl>

      <RadioGroup name="webhook-events">
        <RadioGroup.Label>Which events would you like to trigger this webhook?</RadioGroup.Label>
        <FormControl>
          <Radio value="push" checked={mode === 'push'} onChange={() => setMode('push')} />
          <FormControl.Label>Just the push event</FormControl.Label>
        </FormControl>
        <FormControl>
          <Radio value="all" checked={mode === 'all'} onChange={() => setMode('all')} />
          <FormControl.Label>Send me everything</FormControl.Label>
        </FormControl>
        <FormControl>
          <Radio value="custom" checked={mode === 'custom'} onChange={() => setMode('custom')} />
          <FormControl.Label>Let me select individual events</FormControl.Label>
        </FormControl>
      </RadioGroup>

      {mode === 'custom' && (
        <div className="nipa-webhook-events">
          {EVENT_GROUPS.map((group) => (
            <CheckboxGroup key={group}>
              <CheckboxGroup.Label>{group}</CheckboxGroup.Label>
              {WEBHOOK_EVENTS.filter((entry) => entry.group === group).map((entry) => (
                <FormControl key={entry.value}>
                  <Checkbox value={entry.value} checked={picked.includes(entry.value)} onChange={() => toggleEvent(entry.value)} />
                  <FormControl.Label>{entry.short}</FormControl.Label>
                  <FormControl.Caption>
                    <Mono>{entry.value}</Mono>
                  </FormControl.Caption>
                </FormControl>
              ))}
            </CheckboxGroup>
          ))}
          {picked.length === 0 && (
            <Text as="p" style={{ margin: 0, color: 'var(--fgColor-danger)', fontSize: 12 }}>
              Choose at least one event.
            </Text>
          )}
        </div>
      )}

      {pushSelected && (
        <FormControl>
          <FormControl.Label>Only pushes under path</FormControl.Label>
          <TextInput
            block
            leadingVisual={() => <span style={{ color: 'var(--fgColor-muted)' }}>/</span>}
            placeholder="e.g. assets"
            value={pathPrefix}
            onChange={(event) => setPathPrefix(event.target.value)}
          />
          <FormControl.Caption>Push events fire only when a file under this folder changed. Leave empty for every push.</FormControl.Caption>
        </FormControl>
      )}

      <RadioGroup name="webhook-ssl">
        <RadioGroup.Label>SSL verification</RadioGroup.Label>
        <FormControl>
          <Radio value="enable" checked={!insecureTLS} onChange={() => setInsecureTLS(false)} />
          <FormControl.Label>Enable SSL verification</FormControl.Label>
        </FormControl>
        <FormControl>
          <Radio value="disable" checked={insecureTLS} onChange={() => setInsecureTLS(true)} />
          <FormControl.Label>Disable (not recommended)</FormControl.Label>
          <FormControl.Caption>Only for receivers with a self-signed certificate.</FormControl.Caption>
        </FormControl>
      </RadioGroup>

      <FormControl>
        <Checkbox checked={isActive} onChange={() => setIsActive((value) => !value)} />
        <FormControl.Label>Active</FormControl.Label>
        <FormControl.Caption>Event details are delivered when this webhook is triggered.</FormControl.Caption>
      </FormControl>
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
