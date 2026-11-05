import { UnderlineNav } from '@primer/react'
import { GearIcon, RepoIcon } from '@primer/octicons-react'
import { Link } from 'react-router-dom'

export type OrgTab = 'projects' | 'settings'

export function OrgNav({ org, active, canManage }: { org: string; active: OrgTab; canManage: boolean }) {
  if (!canManage) return null
  return (
    <UnderlineNav aria-label="Organization">
      <UnderlineNav.Item
        as={Link}
        to={`/${org}`}
        aria-current={active === 'projects' ? 'page' : undefined}
        leadingVisual={<RepoIcon />}
      >
        Repositories
      </UnderlineNav.Item>
      <UnderlineNav.Item
        as={Link}
        to={`/${org}/settings`}
        aria-current={active === 'settings' ? 'page' : undefined}
        leadingVisual={<GearIcon />}
      >
        Settings
      </UnderlineNav.Item>
    </UnderlineNav>
  )
}
