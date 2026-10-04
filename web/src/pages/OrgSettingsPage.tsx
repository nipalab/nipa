import { useState } from 'react'
import { Button, Dialog, FormControl, Label, Stack, Text, TextInput } from '@primer/react'
import { Link, useParams } from 'react-router-dom'
import {
  addGroupMember,
  addOrgMember,
  createGroup,
  getGroup,
  listGroups,
  listOrgMembers,
  listOrgs,
  removeGroupMember,
  removeOrgMember,
  updateOrgMember,
} from '../api/endpoints'
import type { GroupResponse, OrgMemberResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/repo/ActorAvatar'
import { UserGroupPicker } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'

const CELL = { padding: '8px 12px' }
const HEADER_CELL = { ...CELL, fontWeight: 600, textAlign: 'left' as const, fontSize: 12, color: 'var(--fgColor-muted)' }

export default function OrgSettingsPage() {
  const { org = '' } = useParams()
  const { me } = useAuth()
  const { data: orgs } = useAsync(listOrgs, [])
  const { data: members, error, loading, reload } = useAsync(() => listOrgMembers(org), [org])
  const { data: groups, reload: reloadGroups } = useAsync(() => listGroups(org), [org])
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null)
  const { data: groupDetail, reload: reloadGroup } = useAsync(
    () => (selectedGroup ? getGroup(org, selectedGroup) : Promise.resolve(null)),
    [org, selectedGroup],
  )
  const [actionError, setActionError] = useState<string | null>(null)
  const [showAddMember, setShowAddMember] = useState(false)
  const [showCreateGroup, setShowCreateGroup] = useState(false)
  const [confirmRemoveMember, setConfirmRemoveMember] = useState<OrgMemberResponse | null>(null)
  const [confirmRemoveGroupMember, setConfirmRemoveGroupMember] = useState<string | null>(null)

  const myRole = orgs?.find((item) => item.slug === org)?.role
  const canManage = Boolean(me?.is_admin || me?.is_super_admin || myRole === 'owner')

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  if (!canManage) {
    return (
      <Page title={`${org} settings`} subtitle="Organization members and groups">
        <ErrorBanner error={error} />
        <Text>Only organization owners can manage members and groups.</Text>
        <Link to={`/${org}`}>back to repositories</Link>
      </Page>
    )
  }

  return (
    <Page
      title={`${org} settings`}
      subtitle="Organization members and groups"
      actions={<Link to={`/${org}`}>back to repositories</Link>}
    >
      <ErrorBanner error={actionError ?? error} />

      <MembersSection
        members={members ?? []}
        loading={loading}
        onAdd={() => setShowAddMember(true)}
        onRemove={(m) => setConfirmRemoveMember(m)}
        onToggleRole={(m) =>
          run(async () => {
            await updateOrgMember(org, m.user_id, m.role === 'owner' ? 'member' : 'owner')
            reload()
          })
        }
      />

      <GroupsSection
        org={org}
        groups={groups ?? []}
        members={members ?? []}
        selectedGroup={selectedGroup}
        groupDetail={groupDetail}
        onSelectGroup={setSelectedGroup}
        onCreate={() => setShowCreateGroup(true)}
        onAddMember={(userId) =>
          run(async () => {
            if (selectedGroup) {
              await addGroupMember(org, selectedGroup, userId)
              reloadGroup()
              reloadGroups()
            }
          })
        }
        onRemoveMember={(userId) => setConfirmRemoveGroupMember(userId)}
      />

      {showAddMember && (
        <AddMemberDialog
          org={org}
          onClose={() => setShowAddMember(false)}
          onAdded={() => {
            setShowAddMember(false)
            reload()
          }}
          onError={setActionError}
        />
      )}

      {showCreateGroup && (
        <CreateGroupDialog
          org={org}
          onClose={() => setShowCreateGroup(false)}
          onCreated={() => {
            setShowCreateGroup(false)
            reloadGroups()
          }}
          onError={setActionError}
        />
      )}

      <ConfirmRemoveMemberDialog
        member={confirmRemoveMember}
        onClose={() => setConfirmRemoveMember(null)}
        onRemoved={() => {
          setConfirmRemoveMember(null)
          reload()
        }}
        onError={setActionError}
      />

      <ConfirmRemoveGroupMemberDialog
        userId={confirmRemoveGroupMember}
        groupId={selectedGroup}
        label={
          (groupDetail?.members ?? []).find((m) => m.user_id === confirmRemoveGroupMember)?.name ??
          (members ?? []).find((m) => m.user_id === confirmRemoveGroupMember)?.name
        }
        onClose={() => setConfirmRemoveGroupMember(null)}
        onRemoved={() => {
          setConfirmRemoveGroupMember(null)
          reloadGroup()
          reloadGroups()
        }}
        onError={setActionError}
      />
    </Page>
  )
}

