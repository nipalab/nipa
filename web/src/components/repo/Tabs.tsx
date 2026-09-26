import type { ReactNode } from 'react'

export interface TabItem {
  id: string
  label: string
  count?: number
}

export function Tabs({
  tabs,
  active,
  onChange,
}: {
  tabs: TabItem[]
  active: string
  onChange: (id: string) => void
}) {
  return (
    <nav
      style={{
        display: 'flex',
        gap: 4,
        borderBottom: '1px solid var(--borderColor-muted)',
      }}
    >
      {tabs.map((tab) => {
        const selected = tab.id === active
        return (
          <button
            key={tab.id}
            type="button"
            onClick={() => onChange(tab.id)}
            style={{
              border: 'none',
              borderBottom: selected ? '2px solid var(--fgColor-accent)' : '2px solid transparent',
              background: 'transparent',
              color: selected ? 'var(--fgColor-default)' : 'var(--fgColor-muted)',
              fontWeight: selected ? 600 : 400,
              padding: '8px 12px',
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
            }}
          >
            {tab.label}
            {tab.count !== undefined && <Counter>{tab.count}</Counter>}
          </button>
        )
      })}
    </nav>
  )
}

function Counter({ children }: { children: ReactNode }) {
  return (
    <span
      style={{
        background: 'var(--bgColor-neutral-muted)',
        borderRadius: 10,
        padding: '1px 7px',
        fontSize: 12,
        color: 'var(--fgColor-muted)',
      }}
    >
      {children}
    </span>
  )
}
