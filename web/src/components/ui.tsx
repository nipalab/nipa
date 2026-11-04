import type { CSSProperties, ElementType, ReactNode } from 'react'
import { Banner, Breadcrumbs, Heading, PageHeader, Spinner, Stack, Text } from '@primer/react'
import { Blankslate, SkeletonText } from '@primer/react/experimental'
import { Link } from 'react-router-dom'

export interface Crumb {
  label: ReactNode
  to?: string
}

const PAGE_STYLE: CSSProperties = {
  maxWidth: 1100,
  margin: 'var(--base-size-24) auto var(--base-size-64)',
  padding: '0 var(--base-size-16)',
}

export function Page({
  title,
  subtitle,
  actions,
  breadcrumbs,
  children,
}: {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  breadcrumbs?: Crumb[]
  children: ReactNode
}) {
  return (
    <div style={PAGE_STYLE}>
      {breadcrumbs && breadcrumbs.length > 0 && (
        <nav aria-label="Breadcrumb" style={{ marginBottom: 'var(--base-size-8)' }}>
          <Breadcrumbs>
            {breadcrumbs.map((crumb, index) => {
              const last = index === breadcrumbs.length - 1
              return crumb.to && !last ? (
                <Breadcrumbs.Item key={index} as={Link} to={crumb.to}>
                  {crumb.label}
                </Breadcrumbs.Item>
              ) : (
                <Breadcrumbs.Item key={index} selected={last}>
                  {crumb.label}
                </Breadcrumbs.Item>
              )
            })}
          </Breadcrumbs>
        </nav>
      )}
      {(title || actions) && (
        <PageHeader>
          {title && (
            <PageHeader.TitleArea>
              <PageHeader.Title as="h2">{title}</PageHeader.Title>
            </PageHeader.TitleArea>
          )}
          {actions && <PageHeader.Actions>{actions}</PageHeader.Actions>}
          {subtitle && <PageHeader.Description>{subtitle}</PageHeader.Description>}
        </PageHeader>
      )}
      <Stack direction="vertical" gap="normal" style={{ marginTop: 'var(--base-size-16)' }}>
        {children}
      </Stack>
    </div>
  )
}

export function SectionHeading({
  children,
  description,
  actions,
  as = 'h3',
}: {
  children: ReactNode
  description?: ReactNode
  actions?: ReactNode
  as?: 'h2' | 'h3' | 'h4'
}) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--base-size-16)',
      }}
    >
      <div style={{ minWidth: 0 }}>
        <Heading as={as} style={{ margin: 0, fontSize: 'var(--text-title-size-small, 1rem)' }}>
          {children}
        </Heading>
        {description && (
          <Text as="p" style={{ margin: 'var(--base-size-4) 0 0', fontSize: 12, color: 'var(--fgColor-muted)' }}>
            {description}
          </Text>
        )}
      </div>
      {actions}
    </div>
  )
}

export function ErrorBanner({
  error,
  title = 'Request failed',
  onDismiss,
}: {
  error: string | null
  title?: string
  onDismiss?: () => void
}) {
  if (!error) return null
  return (
    <Banner variant="critical" title={title} onDismiss={onDismiss}>
      {error}
    </Banner>
  )
}

export function Loading({ variant = 'spinner', lines = 3 }: { variant?: 'spinner' | 'skeleton'; lines?: number }) {
  if (variant === 'skeleton') {
    return (
      <div role="status" aria-label="Loading" style={{ padding: 'var(--base-size-16)' }}>
        <SkeletonText lines={lines} />
      </div>
    )
  }
  return (
    <Stack direction="vertical" align="center" style={{ padding: 'var(--base-size-32)' }}>
      <Spinner size="large" />
    </Stack>
  )
}

export function EmptyState({
  children,
  description,
  icon: Icon,
  action,
}: {
  children: ReactNode
  description?: ReactNode
  icon?: ElementType
  action?: ReactNode
}) {
  return (
    <Blankslate border>
      {Icon && (
        <Blankslate.Visual>
          <Icon size="medium" />
        </Blankslate.Visual>
      )}
      <Blankslate.Heading>{children}</Blankslate.Heading>
      {description && <Blankslate.Description>{description}</Blankslate.Description>}
      {action}
    </Blankslate>
  )
}

export function NoPermission({ loading = false, children }: { loading?: boolean; children?: ReactNode }) {
  if (loading) return <Loading />
  return (
    <EmptyState description={children ?? 'Ask an administrator for access.'}>
      You don&apos;t have permission to do this
    </EmptyState>
  )
}

export function Mono({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return <span style={{ fontFamily: 'var(--fontStack-monospace)', ...style }}>{children}</span>
}

export function StatusLabel({ status }: { status: string }) {
  const color =
    status === 'merged' || status === 'mergeable'
      ? 'var(--fgColor-success)'
      : status === 'closed' || status === 'invalid'
        ? 'var(--fgColor-danger)'
        : status === 'behind_target'
          ? 'var(--fgColor-attention)'
          : 'var(--fgColor-muted)'
  return (
    <span style={{ color, fontWeight: 600, textTransform: 'capitalize' }}>{status.replace(/_/g, ' ')}</span>
  )
}
