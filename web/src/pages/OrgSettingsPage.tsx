import { useState } from 'react'
import {
  ActionList,
  ActionMenu,
  Button,
  FormControl,
  IconButton,
  Label,
  Link as PrimerLink,
  Radio,
  RadioGroup,
  Stack,
  Text,
  Textarea,
  TextInput,
} from '@primer/react'
import {
  ArrowLeftIcon,
  DatabaseIcon,
  KebabHorizontalIcon,
  PeopleIcon,
  PersonAddIcon,
  PersonIcon,
  SearchIcon,
  ShieldIcon,
} from '@primer/octicons-react'
import { useParams, useSearchParams } from 'react-router-dom'
import {
  addGroupMember,
  addOrgMember,
  createGroup,
  getGroup,
  getOrgStorage,
  listGroups,
  removeGroupMember,
  removeOrgMember,
  updateOrgMember,
} from '../api/endpoints'
import type { GroupMemberResponse, OrgMemberResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { OrgNav } from '../components/OrgNav'
import { PickedSubject } from '../components/PickedSubject'
import { SettingsLayout, type SettingsNavItem } from '../components/SettingsLayout'
import StorageCard from '../components/StorageCard'
import { UserGroupPicker, type PickerEntry } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, NoPermission, Page } from '../components/ui'
import { useAction, useAsync } from '../hooks'
import { useOrgMembers, useOrgRole } from '../orgHooks'

type OrgTab = 'members' | 'groups' | 'storage'

function isOrgTab(value: string | null): value is OrgTab {
  return value === 'members' || value === 'groups' || value === 'storage'
}

export default function OrgSettingsPage() {
  const { org = '' } = useParams()
  const [params] = useSearchParams()
  const { canManage, loading: roleLoading } = useOrgRole(org)
  const { data: storage, reload: reloadStorage } = useAsync(() => getOrgStorage(org), [org])

  const requested = params.get('tab')
  const tab: OrgTab = isOrgTab(requested) && (requested !== 'storage' || storage !== null) ? requested : 'members'
  const base = `/${org}/settings`
  const items: SettingsNavItem[] = [
    { key: 'members', label: 'Members', to: base, icon: PersonIcon },
    { key: 'groups', label: 'Groups', to: `${base}?tab=groups`, icon: PeopleIcon },
    { key: 'storage', label: 'Storage', to: `${base}?tab=storage`, icon: DatabaseIcon, hidden: storage === null },
  ]
  const title = `${org} settings`

  if (roleLoading || !canManage) {
    return (
      <Page title={title} breadcrumbs={[{ label: org, to: `/${org}` }, { label: 'Settings' }]}>
        <NoPermission loading={roleLoading}>Only organization owners can manage members and groups.</NoPermission>
      </Page>
    )
  }

  return (
    <Page title={title} subtitle="Organization members, groups and storage">
      <OrgNav org={org} active="settings" canManage />
      <SettingsLayout items={items} active={tab} label="Organization settings">
        {tab === 'members' && <MembersTab org={org} />}
        {tab === 'groups' && <GroupsTab org={org} />}
        {tab === 'storage' && storage && <StorageCard org={org} data={storage} reload={reloadStorage} />}
      </SettingsLayout>
    </Page>
  )
}

type RoleFilter = 'all' | 'owner' | 'member'

