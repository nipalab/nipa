import { useState } from 'react'
import { Button, FormControl, Stack, Text } from '@primer/react'
import { listEmailDeliveries, redeliverEmailDelivery } from '../../api/endpoints'
import type { EmailDeliveryResponse } from '../../api/models'
import { useAsync } from '../../hooks'
import { EmptyState, ErrorBanner, Loading } from '../ui'

const cell = { padding: '6px 4px' }

const STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'failed', label: 'Failed' },
  { value: 'pending', label: 'Pending' },
  { value: 'delivered', label: 'Delivered' },
]

function stateColor(state: string): string {
  if (state === 'delivered') return 'var(--fgColor-success)'
  if (state === 'failed') return 'var(--fgColor-danger)'
  return 'var(--fgColor-attention)'
}

export function EmailSettings({ org, project }: { org: string; project: string }) {
  const [state, setState] = useState('')
  const { data, error, loading, reload } = useAsync(
    () => listEmailDeliveries(org, project, state),
    [org, project, state],
  )
  const [redelivering, setRedelivering] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  async function redeliver(delivery: EmailDeliveryResponse) {
    setRedelivering(delivery.id)
    setActionError(null)
    try {
      await redeliverEmailDelivery(org, project, delivery.id)
      reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setRedelivering(null)
    }
  }

  return (
    <Stack direction="vertical" gap="normal">
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Notification emails queued for this project, newest first. Failed deliveries retry automatically;
        redelivering re-queues a delivered or failed notification.
      </Text>
      <ErrorBanner error={actionError ?? error} />
      <FormControl>
        <FormControl.Label>State</FormControl.Label>
        <select
          aria-label="Delivery state"
          value={state}
          onChange={(event) => setState(event.target.value)}
          style={{
            padding: '5px 8px',
            borderRadius: 6,
            border: '1px solid var(--borderColor-default)',
            background: 'var(--bgColor-default)',
            color: 'inherit',
          }}
        >
          {STATE_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </FormControl>
      {loading ? (
        <Loading />
      ) : !data || data.deliveries.length === 0 ? (
        <EmptyState>No email deliveries.</EmptyState>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ color: 'var(--fgColor-muted)', textAlign: 'left' }}>
              <th style={cell}>Recipient</th>
              <th style={cell}>Event</th>
              <th style={cell}>Subject</th>
              <th style={cell}>State</th>
              <th style={cell}>Attempts</th>
              <th style={cell}>Last error</th>
              <th style={cell}>Created</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.deliveries.map((delivery) => (
              <tr key={delivery.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={cell}>{delivery.email}</td>
                <td style={cell}>{delivery.event}</td>
                <td
                  style={{
                    ...cell,
                    maxWidth: 260,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                  title={delivery.subject}
                >
                  {delivery.subject}
                </td>
                <td style={{ ...cell, fontWeight: 600, color: stateColor(delivery.state) }}>
                  {delivery.state}
                </td>
                <td style={cell}>{delivery.attempts}</td>
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
                  <Button
                    size="small"
                    disabled={redelivering === delivery.id}
                    onClick={() => redeliver(delivery)}
                  >
                    Redeliver
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Stack>
  )
}
