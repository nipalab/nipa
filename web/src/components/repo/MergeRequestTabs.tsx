import { UnderlineNav } from '@primer/react'

export interface TabItem {
  id: string
  label: string
  count?: number
}

export function MergeRequestTabs({
  tabs,
  active,
  onChange,
}: {
  tabs: TabItem[]
  active: string
  onChange: (id: string) => void
}) {
  return (
    <UnderlineNav aria-label="Merge request">
      {tabs.map((tab) => (
        <UnderlineNav.Item
          key={tab.id}
          as="button"
          aria-current={tab.id === active ? 'page' : undefined}
          counter={tab.count}
          onSelect={() => onChange(tab.id)}
        >
          {tab.label}
        </UnderlineNav.Item>
      ))}
    </UnderlineNav>
  )
}
