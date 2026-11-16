import { useEffect, useState } from 'react'
import { Banner, Stack } from '@primer/react'
import { Link, useLocation } from 'react-router-dom'
import { getMyBilling, getOnboardingCapabilities } from '../api/endpoints'
import type { OwnerStorageResponse } from '../api/models'
import { usageAlerts, type UsageAlert } from '../billing/usage'

const DISMISSED_KEY = 'nipa.usage.dismissed'
const REFRESH_MS = 10 * 60 * 1000
const BILLING_PATH = '/settings/billing'

function readDismissed(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(DISMISSED_KEY) ?? '[]') as unknown
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

function writeDismissed(keys: string[]) {
  try {
    localStorage.setItem(DISMISSED_KEY, JSON.stringify(keys.slice(-20)))
  } catch {
    // storage unavailable: the dismissal lasts for this page only
  }
}

interface UsageState {
  data: OwnerStorageResponse | null
  canUpgrade: boolean
}

export function useUsage(enabled: boolean): UsageState {
  const [state, setState] = useState<UsageState>({ data: null, canUpgrade: false })

  useEffect(() => {
    if (!enabled) return
    let active = true
    async function load() {
      try {
        const data = (await Promise.resolve().then(() => getMyBilling())) ?? null
        if (!active) return
        if (usageAlerts(data).length === 0) {
          setState({ data, canUpgrade: false })
          return
        }
        const capabilities = (await Promise.resolve().then(() => getOnboardingCapabilities())) ?? null
        if (active) setState({ data, canUpgrade: Boolean(capabilities?.billing) })
      } catch {
        // usage hints are best effort
      }
    }
    void load()
    const timer = setInterval(() => void load(), REFRESH_MS)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [enabled])

  return state
}

export function UsageAlertBanner({
  alert,
  canUpgrade,
  onDismiss,
}: {
  alert: UsageAlert
  canUpgrade: boolean
  onDismiss?: () => void
}) {
  const action = alert.kind === 'seats_full' ? 'Add seats' : 'Add storage'
  return (
    <Banner variant={alert.variant} title={alert.title} onDismiss={onDismiss}>
      {alert.text}{' '}
      {canUpgrade ? (
        <Link to={BILLING_PATH}>{action} in billing settings</Link>
      ) : (
        'Contact your administrator to raise the limit.'
      )}
    </Banner>
  )
}

export function UsageBanner({ enabled }: { enabled: boolean }) {
  const { pathname } = useLocation()
  const { data, canUpgrade } = useUsage(enabled)
  const [dismissed, setDismissed] = useState<string[]>(readDismissed)
  if (pathname === BILLING_PATH) return null
  const alerts = usageAlerts(data).filter((alert) => !alert.dismissible || !dismissed.includes(alert.key))
  if (alerts.length === 0) return null

  function dismiss(alert: UsageAlert) {
    const next = [...dismissed, alert.key]
    setDismissed(next)
    writeDismissed(next)
  }

  return (
    <div style={{ padding: 'var(--base-size-8) var(--base-size-16) 0' }}>
      <Stack direction="vertical" gap="condensed">
        {alerts.map((alert) => (
          <UsageAlertBanner
            key={alert.key}
            alert={alert}
            canUpgrade={canUpgrade}
            onDismiss={alert.dismissible ? () => dismiss(alert) : undefined}
          />
        ))}
      </Stack>
    </div>
  )
}
