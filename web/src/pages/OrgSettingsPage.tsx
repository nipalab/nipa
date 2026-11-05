import { useState } from 'react'
import { Button, FormControl, Label, Select, Stack, TextInput } from '@primer/react'
import { PeopleIcon, PersonIcon, DatabaseIcon } from '@primer/octicons-react'
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
import type { GroupResponse, OrgMemberResponse } from '../api/models'
import { ActorAvatar } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { DataTable, type Column } from '../components/DataTable'
import { FormDialog } from '../components/FormDialog'
import { OrgNav } from '../components/OrgNav'
import { Row } from '../components/RowList'
import { SettingsCard } from '../components/SettingsCard'
import { SettingsLayout, type SettingsNavItem } from '../components/SettingsLayout'
import StorageCard from '../components/StorageCard'
import { UserGroupPicker } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, Mono, NoPermission, Page } from '../components/ui'
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

function MembersTab({ org }: { org: string }) {
  const confirm = useConfirmAction()
  const { data: members, error, loading, reload } = useOrgMembers(org)
  const { run, error: actionError, clearError } = useAction(reload)
  const [adding, setAdding] = useState(false)

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
      title: 'Remove member',
      content: `Remove ${member.name} (${member.email}) from this organization?`,
      confirmLabel: 'Remove',
    })
    if (ok) await run(() => removeOrgMember(org, member.user_id))
  }

  const columns: Column<OrgMemberResponse & { id: string }>[] = [
    {
      id: 'member',
      header: 'Member',
      rowHeader: true,
      renderCell: (member) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--base-size-12)' }}>
          <ActorAvatar actor={{ user_id: member.user_id, name: member.name, photo_url: member.photo_url }} size={28} />
          <div>
            <div style={{ fontWeight: 600 }}>{member.name}</div>
            <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{member.email}</div>
          </div>
        </div>
      ),
    },
    {
      id: 'role',
      header: 'Role',
      renderCell: (member) => (
        <Stack direction="horizontal" gap="condensed" wrap="wrap">
          {member.role === 'owner' ? <Label variant="danger">Owner</Label> : <Label>Member</Label>}
          {member.is_super_admin && <Label variant="attention">Super admin</Label>}
          {member.is_admin && !member.is_super_admin && <Label variant="attention">Admin</Label>}
        </Stack>
      ),
    },
    {
      id: 'actions',
      header: 'Actions',
      align: 'end',
      renderCell: (member) => (
        <Stack direction="horizontal" gap="condensed" justify="end">
          <Button size="small" onClick={() => void toggleRole(member)}>
            {member.role === 'owner' ? 'Make member' : 'Make owner'}
          </Button>
          <Button size="small" variant="danger" onClick={() => void remove(member)}>
            Remove
          </Button>
        </Stack>
      ),
    },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Members"
        description="Manage who has access to this organization and their roles."
        actions={
          <Button size="small" variant="primary" onClick={() => setAdding(true)}>
            Add member
          </Button>
        }
      >
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      </SettingsCard>
      <DataTable
        label="Members"
        loading={loading}
        data={(members ?? []).map((member) => ({ ...member, id: member.user_id }))}
        columns={columns}
        empty="No members."
      />
      {adding && <AddMemberDialog org={org} onClose={() => setAdding(false)} onAdded={reload} />}
    </Stack>
  )
}