function MembersTab({ org }: { org: string }) {
  const confirm = useConfirmAction()
  const { me } = useAuth()
  const { data: members, error, loading, reload } = useOrgMembers(org)
  const { run, error: actionError, clearError } = useAction(reload)
  const [adding, setAdding] = useState(false)
  const [query, setQuery] = useState('')
  const [role, setRole] = useState<RoleFilter>('all')

  async function toggleRole(member: OrgMemberResponse) {
    const next = member.role === 'owner' ? 'member' : 'owner'
    const ok = await confirm({
      title: `Make ${member.name} ${next === 'owner' ? 'an owner' : 'a member'}?`,
      content:
        next === 'owner'
          ? 'Owners can manage members, groups and every repository in this organization.'
          : 'They will lose owner access to this organization.',
      confirmLabel: next === 'owner' ? 'Make owner' : 'Make member',
      danger: false,
    })
    if (ok) await run(() => updateOrgMember(org, member.user_id, next))
  }

  async function remove(member: OrgMemberResponse) {
    const ok = await confirm({
      title: `Remove ${member.name} from ${org}?`,
      content: `${member.email} loses their membership and owner rights in ${org}.`,
      confirmLabel: 'Remove',
    })
    if (ok) await run(() => removeOrgMember(org, member.user_id))
  }

  const all = members ?? []
  const counts: Record<RoleFilter, number> = {
    all: all.length,
    owner: all.filter((member) => member.role === 'owner').length,
    member: all.filter((member) => member.role !== 'owner').length,
  }
  const needle = query.trim().toLowerCase()
  const visible = all
    .filter((member) => role === 'all' || (role === 'owner') === (member.role === 'owner'))
    .filter((member) => !needle || member.name.toLowerCase().includes(needle) || member.email.toLowerCase().includes(needle))
  const tabs: { key: RoleFilter; label: string }[] = [
    { key: 'all', label: 'All' },
    { key: 'owner', label: 'Owners' },
    { key: 'member', label: 'Members' },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <div className="nipa-section-head">
        <div>
          <Text as="h2" className="nipa-section-title">
            Members
          </Text>
          <Text as="p" className="nipa-section-desc">
            People in {org} and their roles. Repository access comes from owner rights or access rules.
          </Text>
        </div>
        <Button variant="primary" leadingVisual={PersonAddIcon} onClick={() => setAdding(true)}>
          Add member
        </Button>
      </div>
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <TextInput
        block
        leadingVisual={SearchIcon}
        aria-label="Find a member"
        placeholder="Find a member…"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="nipa-list-box">
        <div className="nipa-list-header">
          <nav className="nipa-state-tabs" aria-label="Member role">
            {tabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                aria-pressed={role === tab.key}
                className={role === tab.key ? 'nipa-state-tab nipa-state-tab-active' : 'nipa-state-tab'}
                onClick={() => setRole(tab.key)}
              >
                {tab.label}
                <span className="nipa-count">{counts[tab.key]}</span>
              </button>
            ))}
          </nav>
        </div>
        {loading && <Loading />}
        {!loading && visible.length === 0 && (
          <EmptyState bordered={false}>{all.length === 0 ? 'No members.' : 'No members match these filters.'}</EmptyState>
        )}
        {visible.map((member) => (
          <div key={member.user_id} className="nipa-list-row" style={{ alignItems: 'center' }}>
            <ActorAvatar actor={{ user_id: member.user_id, name: member.name, photo_url: member.photo_url }} size={32} />
            <div style={{ minWidth: 0, flex: 1 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span style={{ fontWeight: 600 }}>{member.name || member.email}</span>
                {me?.id === member.user_id && <Label variant="secondary">You</Label>}
                {member.is_super_admin && <Label variant="done">Super admin</Label>}
                {member.is_admin && !member.is_super_admin && <Label variant="accent">Admin</Label>}
              </div>
              <div className="nipa-list-row-meta">{member.email}</div>
            </div>
            <span className="nipa-role-text">{member.role === 'owner' ? 'Owner' : 'Member'}</span>
            <ActionMenu>
              <ActionMenu.Anchor>
                <IconButton icon={KebabHorizontalIcon} variant="invisible" aria-label={`Manage ${member.name || member.email}`} />
              </ActionMenu.Anchor>
              <ActionMenu.Overlay width="medium" align="end">
                <ActionList>
                  <ActionList.Item onSelect={() => void toggleRole(member)}>
                    <ActionList.LeadingVisual>
                      <ShieldIcon />
                    </ActionList.LeadingVisual>
                    {member.role === 'owner' ? 'Change role to member' : 'Change role to owner'}
                  </ActionList.Item>
                  <ActionList.Divider />
                  <ActionList.Item variant="danger" onSelect={() => void remove(member)}>
                    <ActionList.LeadingVisual>
                      <PersonIcon />
                    </ActionList.LeadingVisual>
                    Remove from organization
                  </ActionList.Item>
                </ActionList>
              </ActionMenu.Overlay>
            </ActionMenu>
          </div>
        ))}
      </div>
      {adding && <AddMemberDialog org={org} onClose={() => setAdding(false)} onAdded={reload} />}
    </Stack>
  )
}

function AvatarStack({ members }: { members: GroupMemberResponse[] }) {
  const shown = members.slice(0, 5)
  return (
    <span className="nipa-avatar-stack" aria-hidden="true">
      {shown.map((member) => (
        <ActorAvatar key={member.user_id} actor={{ user_id: member.user_id, name: member.name }} size={20} />
      ))}
    </span>
  )
}

