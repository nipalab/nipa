import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, TextInput } from '@primer/react'
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
import { useAuth } from '../auth'
import { EmptyState, ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function OrgSettingsPage() {
  const { org = '' } = useParams()
  const { me } = useAuth()
  const { data: orgs } = useAsync(listOrgs, [])
  const { data: members, error, loading, reload } = useAsync(() => listOrgMembers(org), [org])
  const { data: groups, reload: reloadGroups } = useAsync(() => listGroups(org), [org])
  const [identifier, setIdentifier] = useState('')
  const [role, setRole] = useState('member')
  const [groupName, setGroupName] = useState('')
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null)
  const { data: groupDetail, reload: reloadGroup } = useAsync(
    () => (selectedGroup ? getGroup(org, selectedGroup) : Promise.resolve(null)),
    [org, selectedGroup],
  )
  const [groupUser, setGroupUser] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

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
        <PrimerLink as={Link} to={`/${org}`}>back to repositories</PrimerLink>
      </Page>
    )
  }

  return (
    <Page
      title={`${org} settings`}
      subtitle="Organization members and groups"
      actions={<PrimerLink as={Link} to={`/${org}`}>back to repositories</PrimerLink>}
    >
      <ErrorBanner error={actionError ?? error} />

      <Text as="h3">Members</Text>
      {loading && <Loading />}
      {!loading && members && members.length === 0 && <EmptyState>No members.</EmptyState>}
      {members?.map((member) => (
        <div
          key={member.user_id}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            borderBottom: '1px solid var(--borderColor-muted)',
            padding: '6px 0',
          }}
        >
          <span>
            {member.name} · {member.email} · role: {member.role}
            {member.is_super_admin ? ' · superadmin' : member.is_admin ? ' · admin' : ''}
          </span>
          <Stack direction="horizontal" gap="condensed">
            <Button
              size="small"
              onClick={() =>
                run(async () => {
                  await updateOrgMember(org, member.user_id, member.role === 'owner' ? 'member' : 'owner')
                  reload()
                })
              }
            >
              {member.role === 'owner' ? 'Make member' : 'Make owner'}
            </Button>
            <Button
              size="small"
              variant="danger"
              onClick={() =>
                run(async () => {
                  await removeOrgMember(org, member.user_id)
                  reload()
                })
              }
            >
              Remove
            </Button>
          </Stack>
        </div>
      ))}

      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await addOrgMember(org, identifier, role)
            setIdentifier('')
            reload()
          })
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Add member</strong>
          <FormControl required>
            <FormControl.Label>User id (base36) or email</FormControl.Label>
            <TextInput block value={identifier} onChange={(event) => setIdentifier(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Role</FormControl.Label>
            <select value={role} onChange={(event) => setRole(event.target.value)} style={{ padding: 6 }}>
              <option value="member">member</option>
              <option value="owner">owner</option>
            </select>
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Add member
          </Button>
        </Stack>
      </form>

      <Text as="h3">Groups</Text>
      {groups?.map((group) => (
        <div
          key={group.id}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            borderBottom: '1px solid var(--borderColor-muted)',
            padding: '6px 0',
          }}
        >
          <span>
            <button
              type="button"
              onClick={() => setSelectedGroup(group.id)}
              style={{ background: 'none', border: 'none', padding: 0, color: 'var(--fgColor-accent)', cursor: 'pointer' }}
            >
              {group.name}
            </button>
            {group.description ? ` · ${group.description}` : ''} · <Mono>{group.id}</Mono>
          </span>
        </div>
      ))}
      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await createGroup(org, groupName, '')
            setGroupName('')
            reloadGroups()
          })
        }}
        style={{ display: 'flex', gap: 8 }}
      >
        <TextInput
          placeholder="New group name"
          value={groupName}
          onChange={(event) => setGroupName(event.target.value)}
        />
        <Button type="submit">Create group</Button>
      </form>

      {selectedGroup && groupDetail && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}>
          <strong>{groupDetail.name} members</strong>
          <div style={{ margin: '8px 0' }}>
            {(groupDetail.member_ids ?? []).map((member) => (
              <div key={member} style={{ display: 'flex', justifyContent: 'space-between', padding: '4px 0' }}>
                <Mono>{member}</Mono>
                <Button
                  size="small"
                  variant="danger"
                  onClick={() =>
                    run(async () => {
                      await removeGroupMember(org, selectedGroup, member)
                      reloadGroup()
                    })
                  }
                >
                  Remove
                </Button>
              </div>
            ))}
            {(groupDetail.member_ids ?? []).length === 0 && <Text>No members.</Text>}
          </div>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              run(async () => {
                await addGroupMember(org, selectedGroup, groupUser)
                setGroupUser('')
                reloadGroup()
              })
            }}
            style={{ display: 'flex', gap: 8 }}
          >
            <TextInput
              placeholder="User id (base36)"
              value={groupUser}
              onChange={(event) => setGroupUser(event.target.value)}
            />
            <Button type="submit">Add member</Button>
          </form>
        </div>
      )}
    </Page>
  )
}
