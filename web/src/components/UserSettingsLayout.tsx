import type { ReactNode } from 'react'
import {
  CreditCardIcon,
  DatabaseIcon,
  KeyIcon,
  LinkIcon,
  LockIcon,
  PersonIcon,
  ShieldLockIcon,
} from '@primer/octicons-react'
import { useAuth } from '../auth'
import { SettingsLayout } from './SettingsLayout'
import { useByoStorage } from './storage/storage'
import { Page } from './ui'

export type UserTab = 'profile' | 'password' | 'security' | 'accounts' | 'keys' | 'storage' | 'billing'

export const USER_TABS: UserTab[] = ['profile', 'password', 'security', 'accounts', 'keys', 'storage', 'billing']

const ITEMS = [
  { key: 'profile', label: 'Public profile', to: '/settings/profile', icon: PersonIcon },
  { key: 'password', label: 'Password', to: '/settings/profile?tab=password', icon: LockIcon },
  { key: 'security', label: 'Security', to: '/settings/profile?tab=security', icon: ShieldLockIcon },
  { key: 'accounts', label: 'Connected accounts', to: '/settings/profile?tab=accounts', icon: LinkIcon },
  { key: 'keys', label: 'SSH keys', to: '/settings/profile?tab=keys', icon: KeyIcon },
  { key: 'storage', label: 'Storage servers', to: '/settings/profile?tab=storage', icon: DatabaseIcon },
  { key: 'billing', label: 'Billing', to: '/settings/billing', icon: CreditCardIcon },
]

export function isUserTab(value: string | null): value is UserTab {
  return USER_TABS.some((tab) => tab === value)
}

export function UserSettingsLayout({ active, children }: { active: UserTab; children: ReactNode }) {
  const { me } = useAuth()
  const { enabled: byoStorage } = useByoStorage()
  const items = ITEMS.map((item) => (item.key === 'storage' ? { ...item, hidden: !byoStorage } : item))
  return (
    <Page title="Account settings" subtitle={me?.email}>
      <SettingsLayout items={items} active={active} label="User settings">
        {children}
      </SettingsLayout>
    </Page>
  )
}
