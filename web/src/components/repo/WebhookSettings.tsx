import { Fragment, useState } from 'react'
import { Banner, Button, FormControl, Stack, Text, TextInput } from '@primer/react'
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
import { useAsync } from '../../hooks'
import { EmptyState, ErrorBanner, Loading, Mono } from '../ui'

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

const cell = { padding: '6px 4px' }

export function WebhookSettings({ org, project }: { org: string; project: string }) {
  const { data: webhooks, error, loading, reload } = useAsync(
    () => listWebhooks(org, project),
    [org, project],
  )
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [events, setEvents] = useState<string[]>(['push'])
  const [pathPrefix, setPathPrefix] = useState('')
  const [isActive, setIsActive] = useState(true)
  const [insecureTLS, setInsecureTLS] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [deliveriesNonce, setDeliveriesNonce] = useState(0)
  const [revealedSecret, setRevealedSecret] = useState<{ name: string; secret: string } | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  function resetForm() {
    setName('')
    setUrl('')
    setEvents(['push'])
    setPathPrefix('')
    setIsActive(true)
    setInsecureTLS(false)
    setCreating(false)
  }

  function handleCreate(event: React.FormEvent) {
    event.preventDefault()
    run(async () => {
      const created = await createWebhook(org, project, {
        name,
        url,
        events,
        path_prefix: pathPrefix,
        is_active: isActive,
        insecure_tls: insecureTLS,
      })
      if (created.secret) {
        setRevealedSecret({ name: created.name || created.url, secret: created.secret })
      }
      resetForm()
      reload()
    })
  }

  function toggleEvent(value: string) {
    setEvents((current) =>
      current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value],
    )
  }

  function handleToggleActive(webhook: WebhookResponse) {
    run(async () => {
      await updateWebhook(org, project, webhook.id, { is_active: !webhook.is_active })
      reload()
    })
  }

  function handleRotate(webhook: WebhookResponse) {
    run(async () => {
      const rotated = await rotateWebhookSecret(org, project, webhook.id)
      if (rotated.secret) {
        setRevealedSecret({ name: rotated.name || rotated.url, secret: rotated.secret })
      }
    })
  }

  function handleTest(webhook: WebhookResponse) {
    run(async () => {
      await testWebhook(org, project, webhook.id)
      setExpanded(webhook.id)
      setDeliveriesNonce((value) => value + 1)
    })
  }

  function handleDelete(webhook: WebhookResponse) {
    run(async () => {
      await deleteWebhook(org, project, webhook.id)
      if (expanded === webhook.id) {
        setExpanded(null)
      }
      reload()
    })
  }

  return (
    <>
      <ErrorBanner error={actionError ?? error} />

      {revealedSecret && (
        <Banner variant="warning" title={`Signing secret for ${revealedSecret.name}`}>
          <Stack direction="vertical" gap="condensed">
            <Mono>{revealedSecret.secret}</Mono>
            <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
              Copy it now: the secret is only shown when it is created or rotated.
            </Text>
            <Button size="small" onClick={() => setRevealedSecret(null)} style={{ alignSelf: 'flex-start' }}>
              Dismiss
            </Button>
          </Stack>
        </Banner>
      )}

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Webhooks</Text>
        <Button size="small" onClick={() => setCreating((value) => !value)}>
          {creating ? 'Cancel' : 'Create webhook'}
        </Button>
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Signed HTTP POST notifications for pushes, branches and merge requests.
      </Text>

      {loading && <Loading />}
      {!loading && webhooks?.length === 0 && (
        <EmptyState>No webhooks. Create one to notify an external service about repository events.</EmptyState>
      )}
      {webhooks && webhooks.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ color: 'var(--fgColor-muted)', textAlign: 'left' }}>
              <th style={cell}>Name</th>
              <th style={cell}>URL</th>
              <th style={cell}>Events</th>
              <th style={cell}>Active</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {webhooks.map((webhook) => (
              <Fragment key={webhook.id}>
                <tr style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                  <td style={cell}>
                    {webhook.name || '(unnamed)'}
                    {webhook.path_prefix && (
                      <div style={{ color: 'var(--fgColor-muted)' }}>
                        only <Mono>{webhook.path_prefix}</Mono> pushes
                      </div>
                    )}
                  </td>
                  <td style={cell}>
                    <Mono>{webhook.url}</Mono>
                    {webhook.insecure_tls && (
                      <div style={{ color: 'var(--fgColor-attention)' }}>insecure TLS</div>
                    )}
                  </td>
                  <td style={cell}>{webhook.events.map(webhookEventLabel).join(', ')}</td>
                  <td style={cell}>
                    <input
                      type="checkbox"
                      aria-label={`Toggle ${webhook.name || webhook.url}`}
                      checked={webhook.is_active}
                      onChange={() => handleToggleActive(webhook)}
                    />
                  </td>
                  <td style={{ ...cell, textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <Button size="small" onClick={() => handleTest(webhook)}>
                      Send test
                    </Button>{' '}
                    <Button
                      size="small"
                      onClick={() => {
                        setExpanded((current) => (current === webhook.id ? null : webhook.id))
                        setDeliveriesNonce((value) => value + 1)
                      }}
                    >
                      {expanded === webhook.id ? 'Hide deliveries' : 'Deliveries'}
                    </Button>{' '}
                    <Button size="small" onClick={() => handleRotate(webhook)}>
                      Rotate secret
                    </Button>{' '}
                    <Button size="small" variant="danger" onClick={() => handleDelete(webhook)}>
                      Delete
                    </Button>
                  </td>
                </tr>
                {expanded === webhook.id && (
                  <tr>
                    <td colSpan={5} style={{ padding: '8px 4px', background: 'var(--bgColor-muted)' }}>
                      <WebhookDeliveries
                        key={`${webhook.id}:${deliveriesNonce}`}
                        org={org}
                        project={project}
                        webhookId={webhook.id}
                        onError={setActionError}
                      />
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      )}

      {creating && (
        <form
          onSubmit={handleCreate}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <Stack direction="vertical" gap="normal">
            <strong>Create webhook</strong>
            <FormControl>
              <FormControl.Label>Name</FormControl.Label>
              <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
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
                    <input
                      type="checkbox"
                      checked={events.includes(entry.value)}
                      onChange={() => toggleEvent(entry.value)}
                    />
                    {entry.label}
                  </label>
                ))}
              </Stack>
            </FormControl>
            <FormControl>
              <FormControl.Label>Path prefix (only filters push events)</FormControl.Label>
              <TextInput
                block
                placeholder="assets"
                value={pathPrefix}
                onChange={(event) => setPathPrefix(event.target.value)}
              />
            </FormControl>
            <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <input type="checkbox" checked={isActive} onChange={() => setIsActive((value) => !value)} />
              Active
            </label>
            <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <input type="checkbox" checked={insecureTLS} onChange={() => setInsecureTLS((value) => !value)} />
              Skip TLS verification (self-signed receiver)
            </label>
            <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
              Create
            </Button>
          </Stack>
        </form>
      )}
    </>
  )
}