function GroupsTab({ org }: { org: string }) {
  const confirm = useConfirmAction()
  const { data: groups, error, loading, reload: reloadGroups } = useAsync(() => listGroups(org), [org])
  const { data: members } = useOrgMembers(org)
  const [selected, setSelected] = useState<string | null>(null)
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
      title: 'Remove group member',
      content: `Remove ${label ?? `user ${userId}`} from this group?`,
      confirmLabel: 'Remove',
    })
    if (ok) await run(() => removeGroupMember(org, selected, userId))
  }

  const columns: Column<GroupResponse>[] = [
    {
      id: 'group',
      header: 'Group',
      rowHeader: true,
      renderCell: (group) => (
        <div>
          <div style={{ fontWeight: 600 }}>{group.name}</div>
          {group.description && <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{group.description}</div>}
        </div>
      ),
    },
    { id: 'members', header: 'Members', renderCell: (group) => <Mono>{group.member_count}</Mono> },
    {
      id: 'actions',
      header: 'Actions',
      align: 'end',
      renderCell: (group) => (
        <Button size="small" onClick={() => setSelected(selected === group.id ? null : group.id)}>
          {selected === group.id ? 'Hide' : 'View'}
        </Button>
      ),
    },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Groups"
        description="Organize members into groups for easier access management."
        actions={
          <Button size="small" variant="primary" onClick={() => setCreating(true)}>
            Create group
          </Button>
        }
      >
        <ErrorBanner
          error={actionError ?? error ?? detailError}
          onDismiss={actionError ? clearError : undefined}
        />
      </SettingsCard>
      <DataTable
        label="Groups"
        loading={loading}
        data={groups ?? []}
        columns={columns}
        empty="No groups. Create one to organize members."
      />

      {selected && detail && detail.id === selected && (
        <SettingsCard
          title={`${detail.name} members`}
          actions={
            <Button size="small" variant="primary" onClick={() => setAddingMember(true)}>
              Add member
            </Button>
          }
        >
          {(detail.members ?? []).length === 0 && <EmptyState>No members.</EmptyState>}
          {(detail.members ?? []).map((member, index) => (
            <Row
              key={member.user_id}
              first={index === 0}
              title={
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--base-size-12)' }}>
                  <ActorAvatar
                    actor={{
                      user_id: member.user_id,
                      name: member.name,
                      photo_url: membersById.get(member.user_id)?.photo_url,
                    }}
                    size={28}
                  />
                  {member.name}
                </span>
              }
              meta={member.email}
              actions={
                <Button size="small" variant="danger" onClick={() => void removeMember(member.user_id)}>
                  Remove
                </Button>
              }
            />
          ))}
        </SettingsCard>
      )}
      {selected && !detail && detailLoading && <Loading variant="skeleton" />}

      {creating && <CreateGroupDialog org={org} onClose={() => setCreating(false)} onCreated={reloadGroups} />}
      {addingMember && selected && (
        <AddGroupMemberDialog
          org={org}
          onClose={() => setAddingMember(false)}
          onAdded={(userId) => addGroupMember(org, selected, userId).then(() => {
            reloadDetail()
            reloadGroups()
          })}
        />
      )}
    </Stack>
  )
}

function AddMemberDialog({ org, onClose, onAdded }: { org: string; onClose: () => void; onAdded: () => void }) {
  const [userId, setUserId] = useState('')
  const [role, setRole] = useState('member')
  return (
    <FormDialog
      title="Add member"
      submitLabel="Add member"
      submitDisabled={!userId}
      onClose={onClose}
      onSubmit={async () => {
        await addOrgMember(org, userId, role)
        onAdded()
      }}
    >
      <FormControl required>
        <FormControl.Label>User</FormControl.Label>
        <UserGroupPicker
          org={org}
          kind="user"
          onSelect={(entry) => setUserId(entry.id)}
          placeholder="Search users by name or email…"
        />
      </FormControl>
      <FormControl required>
        <FormControl.Label>Role</FormControl.Label>
        <Select block value={role} onChange={(event) => setRole(event.target.value)}>
          <Select.Option value="member">Member</Select.Option>
          <Select.Option value="owner">Owner</Select.Option>
        </Select>
      </FormControl>
    </FormDialog>
  )
}

function CreateGroupDialog({ org, onClose, onCreated }: { org: string; onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  return (
    <FormDialog
      title="Create group"
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
        <TextInput block autoFocus placeholder="e.g. texture-artists" value={name} onChange={(e) => setName(e.target.value)} />
      </FormControl>
      <FormControl>
        <FormControl.Label>Description</FormControl.Label>
        <TextInput
          block
          placeholder="Optional description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </FormControl>
    </FormDialog>
  )
}

function AddGroupMemberDialog({
  org,
  onClose,
  onAdded,
}: {
  org: string
  onClose: () => void
  onAdded: (userId: string) => Promise<unknown>
}) {
  const [userId, setUserId] = useState('')
  return (
    <FormDialog
      title="Add group member"
      submitLabel="Add member"
      submitDisabled={!userId}
      onClose={onClose}
      onSubmit={() => onAdded(userId)}
    >
      <FormControl required>
        <FormControl.Label>User</FormControl.Label>
        <UserGroupPicker
          org={org}
          kind="user"
          onSelect={(entry) => setUserId(entry.id)}
          placeholder="Search users by name or email…"
        />
      </FormControl>
    </FormDialog>
  )
}
