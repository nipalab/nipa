import type { ReactNode } from 'react'
import { CreditCardIcon, KeyIcon, LinkIcon, LockIcon, PersonIcon } from '@primer/octicons-react'
import { useAuth } from '../auth'
import { SettingsLayout } from './SettingsLayout'
import { Page } from './ui'

export type UserTab = 'profile' | 'password' | 'accounts' | 'keys' | 'billing'

export const USER_TABS: UserTab[] = ['profile', 'password', 'accounts', 'keys', 'billing']

const ITEMS = [
  { key: 'profile', label: 'Public profile', to: '/settings/profile', icon: PersonIcon },
  { key: 'password', label: 'Password', to: '/settings/profile?tab=password', icon: LockIcon },
  { key: 'accounts', label: 'Connected accounts', to: '/settings/profile?tab=accounts', icon: LinkIcon },
  { key: 'keys', label: 'SSH keys', to: '/settings/profile?tab=keys', icon: KeyIcon },
  { key: 'billing', label: 'Billing', to: '/settings/billing', icon: CreditCardIcon },
]

export function isUserTab(value: string | null): value is UserTab {
  return USER_TABS.some((tab) => tab === value)
}

export function UserSettingsLayout({ active, children }: { active: UserTab; children: ReactNode }) {
  const { me } = useAuth()
  return (
    <Page title="Account settings" subtitle={me?.email}>
      <SettingsLayout items={ITEMS} active={active} label="User settings">
        {children}
      </SettingsLayout>
    </Page>
  )
}
