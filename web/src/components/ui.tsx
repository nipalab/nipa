import type { ReactNode } from 'react'
import { Banner, Heading, Spinner, Stack, Text } from '@primer/react'

export function Page({
  title,
  subtitle,
  actions,
  children,
}: {
  title?: string
  subtitle?: string
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <div style={{ maxWidth: 1100, margin: '24px auto 64px', padding: '0 16px' }}>
      {(title || actions) && (
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 16 }}>
          <div>
            {title && <Heading as="h2">{title}</Heading>}
            {subtitle && (
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                {subtitle}
              </Text>
            )}
          </div>
          {actions}
        </div>
      )}
      <Stack direction="vertical" gap="normal" style={{ marginTop: 16 }}>
        {children}
      </Stack>
    </div>
  )
}

export function ErrorBanner({ error }: { error: string | null }) {
  if (!error) return null
  return (
    <Banner variant="critical" title="Request failed">
      {error}
    </Banner>
  )
}

export function Loading() {
  return (
    <Stack direction="vertical" align="center" style={{ padding: 32 }}>
      <Spinner size="large" />
    </Stack>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return (
    <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
      {children}
    </Text>
  )
}

export function Mono({ children }: { children: ReactNode }) {
  return <span style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}>{children}</span>
}

export function StatusLabel({ status }: { status: string }) {
  const color =
    status === 'merged' || status === 'mergeable'
      ? 'var(--fgColor-success)'
      : status === 'closed' || status === 'conflicted'
        ? 'var(--fgColor-danger)'
        : status === 'behind_target'
          ? 'var(--fgColor-attention)'
          : 'var(--fgColor-muted)'
  return (
    <span style={{ color, fontWeight: 600, textTransform: 'capitalize' }}>{status.replace(/_/g, ' ')}</span>
  )
}