function GroupsTab({ org }: { org: string }) {
  const confirm = useConfirmAction()
  const { data: groups, error, loading, reload: reloadGroups } = useAsync(() => listGroups(org), [org])
  const { data: members } = useOrgMembers(org)
  const [selected, setSelected] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const {
    data: detail,
    error: detailError,
    loading: detailLoading,
    reload: reloadDetail,
  } = useAsync(() => (selected ? getGroup(org, selected) : Promise.resolve(null)), [org, selected])
  const { run, error: actionError, clearError } = useAction(() => {
    reloadDetail()
    reloadGroups()
  })
  const [creating, setCreating] = useState(false)
  const [addingMember, setAddingMember] = useState(false)

  const membersById = new Map((members ?? []).map((member) => [member.user_id, member]))

  async function removeMember(userId: string) {
    if (!selected) return
    const label = (detail?.members ?? []).find((m) => m.user_id === userId)?.name ?? membersById.get(userId)?.name
    const ok = await confirm({
      title: `Remove ${label ?? `user ${userId}`} from ${detail?.name ?? 'this group'}?`,
      content: 'They lose the access granted through this group but stay in the organization.',
      confirmLabel: 'Remove',
    })
    if (ok) await run(() => removeGroupMember(org, selected, userId))
  }

  if (selected) {
    const groupMembers = detail && detail.id === selected ? (detail.members ?? []) : []
    return (
      <Stack direction="vertical" gap="normal">
        <PrimerLink as="button" className="nipa-back-link" onClick={() => setSelected(null)}>
          <ArrowLeftIcon /> All groups
        </PrimerLink>
        <ErrorBanner error={actionError ?? detailError} onDismiss={actionError ? clearError : undefined} />
        {detailLoading && !detail && <Loading variant="skeleton" />}
        {detail && detail.id === selected && (
          <>
            <div className="nipa-section-head">
              <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
                <span className="nipa-group-icon nipa-group-icon-large">
                  <PeopleIcon size={24} />
                </span>
                <div>
                  <Text as="h2" className="nipa-section-title">
                    {detail.name}
                  </Text>
                  <Text as="p" className="nipa-section-desc">
                    {detail.description || 'No description.'}
                  </Text>
                </div>
              </div>
              <Button variant="primary" leadingVisual={PersonAddIcon} onClick={() => setAddingMember(true)}>
                Add member
              </Button>
            </div>
            <div className="nipa-list-box">
              <div className="nipa-list-header">
                <Text style={{ fontWeight: 600 }}>
                  Members <span className="nipa-count">{groupMembers.length}</span>
                </Text>
              </div>
              {groupMembers.length === 0 && (
                <EmptyState bordered={false}>This group has no members yet. Add people to grant them its access in one step.</EmptyState>
              )}
              {groupMembers.map((member) => (
                <div key={member.user_id} className="nipa-list-row" style={{ alignItems: 'center' }}>
                  <ActorAvatar
                    actor={{ user_id: member.user_id, name: member.name, photo_url: membersById.get(member.user_id)?.photo_url }}
                    size={32}
                  />
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ fontWeight: 600 }}>{member.name}</div>
                    <div className="nipa-list-row-meta">{member.email}</div>
                  </div>
                  <Button size="small" onClick={() => void removeMember(member.user_id)}>
                    Remove
                  </Button>
                </div>
              ))}
            </div>
          </>
        )}
        {addingMember && (
          <AddGroupMemberDialog
            org={org}
            group={detail?.name ?? ''}
            onClose={() => setAddingMember(false)}
            onAdded={(userId) =>
              addGroupMember(org, selected, userId).then(() => {
                reloadDetail()
                reloadGroups()
              })
            }
          />
        )}
      </Stack>
    )
  }

  const needle = query.trim().toLowerCase()
  const visible = (groups ?? []).filter(
    (group) => !needle || group.name.toLowerCase().includes(needle) || group.description.toLowerCase().includes(needle),
  )

  return (
    <Stack direction="vertical" gap="normal">
      <div className="nipa-section-head">
        <div>
          <Text as="h2" className="nipa-section-title">
            Groups
          </Text>
          <Text as="p" className="nipa-section-desc">
            Group people to give them repository access together through access rules.
          </Text>
        </div>
        <Button variant="primary" leadingVisual={PeopleIcon} onClick={() => setCreating(true)}>
          New group
        </Button>
      </div>
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <TextInput
        block
        leadingVisual={SearchIcon}
        aria-label="Find a group"
        placeholder="Find a group…"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="nipa-list-box">
        <div className="nipa-list-header">
          <Text style={{ fontWeight: 600 }}>
            Groups <span className="nipa-count">{(groups ?? []).length}</span>
          </Text>
        </div>
        {loading && <Loading />}
        {!loading && visible.length === 0 && (
          <EmptyState bordered={false}>
            {(groups ?? []).length === 0 ? 'No groups. Create one to organize members.' : 'No groups match this search.'}
          </EmptyState>
        )}
        {visible.map((group) => (
          <div key={group.id} className="nipa-list-row" style={{ alignItems: 'center' }}>
            <span className="nipa-group-icon">
              <PeopleIcon />
            </span>
            <div style={{ minWidth: 0, flex: 1 }}>
              <PrimerLink as="button" className="nipa-list-row-title nipa-link-button" onClick={() => setSelected(group.id)}>
                {group.name}
              </PrimerLink>
              {group.description && <div className="nipa-list-row-meta">{group.description}</div>}
            </div>
            {group.members && group.members.length > 0 && <AvatarStack members={group.members} />}
            <span className="nipa-role-text">
              {group.member_count} {group.member_count === 1 ? 'member' : 'members'}
            </span>
          </div>
        ))}
      </div>
      {creating && <CreateGroupDialog org={org} onClose={() => setCreating(false)} onCreated={reloadGroups} />}
    </Stack>
  )
}

