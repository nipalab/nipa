import { useState } from 'react'
import { ActionList, ActionMenu, Button, Stack } from '@primer/react'
import { listEmailDeliveries, redeliverEmailDelivery } from '../../api/endpoints'
import type { EmailDeliveryResponse } from '../../api/models'
import { useAction, useAsync } from '../../hooks'
import { DataTable, type Column } from '../DataTable'
import { SettingsCard } from '../SettingsCard'
import { StateBadge } from '../StateBadge'
import { Timestamp } from '../Timestamp'
import { ErrorBanner } from '../ui'

const STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'failed', label: 'Failed' },
  { value: 'pending', label: 'Pending' },
  { value: 'delivered', label: 'Delivered' },
]

const ellipsis = {
  display: 'inline-block',
  maxWidth: 260,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'bottom',
} as const

export function EmailSettings({ org, project }: { org: string; project: string }) {
  const [state, setState] = useState('')
  const { data, error, loading, reload } = useAsync(
    () => listEmailDeliveries(org, project, state),
    [org, project, state],
  )
  const { run, error: actionError, clearError } = useAction(reload)
  const stateLabel = STATE_OPTIONS.find((option) => option.value === state)?.label ?? 'All states'
  const [redelivering, setRedelivering] = useState<string | null>(null)

  async function redeliver(delivery: EmailDeliveryResponse) {
    setRedelivering(delivery.id)
    await run(() => redeliverEmailDelivery(org, project, delivery.id))
    setRedelivering(null)
  }

  const columns: Column<EmailDeliveryResponse>[] = [
    { id: 'email', header: 'Recipient', rowHeader: true, renderCell: (delivery) => delivery.email },
    { id: 'event', header: 'Event', renderCell: (delivery) => delivery.event },
    {
      id: 'subject',
      header: 'Subject',
      renderCell: (delivery) => (
        <span style={ellipsis} title={delivery.subject}>
          {delivery.subject}
        </span>
      ),
    },
    { id: 'state', header: 'State', renderCell: (delivery) => <StateBadge status={delivery.state} /> },
    { id: 'attempts', header: 'Attempts', renderCell: (delivery) => delivery.attempts },
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
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Email deliveries"
        description="Notification emails queued for this project, newest first. Failed deliveries retry automatically; redelivering re-queues a delivered or failed notification."
        actions={
          <ActionMenu>
            <ActionMenu.Button aria-label="Filter by delivery state">
              <span style={{ color: 'var(--fgColor-muted)' }}>State:</span> {stateLabel}
            </ActionMenu.Button>
            <ActionMenu.Overlay width="small" align="end">
              <ActionList selectionVariant="single" aria-label="Delivery state">
                {STATE_OPTIONS.map((option) => (
                  <ActionList.Item
                    key={option.value}
                    selected={option.value === state}
                    onSelect={() => setState(option.value)}
                  >
                    {option.label}
                  </ActionList.Item>
                ))}
              </ActionList>
            </ActionMenu.Overlay>
          </ActionMenu>
        }
      >
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      </SettingsCard>
      <DataTable
        label="Email deliveries"
        loading={loading}
        data={data?.deliveries ?? []}
        columns={columns}
        empty="No email deliveries."
      />
    </Stack>
  )
}
