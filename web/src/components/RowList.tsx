import type { ReactNode } from 'react'
import { Card } from './SettingsCard'

export function RowList({ children }: { children: ReactNode }) {
  return <Card>{children}</Card>
}

export function Row({
  title,
  meta,
  actions,
  first = false,
}: {
  title: ReactNode
  meta?: ReactNode
  actions?: ReactNode
  first?: boolean
}) {
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: 'var(--base-size-16)',
        padding: 'var(--base-size-12) var(--base-size-16)',
        borderTop: first ? undefined : '1px solid var(--borderColor-muted)',
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 600 }}>{title}</div>
        {meta && <div style={{ color: 'var(--fgColor-muted)', fontSize: 13 }}>{meta}</div>}
      </div>
      {actions}
    </div>
  )
}
