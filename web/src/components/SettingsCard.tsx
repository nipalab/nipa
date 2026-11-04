import type { CSSProperties, ReactNode } from 'react'
import { SectionHeading } from './ui'

export function Card({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        border: '1px solid var(--borderColor-default)',
        borderRadius: 'var(--borderRadius-medium)',
        overflow: 'hidden',
        ...style,
      }}
    >
      {children}
    </div>
  )
}

export function SettingsCard({
  title,
  description,
  actions,
  footer,
  children,
}: {
  title: string
  description?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  children?: ReactNode
}) {
  return (
    <Card>
      <div style={{ padding: 'var(--base-size-12) var(--base-size-16)', background: 'var(--bgColor-muted)' }}>
        <SectionHeading as="h2" description={description} actions={actions}>
          {title}
        </SectionHeading>
      </div>
      {children}
      {footer && (
        <div
          style={{
            borderTop: '1px solid var(--borderColor-default)',
            background: 'var(--bgColor-muted)',
            padding: 'var(--base-size-12) var(--base-size-16)',
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--base-size-8)',
          }}
        >
          {footer}
        </div>
      )}
    </Card>
  )
}

export function SettingsRow({
  label,
  subtext,
  children,
}: {
  label: ReactNode
  subtext?: ReactNode
  children?: ReactNode
}) {
  return (
    <div
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-start',
        gap: 'var(--base-size-16)',
        padding: 'var(--base-size-16)',
        borderTop: '1px solid var(--borderColor-muted)',
      }}
    >
      <div style={{ flex: '0 0 220px', minWidth: 0 }}>
        <div style={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{label}</div>
        {subtext && (
          <div style={{ color: 'var(--fgColor-muted)', fontSize: 12, overflowWrap: 'anywhere' }}>{subtext}</div>
        )}
      </div>
      {children !== undefined && <div style={{ flex: '1 1 300px', minWidth: 0 }}>{children}</div>}
    </div>
  )
}