function WebhookDeliveries({
  org,
  project,
  webhookId,
  onError,
}: {
  org: string
  project: string
  webhookId: string
  onError: (message: string) => void
}) {
  const { data: deliveries, error, loading, reload } = useAsync(
    () => listWebhookDeliveries(org, project, webhookId),
    [org, project, webhookId],
  )
  const [redelivering, setRedelivering] = useState<string | null>(null)

  async function redeliver(delivery: WebhookDeliveryResponse) {
    setRedelivering(delivery.id)
    try {
      await redeliverWebhookDelivery(org, project, webhookId, delivery.id)
      reload()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setRedelivering(null)
    }
  }

  if (loading) {
    return <Loading />
  }
  if (error) {
    return <ErrorBanner error={error} />
  }
  if (!deliveries || deliveries.length === 0) {
    return <EmptyState>No deliveries yet.</EmptyState>
  }
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
      <thead>
        <tr style={{ color: 'var(--fgColor-muted)', textAlign: 'left' }}>
          <th style={cell}>Event</th>
          <th style={cell}>State</th>
          <th style={cell}>Attempt</th>
          <th style={cell}>Response</th>
          <th style={cell}>Last error</th>
          <th style={cell}>Created</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {deliveries.map((delivery) => (
          <tr key={delivery.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
            <td style={cell}>{delivery.event_type}</td>
            <td style={cell}>
              <span
                style={{
                  fontWeight: 600,
                  color:
                    delivery.state === 'delivered'
                      ? 'var(--fgColor-success)'
                      : delivery.state === 'failed'
                        ? 'var(--fgColor-danger)'
                        : 'var(--fgColor-attention)',
                }}
              >
                {delivery.state}
              </span>
            </td>
            <td style={cell}>{delivery.attempt}</td>
            <td style={cell}>{delivery.response_status ?? '—'}</td>
            <td
              style={{
                ...cell,
                maxWidth: 260,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
              title={delivery.last_error}
            >
              {delivery.last_error || '—'}
            </td>
            <td style={{ ...cell, color: 'var(--fgColor-muted)' }}>
              {new Date(delivery.created_at).toLocaleString()}
            </td>
            <td style={{ ...cell, textAlign: 'right' }}>
              <Button size="small" disabled={redelivering === delivery.id} onClick={() => redeliver(delivery)}>
                Redeliver
              </Button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
