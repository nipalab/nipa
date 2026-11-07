import { useEffect, useState } from 'react'
import { Banner, Button, IconButton, Label, SegmentedControl, Stack, Text, TextInput } from '@primer/react'
import { DashIcon, LinkExternalIcon, PlusIcon } from '@primer/octicons-react'
import { createCheckout, getMyBilling, openBillingPortal, updateSubscription } from '../api/endpoints'
import type { BillingCapabilities, BillingInterval, BillingSummary } from '../api/models'
import { loadPaddle, previewPrices, subscribePaddleEvents, type Paddle, type PreviewLine } from '../billing/paddle'
import { errorMessage } from '../hooks'
import { SettingsCard, SettingsRow } from './SettingsCard'
import { ErrorBanner } from './ui'

export const billingTiming = { pollMs: 2000, pollAttempts: 15, previewDebounceMs: 300 }

const STATUS_LABELS: Record<string, string> = {
  active: 'Active',
  trialing: 'Trial',
  past_due: 'Past due',
  paused: 'Paused',
  canceled: 'Canceled',
}

function formatDate(iso: string | null | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(undefined, { dateStyle: 'medium' })
}

function intervalLabel(interval: string): string {
  return interval === 'year' ? 'Yearly' : interval === 'month' ? 'Monthly' : '—'
}

export function isPaidPlan(summary: BillingSummary): boolean {
  return summary.plan !== 'free'
}

export function PlanCard({ summary }: { summary: BillingSummary }) {
  const paid = isPaidPlan(summary)
  const periodEnd = formatDate(summary.current_period_end)
  return (
    <SettingsCard title="Plan" description="Your subscription covers every organization you own">
      {summary.status === 'past_due' && (
        <div style={{ padding: '0 var(--base-size-16) var(--base-size-16)' }}>
          <Banner variant="warning" title="Payment past due">
            The last payment failed. Your plan stays active while the payment is retried; update the payment
            method under Manage billing to keep it.
          </Banner>
        </div>
      )}
      <SettingsRow label="Plan">
        <Stack direction="horizontal" gap="condensed" align="center">
          <Text style={{ fontWeight: 600 }}>{paid ? capitalize(summary.plan) : 'Free'}</Text>
          {paid && summary.status && (
            <Label variant={summary.status === 'past_due' ? 'attention' : 'success'}>
              {STATUS_LABELS[summary.status] ?? summary.status}
            </Label>
          )}
        </Stack>
      </SettingsRow>
      {paid && (
        <SettingsRow label="Billing interval">
          <Text>{intervalLabel(summary.interval)}</Text>
        </SettingsRow>
      )}
      {paid && periodEnd && (
        <SettingsRow label={summary.cancel_at_period_end ? 'Cancels' : 'Renews'}>
          <Text>{summary.cancel_at_period_end ? `Cancels on ${periodEnd}` : `Renews on ${periodEnd}`}</Text>
        </SettingsRow>
      )}
      <SettingsRow label="Seats" subtext="Members, group members and user access rules across your organizations">
        <Text>{`${summary.seats_used} of ${summary.seat_limit} seats used`}</Text>
      </SettingsRow>
      {paid && (
        <SettingsRow label="Storage packs">
          <Text>{summary.storage_packs}</Text>
        </SettingsRow>
      )}
    </SettingsCard>
  )
}

function capitalize(value: string): string {
  return value ? value[0].toUpperCase() + value.slice(1) : value
}

function Stepper({
  label,
  value,
  min,
  onChange,
}: {
  label: string
  value: number
  min: number
  onChange: (value: number) => void
}) {
  return (
    <Stack direction="horizontal" gap="condensed" align="center">
      <IconButton
        icon={DashIcon}
        aria-label={`Decrease ${label}`}
        size="small"
        disabled={value <= min}
        onClick={() => onChange(Math.max(min, value - 1))}
      />
      <TextInput
        aria-label={label}
        type="number"
        min={min}
        value={String(value)}
        onChange={(event) => {
          const next = Number.parseInt(event.target.value, 10)
          onChange(Number.isFinite(next) ? Math.max(min, next) : min)
        }}
        style={{ width: 72, textAlign: 'center' }}
      />
      <IconButton icon={PlusIcon} aria-label={`Increase ${label}`} size="small" onClick={() => onChange(value + 1)} />
    </Stack>
  )
}