function RoleChoice({ value, onChange }: { value: string; onChange: (role: string) => void }) {
  return (
    <RadioGroup name="org-role">
      <RadioGroup.Label>Role</RadioGroup.Label>
      <FormControl>
        <Radio value="member" checked={value === 'member'} onChange={() => onChange('member')} />
        <FormControl.Label>Member</FormControl.Label>
        <FormControl.Caption>Sees the organization and works in the repositories access rules open to them.</FormControl.Caption>
      </FormControl>
      <FormControl>
        <Radio value="owner" checked={value === 'owner'} onChange={() => onChange('owner')} />
        <FormControl.Label>Owner</FormControl.Label>
        <FormControl.Caption>Full access to every repository and to members, groups and settings.</FormControl.Caption>
      </FormControl>
    </RadioGroup>
  )
}

function AddMemberDialog({ org, onClose, onAdded }: { org: string; onClose: () => void; onAdded: () => void }) {
  const [subject, setSubject] = useState<PickerEntry | null>(null)
  const [role, setRole] = useState('member')
  return (
    <FormDialog
      title={`Add a member to ${org}`}
      submitLabel="Add member"
      submitDisabled={!subject}
      onClose={onClose}
      onSubmit={async () => {
        if (!subject) return
        await addOrgMember(org, subject.id, role)
        onAdded()
      }}
    >
      <FormControl required>
        <FormControl.Label>User</FormControl.Label>
        {subject ? (
          <PickedSubject subject={subject} onChange={() => setSubject(null)} />
        ) : (
          <UserGroupPicker org={org} kind="user" inline onSelect={setSubject} placeholder="Search by name or email…" />
        )}
      </FormControl>
      <RoleChoice value={role} onChange={setRole} />
    </FormDialog>
  )
}

function CreateGroupDialog({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  return (
    <FormDialog
      title="Create a new group"
      submitLabel="Create group"
      submitDisabled={!name.trim()}
      onClose={onClose}
      onSubmit={async () => {
        await createGroup(org, name.trim(), description.trim())
        onCreated()
      }}
    >
      <FormControl required>
        <FormControl.Label>Group name</FormControl.Label>
        <TextInput
          block
          autoFocus
          leadingVisual={PeopleIcon}
          placeholder="e.g. texture-artists"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <FormControl.Caption>Use a name people recognize, such as a team or discipline.</FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Description</FormControl.Label>
        <Textarea
          block
          rows={3}
          placeholder="What is this group for?"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </FormControl>
    </FormDialog>
  )
}

function AddGroupMemberDialog({
  org,
  group,
  onClose,
  onAdded,
}: {
  org: string
  group: string
  onClose: () => void
  onAdded: (userId: string) => Promise<unknown>
}) {
  const [subject, setSubject] = useState<PickerEntry | null>(null)
  return (
    <FormDialog
      title={group ? `Add a member to ${group}` : 'Add group member'}
      submitLabel="Add member"
      submitDisabled={!subject}
      onClose={onClose}
      onSubmit={() => onAdded(subject?.id ?? '')}
    >
      <FormControl required>
        <FormControl.Label>User</FormControl.Label>
        {subject ? (
          <PickedSubject subject={subject} onChange={() => setSubject(null)} />
        ) : (
          <UserGroupPicker org={org} kind="user" inline onSelect={setSubject} placeholder="Search by name or email…" />
        )}
        <FormControl.Caption>Group members get every access rule granted to the group.</FormControl.Caption>
      </FormControl>
    </FormDialog>
  )
}
