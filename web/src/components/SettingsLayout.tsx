import type { ElementType, ReactNode } from 'react'
import { NavList } from '@primer/react'
import { Link } from 'react-router-dom'

export interface SettingsNavItem {
  key: string
  label: string
  to: string
  icon?: ElementType
  hidden?: boolean
}

export function SettingsLayout({
  items,
  active,
  label = 'Settings',
  children,
}: {
  items: SettingsNavItem[]
  active: string
  label?: string
  children: ReactNode
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--base-size-32)', flexWrap: 'wrap' }}>
      <nav aria-label={label} style={{ flex: '0 0 200px', minWidth: 0 }}>
        <NavList>
          {items
            .filter((item) => !item.hidden)
            .map((item) => (
              <NavList.Item key={item.key} as={Link} to={item.to} aria-current={item.key === active ? 'page' : undefined}>
                {item.icon && (
                  <NavList.LeadingVisual>
                    <item.icon />
                  </NavList.LeadingVisual>
                )}
                {item.label}
              </NavList.Item>
            ))}
        </NavList>
      </nav>
      <div style={{ flex: '1 1 480px', minWidth: 0 }}>{children}</div>
    </div>
  )
}