function MembersSection({
  members,
  loading,
  onAdd,
  onRemove,
  onToggleRole,
}: {
  members: OrgMemberResponse[]
  loading: boolean
  onAdd: () => void
  onRemove: (member: OrgMemberResponse) => void
  onToggleRole: (member: OrgMemberResponse) => void
}) {
  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Members</Text>
        <Button size="small" variant="primary" onClick={onAdd}>
          Add member
        </Button>
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Manage who has access to this organization and their roles.
      </Text>
      {loading && <Loading />}
      {!loading && members.length === 0 && <EmptyState>No members.</EmptyState>}
      {!loading && members.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)' }}>
                <th style={HEADER_CELL}>Member</th>
                <th style={HEADER_CELL}>Role</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {members.map((member) => (
                <tr key={member.user_id} style={{ borderTop: '1px solid var(--borderColor-muted)' }}>
                  <td style={CELL}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <ActorAvatar
                        actor={{ user_id: member.user_id, name: member.name, photo_url: member.photo_url }}
                        size={28}
                      />
                      <div>
                        <div style={{ fontWeight: 600 }}>{member.name}</div>
                        <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{member.email}</div>
                      </div>
                    </div>
                  </td>
                  <td style={CELL}>
                    {member.role === 'owner' ? (
                      <Label variant="danger">Owner</Label>
                    ) : (
                      <Label>Member</Label>
                    )}
                    {member.is_super_admin && (
                      <div style={{ marginTop: 4 }}>
                        <Label variant="attention">Super admin</Label>
                      </div>
                    )}
                    {member.is_admin && !member.is_super_admin && (
                      <div style={{ marginTop: 4 }}>
                        <Label variant="attention">Admin</Label>
                      </div>
                    )}
                  </td>
                  <td style={{ ...CELL, textAlign: 'right' }}>
                    <Stack direction="horizontal" gap="condensed" justify="end">
                      <Button size="small" onClick={() => onToggleRole(member)}>
                        {member.role === 'owner' ? 'Make member' : 'Make owner'}
                      </Button>
                      <Button size="small" variant="danger" onClick={() => onRemove(member)}>
                        Remove
                      </Button>
                    </Stack>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

function GroupsSection({
  org,
  groups,
  members,
  selectedGroup,
  groupDetail,
  onSelectGroup,
  onCreate,
  onAddMember,
  onRemoveMember,
}: {
  org: string
  groups: GroupResponse[]
  members: OrgMemberResponse[]
  selectedGroup: string | null
  groupDetail: GroupResponse | null
  onSelectGroup: (id: string | null) => void
  onCreate: () => void
  onAddMember: (userId: string) => void
  onRemoveMember: (userId: string) => void
}) {
  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Groups</Text>
        <Button size="small" variant="primary" onClick={onCreate}>
          Create group
        </Button>
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Organize members into groups for easier access management.
      </Text>
      {groups.length === 0 && <EmptyState>No groups. Create one to organize members.</EmptyState>}
      {groups.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)' }}>
                <th style={HEADER_CELL}>Group</th>
                <th style={HEADER_CELL}>Members</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((group) => (
                <tr
                  key={group.id}
                  style={{
                    borderTop: '1px solid var(--borderColor-muted)',
                    background: selectedGroup === group.id ? 'var(--bgColor-muted)' : 'transparent',
                  }}
                >
                  <td style={CELL}>
                    <button
                      type="button"
                      onClick={() => onSelectGroup(selectedGroup === group.id ? null : group.id)}
                      style={{
                        background: 'none',
                        border: 'none',
                        padding: 0,
                        color: 'var(--fgColor-accent)',
                        cursor: 'pointer',
                        fontWeight: 600,
                        fontSize: 14,
                      }}
                    >
                      {group.name}
                    </button>
                    {group.description && (
                      <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{group.description}</div>
                    )}
                  </td>
                  <td style={CELL}>
                    <Mono>{group.member_count}</Mono>
                  </td>
                  <td style={{ ...CELL, textAlign: 'right' }}>
                    <Button size="small" onClick={() => onSelectGroup(selectedGroup === group.id ? null : group.id)}>
                      {selectedGroup === group.id ? 'Hide' : 'View'}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selectedGroup && groupDetail && groupDetail.id === selectedGroup && (
        <GroupDetailPanel
          org={org}
          group={groupDetail}
          members={members}
          onAddMember={onAddMember}
          onRemoveMember={onRemoveMember}
        />
      )}
    </>
  )
}

function GroupDetailPanel({
  org,
  group,
  members,
  onAddMember,
  onRemoveMember,
}: {
  org: string
  group: GroupResponse
  members: OrgMemberResponse[]
  onAddMember: (userId: string) => void
  onRemoveMember: (userId: string) => void
}) {
  const [showAddMember, setShowAddMember] = useState(false)
  const membersById = new Map(members.map((member) => [member.user_id, member]))

  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h4">{group.name} members</Text>
        <Button size="small" variant="primary" onClick={() => setShowAddMember(true)}>
          Add member
        </Button>
      </div>
      <div style={{ margin: '12px 0' }}>
        {(group.members ?? []).map((member) => (
          <div
            key={member.user_id}
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              padding: '6px 0',
              borderBottom: '1px solid var(--borderColor-muted)',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <ActorAvatar
                actor={{ user_id: member.user_id, name: member.name, photo_url: membersById.get(member.user_id)?.photo_url }}
                size={28}
              />
              <div>
                <div style={{ fontWeight: 600 }}>{member.name}</div>
                <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{member.email}</div>
              </div>
            </div>
            <Button size="small" variant="danger" onClick={() => onRemoveMember(member.user_id)}>
              Remove
            </Button>
          </div>
        ))}
        {(group.members ?? []).length === 0 && <Text>No members.</Text>}
      </div>

      {showAddMember && (
        <AddGroupMemberDialog
          org={org}
          onClose={() => setShowAddMember(false)}
          onAdded={(userId) => {
            onAddMember(userId)
            setShowAddMember(false)
          }}
        />
      )}
    </div>
  )
}

function AddMemberDialog({
  org,
  onClose,
  onAdded,
  onError,
}: {
  org: string
  onClose: () => void
  onAdded: () => void
  onError: (msg: string) => void
}) {
  const [userId, setUserId] = useState('')
  const [role, setRole] = useState('member')
  const [saving, setSaving] = useState(false)

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!userId) return
    setSaving(true)
    try {
      await addOrgMember(org, userId, role)
      setUserId('')
      onAdded()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog title="Add member" onClose={onClose} width="medium">
      <Dialog.Body>
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
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
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                style={{ padding: 6, width: '100%' }}
              >
                <option value="member">Member</option>
                <option value="owner">Owner</option>
              </select>
            </FormControl>
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!userId || saving} onClick={handleSubmit}>
          {saving ? 'Adding…' : 'Add member'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function CreateGroupDialog({
  org,
  onClose,
  onCreated,
  onError,
}: {
  org: string
  onClose: () => void
  onCreated: () => void
  onError: (msg: string) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)

  function reset() {
    setName('')
    setDescription('')
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!name.trim()) return
    setSaving(true)
    try {
      await createGroup(org, name.trim(), description.trim())
      reset()
      onCreated()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog title="Create group" onClose={onClose} width="medium">
      <Dialog.Body>
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
            <FormControl required>
              <FormControl.Label>Group name</FormControl.Label>
              <TextInput
                block
                placeholder="e.g. texture-artists"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
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
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!name.trim() || saving} onClick={handleSubmit}>
          {saving ? 'Creating…' : 'Create group'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function AddGroupMemberDialog({
  org,
  onClose,
  onAdded,
}: {
  org: string
  onClose: () => void
  onAdded: (userId: string) => void
}) {
  const [userId, setUserId] = useState('')

  function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!userId) return
    onAdded(userId)
  }

  return (
    <Dialog title="Add group member" onClose={onClose} width="medium">
      <Dialog.Body>
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
            <FormControl required>
              <FormControl.Label>User</FormControl.Label>
              <UserGroupPicker
                org={org}
                kind="user"
                onSelect={(entry) => setUserId(entry.id)}
                placeholder="Search users by name or email…"
              />
            </FormControl>
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!userId} onClick={handleSubmit}>
          Add member
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function ConfirmRemoveMemberDialog({
  member,
  onClose,
  onRemoved,
  onError,
}: {
  member: OrgMemberResponse | null
  onClose: () => void
  onRemoved: () => void
  onError: (msg: string) => void
}) {
  const [removing, setRemoving] = useState(false)
  const { org = '' } = useParams()

  async function handleRemove() {
    if (!member) return
    setRemoving(true)
    try {
      await removeOrgMember(org, member.user_id)
      onRemoved()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setRemoving(false)
    }
  }

  if (!member) return null
  return (
    <Dialog title="Remove member" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Remove <strong>{member.name}</strong> ({member.email}) from this organization?
        </Text>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="danger" disabled={removing} onClick={handleRemove}>
          {removing ? 'Removing…' : 'Remove'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function ConfirmRemoveGroupMemberDialog({
  userId,
  groupId,
  label,
  onClose,
  onRemoved,
  onError,
}: {
  userId: string | null
  groupId: string | null
  label?: string
  onClose: () => void
  onRemoved: () => void
  onError: (msg: string) => void
}) {
  const [removing, setRemoving] = useState(false)
  const { org = '' } = useParams()

  async function handleRemove() {
    if (!userId || !groupId) return
    setRemoving(true)
    try {
      await removeGroupMember(org, groupId, userId)
      onRemoved()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setRemoving(false)
    }
  }

  if (!userId) return null
  return (
    <Dialog title="Remove group member" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Remove {label ? <strong>{label}</strong> : 'user'} <Mono>{userId}</Mono> from this group?
        </Text>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="danger" disabled={removing} onClick={handleRemove}>
          {removing ? 'Removing…' : 'Remove'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