function usePaddle(config: BillingCapabilities) {
  const [paddle, setPaddle] = useState<Paddle | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let active = true
    loadPaddle(config)
      .then((instance) => active && setPaddle(instance))
      .catch(() => active && setFailed(true))
    return () => {
      active = false
    }
  }, [config])
  return { paddle, failed }
}

function usePricePreview(
  paddle: Paddle | null,
  config: BillingCapabilities,
  seats: number,
  packs: number,
  interval: BillingInterval,
) {
  const [lines, setLines] = useState<Record<string, PreviewLine> | null>(null)
  const [failed, setFailed] = useState(false)
  const key = interval === 'year' ? 'yearly' : 'monthly'
  const seatPrice = config.prices.seat[key]
  const storagePrice = config.prices.storage[key]

  useEffect(() => {
    if (!paddle) return
    let active = true
    const timer = setTimeout(() => {
      previewPrices(paddle, [
        { priceId: seatPrice, quantity: seats },
        { priceId: storagePrice, quantity: Math.max(packs, 1) },
      ])
        .then((result) => {
          if (!active) return
          setLines(result)
          setFailed(false)
        })
        .catch(() => active && setFailed(true))
    }, billingTiming.previewDebounceMs)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [paddle, seatPrice, storagePrice, seats, packs])

  return { seat: lines?.[seatPrice], storage: lines?.[storagePrice], failed }
}

export function PlanEditor({
  summary,
  config,
  onChanged,
}: {
  summary: BillingSummary
  config: BillingCapabilities
  onChanged: () => void
}) {
  const paid = isPaidPlan(summary)
  const minSeats = Math.max(summary.seats_used, 1)
  const initialSeats = Math.max(paid ? summary.seats || summary.seat_limit : minSeats, minSeats)
  const initialPacks = paid ? summary.storage_packs : 0
  const initialInterval: BillingInterval = summary.interval === 'year' ? 'year' : 'month'

  const [seats, setSeats] = useState(initialSeats)
  const [packs, setPacks] = useState(initialPacks)
  const [interval, setBillingInterval] = useState<BillingInterval>(initialInterval)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const { paddle, failed: paddleFailed } = usePaddle(config)
  const preview = usePricePreview(paddle, config, seats, packs, interval)

  useEffect(
    () =>
      subscribePaddleEvents((event) => {
        if (event.name === 'checkout.completed') {
          setPending(true)
          setBusy(false)
        } else if (event.name === 'checkout.closed') {
          setBusy(false)
        }
      }),
    [],
  )

  useEffect(() => {
    if (!pending) return
    let active = true
    let attempts = 0
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      attempts++
      try {
        const latest = await getMyBilling()
        if (!active) return
        if (latest && latest.plan && latest.plan !== 'free') {
          setPending(false)
          onChanged()
          return
        }
      } catch {
        // keep polling: the webhook may still be on its way
      }
      if (!active) return
      if (attempts >= billingTiming.pollAttempts) {
        setPending(false)
        setNotice('Payment received. Your plan updates as soon as the payment provider confirms it; reload this page in a minute.')
        return
      }
      timer = setTimeout(() => void poll(), billingTiming.pollMs)
    }
    timer = setTimeout(() => void poll(), billingTiming.pollMs)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [pending, onChanged])

  const unchanged = seats === initialSeats && packs === initialPacks && interval === initialInterval
  const items = { seats, storage_packs: packs, interval }

  async function upgrade() {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      const { transaction_id } = await createCheckout(items)
      const instance = await loadPaddle(config)
      instance.Checkout.open({ transactionId: transaction_id })
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  async function update() {
    setBusy(true)
    setError(null)
    try {
      await updateSubscription(items)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function manage() {
    setError(null)
    const tab = window.open('', '_blank')
    try {
      const { url } = await openBillingPortal()
      if (tab) {
        tab.opener = null
        tab.location.href = url
      } else {
        window.location.assign(url)
      }
    } catch (err) {
      tab?.close()
      setError(errorMessage(err))
    }
  }

  const per = interval === 'year' ? 'year' : 'month'

  return (
    <SettingsCard
      title={paid ? 'Change plan' : 'Upgrade'}
      description={
        paid
          ? 'Change seats, storage packs or the billing interval. Changes are prorated immediately.'
          : 'Pick the seats and extra storage you need. Each seat includes storage; packs add more.'
      }
      actions={
        paid ? (
          <Button size="small" trailingVisual={LinkExternalIcon} onClick={() => void manage()}>
            Manage billing
          </Button>
        ) : undefined
      }
    >
      <div style={{ padding: '0 var(--base-size-16)' }}>
        <Stack direction="vertical" gap="condensed">
          <ErrorBanner error={error} title={paid ? 'Could not update the plan' : 'Could not start checkout'} />
          {pending && (
            <Banner variant="info" title="Confirming your subscription">
              Waiting for the payment provider to confirm the payment…
            </Banner>
          )}
          {notice && (
            <Banner variant="info" title="Payment received" onDismiss={() => setNotice(null)}>
              {notice}
            </Banner>
          )}
        </Stack>
      </div>
      <SettingsRow label="Billing interval">
        <SegmentedControl
          aria-label="Billing interval"
          size="small"
          onChange={(index) => setBillingInterval(index === 1 ? 'year' : 'month')}
        >
          <SegmentedControl.Button selected={interval === 'month'}>Monthly</SegmentedControl.Button>
          <SegmentedControl.Button selected={interval === 'year'}>Yearly</SegmentedControl.Button>
        </SegmentedControl>
      </SettingsRow>
      <SettingsRow label="Seats" subtext={`At least ${minSeats}: seats in use`}>
        <Stepper label="seats" value={seats} min={minSeats} onChange={setSeats} />
        {preview.seat && (
          <Text as="p" style={{ margin: 'var(--base-size-8) 0 0', color: 'var(--fgColor-muted)' }}>
            {`${preview.seat.unit} per seat / ${per} · ${preview.seat.total}`}
          </Text>
        )}
      </SettingsRow>
      <SettingsRow label="Storage packs" subtext="Extra storage on top of the storage included with seats">
        <Stepper label="storage packs" value={packs} min={0} onChange={setPacks} />
        {preview.storage && (
          <Text as="p" style={{ margin: 'var(--base-size-8) 0 0', color: 'var(--fgColor-muted)' }}>
            {packs > 0
              ? `${preview.storage.unit} per pack / ${per} · ${preview.storage.total}`
              : `${preview.storage.unit} per pack / ${per}`}
          </Text>
        )}
      </SettingsRow>
      <SettingsRow label="Price">
        <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
          {paddleFailed || preview.failed
            ? 'Prices could not be loaded. The checkout shows the exact amount before you pay.'
            : preview.seat
              ? 'Estimated for your location; the final amount including tax is confirmed by the payment provider.'
              : 'Loading prices…'}
        </Text>
      </SettingsRow>
      <div
        style={{
          borderTop: '1px solid var(--borderColor-muted)',
          padding: 'var(--base-size-12) var(--base-size-16)',
          display: 'flex',
          justifyContent: 'flex-end',
        }}
      >
        {paid ? (
          <Button variant="primary" loading={busy} disabled={busy || unchanged} onClick={() => void update()}>
            Update plan
          </Button>
        ) : (
          <Button variant="primary" loading={busy || pending} disabled={busy || pending} onClick={() => void upgrade()}>
            Upgrade
          </Button>
        )}
      </div>
    </SettingsCard>
  )
}
