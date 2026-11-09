import { ActionList, ActionMenu, Text } from '@primer/react'
import { CreditCardIcon, HomeIcon, PeopleIcon, PersonIcon, SignOutIcon, TriangleDownIcon } from '@primer/octicons-react'
import { useNavigate } from 'react-router-dom'
import type { UserResponse } from '../api/models'
import { APP_HOME } from '../routes'
import { ActorAvatar } from './ActorAvatar'
import './UserMenu.css'

// UserMenu is the avatar button at the right of the header; like GitHub's,
// it opens a menu with the account links and sign out.
export function UserMenu({ me, onSignOut }: { me: UserResponse | null; onSignOut: () => void }) {
  const navigate = useNavigate()
  const actor = me ? { user_id: me.id, name: me.name, photo_url: me.photo_url } : undefined
  const admin = Boolean(me?.is_admin || me?.is_super_admin)

  return (
    <ActionMenu>
      <ActionMenu.Anchor>
        <button type="button" className="nipa-user-menu-button" aria-label="Open user navigation menu">
          <ActorAvatar actor={actor} size={32} />
          <TriangleDownIcon size={16} />
        </button>
      </ActionMenu.Anchor>
      <ActionMenu.Overlay width="medium" align="end">
        <div className="nipa-user-menu-header">
          <ActorAvatar actor={actor} size={32} />
          <div style={{ minWidth: 0 }}>
            <Text as="div" style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {me?.name ?? 'Signed in'}
            </Text>
            {me?.email && (
              <Text
                as="div"
                style={{ fontSize: 12, color: 'var(--fgColor-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
              >
                {me.email}
              </Text>
            )}
          </div>
        </div>
        <ActionList>
          <ActionList.Item onSelect={() => navigate(APP_HOME)}>
            <ActionList.LeadingVisual>
              <HomeIcon />
            </ActionList.LeadingVisual>
            Dashboard
          </ActionList.Item>
          <ActionList.Item onSelect={() => navigate('/settings/profile')}>
            <ActionList.LeadingVisual>
              <PersonIcon />
            </ActionList.LeadingVisual>
            Your profile
          </ActionList.Item>
          <ActionList.Item onSelect={() => navigate('/settings/billing')}>
            <ActionList.LeadingVisual>
              <CreditCardIcon />
            </ActionList.LeadingVisual>
            Billing and plans
          </ActionList.Item>
          {admin && (
            <>
              <ActionList.Divider />
              <ActionList.Group>
                <ActionList.GroupHeading>Site administration</ActionList.GroupHeading>
                <ActionList.Item onSelect={() => navigate('/admin/users')}>
                  <ActionList.LeadingVisual>
                    <PeopleIcon />
                  </ActionList.LeadingVisual>
                  Users
                </ActionList.Item>
              </ActionList.Group>
            </>
          )}
          <ActionList.Divider />
          <ActionList.Item onSelect={onSignOut}>
            <ActionList.LeadingVisual>
              <SignOutIcon />
            </ActionList.LeadingVisual>
            Sign out
          </ActionList.Item>
        </ActionList>
      </ActionMenu.Overlay>
    </ActionMenu>
  )
}
