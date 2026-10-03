import { useState } from 'react'
import { Button, Dialog, FormControl, Label, Stack, Text, TextInput } from '@primer/react'
import { useParams } from 'react-router-dom'
import {
  createProjectRule,
  deleteProjectDefault,
  deleteProjectRule,
  listProjectDefaults,
  listProjectRules,
  setBranchProtection,
  setProjectDefault,
} from '../api/endpoints'
import type { PBACRuleResponse, PermissionEntry } from '../api/models'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { WebhookSettings } from '../components/repo/WebhookSettings'
import { PermissionBadge } from '../components/PermissionBadge'
import { PermissionCheckboxes } from '../components/PermissionCheckboxes'
import { UserGroupPicker } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'
import { useAsync } from '../hooks'

const CELL = { padding: '8px 12px' }
const HEADER_CELL = { ...CELL, fontWeight: 600, textAlign: 'left' as const, fontSize: 12, color: 'var(--fgColor-muted)' }

export default function ProjectSettingsPage() {
  const { org = '', project = '' } = useParams()
  const { branches, reloadBranches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)

  const { data: rules, error: rulesError, loading: rulesLoading, reload: reloadRules } = useAsync(
    () => listProjectRules(org, project),
    [org, project],
  )
  const { data: defaults, reload: reloadDefaults } = useAsync(
    () => listProjectDefaults(org, project),
    [org, project],
  )

  const [actionError, setActionError] = useState<string | null>(null)
  const [showAddRule, setShowAddRule] = useState(false)
  const [showAddDefault, setShowAddDefault] = useState(false)
  const [editingDefault, setEditingDefault] = useState<PermissionEntry | null>(null)
  const [confirmDeleteRule, setConfirmDeleteRule] = useState<PBACRuleResponse | null>(null)
  const [confirmDeleteDefault, setConfirmDeleteDefault] = useState<PermissionEntry | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  if (!canAdmin) {
    return (
      <RepoPageShell
        org={org}
        project={project}
        active="settings"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="Settings"
      >
        <ErrorBanner error={rulesError} />
        <Text>You need project admin permission to manage this repository.</Text>
      </RepoPageShell>
    )
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="settings"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Settings"
    >
      <ErrorBanner error={actionError ?? rulesError} />

      <Text as="h3">Branch protection</Text>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          {branches?.map((branch) => (
            <tr key={branch.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
              <td style={CELL}>
                {branch.name}
                {branch.is_default && <span style={{ color: 'var(--fgColor-accent)' }}> · default</span>}
              </td>
              <td style={{ ...CELL, textAlign: 'right' }}>
                <Button
                  size="small"
                  onClick={() =>
                    run(async () => {
                      await setBranchProtection(org, project, branch.name, !branch.is_protected)
                      reloadBranches()
                    })
                  }
                >
                  {branch.is_protected ? 'Unprotect' : 'Protect'}
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <AccessRulesSection
        rules={rules ?? []}
        loading={rulesLoading}
        onAdd={() => setShowAddRule(true)}
        onDelete={(rule) => setConfirmDeleteRule(rule)}
      />

      <PathDefaultsSection
        defaults={defaults ?? []}
        onAdd={() => {
          setEditingDefault(null)
          setShowAddDefault(true)
        }}
        onEdit={(entry) => {
          setEditingDefault(entry)
          setShowAddDefault(true)
        }}
        onDelete={(entry) => setConfirmDeleteDefault(entry)}
      />

      {showAddRule && (
        <AddRuleDialog
          org={org}
          project={project}
          onClose={() => setShowAddRule(false)}
          onCreated={() => {
            setShowAddRule(false)
            reloadRules()
          }}
          onError={setActionError}
        />
      )}

      {showAddDefault && (
        <AddDefaultDialog
          org={org}
          project={project}
          editing={editingDefault}
          onClose={() => setShowAddDefault(false)}
          onSaved={() => {
            setShowAddDefault(false)
            reloadDefaults()
          }}
          onError={setActionError}
        />
      )}

      <ConfirmDeleteRuleDialog
        rule={confirmDeleteRule}
        onClose={() => setConfirmDeleteRule(null)}
        onDeleted={() => {
          setConfirmDeleteRule(null)
          reloadRules()
        }}
        onError={setActionError}
      />

      <ConfirmDeleteDefaultDialog
        entry={confirmDeleteDefault}
        onClose={() => setConfirmDeleteDefault(null)}
        onDeleted={() => {
          setConfirmDeleteDefault(null)
          reloadDefaults()
        }}
        onError={setActionError}
      />

      <WebhookSettings org={org} project={project} />
    </RepoPageShell>
  )
}

function AccessRulesSection({
  rules,
  loading,
  onAdd,
  onDelete,
}: {
  rules: PBACRuleResponse[]
  loading: boolean
  onAdd: () => void
  onDelete: (rule: PBACRuleResponse) => void
}) {
  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Access rules</Text>
        <Button size="small" variant="primary" onClick={onAdd}>
          Add access rule
        </Button>
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Grant specific users or groups access to paths in this repository.
      </Text>
      {loading && <Loading />}
      {!loading && rules.length === 0 && (
        <EmptyState>No access rules. Add one to grant specific users or groups access.</EmptyState>
      )}
      {!loading && rules.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)' }}>
                <th style={HEADER_CELL}>User / Group</th>
                <th style={HEADER_CELL}>Path</th>
                <th style={HEADER_CELL}>Permissions</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id} style={{ borderTop: '1px solid var(--borderColor-muted)' }}>
                  <td style={CELL}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Label>{rule.user_id ? 'User' : 'Group'}</Label>
                      <Mono>{rule.user_id ?? rule.group_id}</Mono>
                    </div>
                  </td>
                  <td style={CELL}>
                    <Mono>{rule.path_prefix || '/'}</Mono>
                  </td>
                  <td style={CELL}>
                    <PermissionBadge permission={rule.permission} />
                  </td>
                  <td style={{ ...CELL, textAlign: 'right' }}>
                    <Button size="small" variant="danger" onClick={() => onDelete(rule)}>
                      Revoke
                    </Button>
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

function PathDefaultsSection({
  defaults,
  onAdd,
  onEdit,
  onDelete,
}: {
  defaults: PermissionEntry[]
  onAdd: () => void
  onEdit: (entry: PermissionEntry) => void
  onDelete: (entry: PermissionEntry) => void
}) {
  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text as="h3">Path defaults</Text>
        <Button size="small" variant="primary" onClick={onAdd}>
          Add default
        </Button>
      </div>
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        Default permissions for paths without a matching access rule.
      </Text>
      {defaults.length === 0 && (
        <EmptyState>No path defaults. The repository is accessible to all members by default.</EmptyState>
      )}
      {defaults.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ background: 'var(--bgColor-muted)' }}>
                <th style={HEADER_CELL}>Path</th>
                <th style={HEADER_CELL}>Permissions</th>
                <th style={{ ...HEADER_CELL, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {defaults.map((entry) => (
                <tr key={entry.path_prefix} style={{ borderTop: '1px solid var(--borderColor-muted)' }}>
                  <td style={CELL}>
                    <Mono>{entry.path_prefix || '/'}</Mono>
                  </td>
                  <td style={CELL}>
                    <PermissionBadge permission={entry.permission} />
                  </td>
                  <td style={{ ...CELL, textAlign: 'right' }}>
                    <Stack direction="horizontal" gap="condensed" justify="end">
                      <Button size="small" onClick={() => onEdit(entry)}>
                        Edit
                      </Button>
                      <Button size="small" variant="danger" onClick={() => onDelete(entry)}>
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

function AddRuleDialog({
  org,
  project,
  onClose,
  onCreated,
  onError,
}: {
  org: string
  project: string
  onClose: () => void
  onCreated: () => void
  onError: (msg: string) => void
}) {
  const [subject, setSubject] = useState<{ id: string; label: string; isGroup: boolean } | null>(null)
  const [prefix, setPrefix] = useState('')
  const [permission, setPermission] = useState(0)
  const [saving, setSaving] = useState(false)

  function reset() {
    setSubject(null)
    setPrefix('')
    setPermission(0)
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!subject || permission === 0) return
    setSaving(true)
    try {
      await createProjectRule(
        org,
        project,
        subject.isGroup ? '' : subject.id,
        subject.isGroup ? subject.id : '',
        prefix,
        permission,
      )
      reset()
      onCreated()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog title="Add access rule" onClose={onClose} width="medium">
      <Dialog.Body>
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
            <FormControl required>
              <FormControl.Label>User or group</FormControl.Label>
              <UserGroupPicker
                org={org}
                kind="user-or-group"
                onSelect={(entry) => setSubject({ id: entry.id, label: entry.label, isGroup: entry.isGroup })}
                placeholder="Search users or groups…"
              />
              {subject && (
                <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
                  Selected: {subject.label} · {subject.isGroup ? 'group' : 'user'} · <Mono>{subject.id}</Mono>
                </Text>
              )}
            </FormControl>
            <FormControl>
              <FormControl.Label>Path prefix</FormControl.Label>
              <TextInput
                block
                placeholder="e.g. assets/textures (empty = whole repository)"
                value={prefix}
                onChange={(e) => setPrefix(e.target.value)}
              />
              <FormControl.Caption>Leave empty to apply to the entire repository.</FormControl.Caption>
            </FormControl>
            <FormControl required>
              <FormControl.Label>Permissions</FormControl.Label>
              <PermissionCheckboxes value={permission} onChange={setPermission} />
            </FormControl>
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          disabled={!subject || permission === 0 || saving}
          onClick={handleSubmit}
        >
          {saving ? 'Adding…' : 'Add rule'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function AddDefaultDialog({
  org,
  project,
  editing,
  onClose,
  onSaved,
  onError,
}: {
  org: string
  project: string
  editing: PermissionEntry | null
  onClose: () => void
  onSaved: () => void
  onError: (msg: string) => void
}) {
  const [prefix, setPrefix] = useState(editing?.path_prefix ?? '')
  const [permission, setPermission] = useState(editing?.permission ?? 0)
  const [saving, setSaving] = useState(false)

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (permission === 0) return
    setSaving(true)
    try {
      await setProjectDefault(org, project, prefix, permission)
      onSaved()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog title={editing ? 'Edit path default' : 'Add path default'} onClose={onClose} width="medium">
      <Dialog.Body>
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
            <FormControl>
              <FormControl.Label>Path prefix</FormControl.Label>
              <TextInput
                block
                placeholder="e.g. assets/textures (empty = whole repository)"
                value={prefix}
                onChange={(e) => setPrefix(e.target.value)}
              />
              <FormControl.Caption>Leave empty to apply to the entire repository.</FormControl.Caption>
            </FormControl>
            <FormControl required>
              <FormControl.Label>Permissions</FormControl.Label>
              <PermissionCheckboxes value={permission} onChange={setPermission} />
            </FormControl>
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={permission === 0 || saving} onClick={handleSubmit}>
          {saving ? 'Saving…' : editing ? 'Save' : 'Add default'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function ConfirmDeleteRuleDialog({
  rule,
  onClose,
  onDeleted,
  onError,
}: {
  rule: PBACRuleResponse | null
  onClose: () => void
  onDeleted: () => void
  onError: (msg: string) => void
}) {
  const [deleting, setDeleting] = useState(false)
  const { org = '', project = '' } = useParams()

  async function handleDelete() {
    if (!rule) return
    setDeleting(true)
    try {
      await deleteProjectRule(org, project, rule.id)
      onDeleted()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setDeleting(false)
    }
  }

  if (!rule) return null
  return (
    <Dialog title="Revoke access rule" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Revoke access for{' '}
          <strong>
            {rule.user_id ? `user: ${rule.user_id}` : `group: ${rule.group_id}`}
          </strong>{' '}
          on path <Mono>{rule.path_prefix || '/'}</Mono>?
        </Text>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="danger" disabled={deleting} onClick={handleDelete}>
          {deleting ? 'Revoking…' : 'Revoke'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function ConfirmDeleteDefaultDialog({
  entry,
  onClose,
  onDeleted,
  onError,
}: {
  entry: PermissionEntry | null
  onClose: () => void
  onDeleted: () => void
  onError: (msg: string) => void
}) {
  const [deleting, setDeleting] = useState(false)
  const { org = '', project = '' } = useParams()

  async function handleDelete() {
    if (!entry) return
    setDeleting(true)
    try {
      await deleteProjectDefault(org, project, entry.path_prefix)
      onDeleted()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setDeleting(false)
    }
  }

  if (!entry) return null
  return (
    <Dialog title="Remove path default" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Remove default permissions for path <Mono>{entry.path_prefix || '/'}</Mono>?
        </Text>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="danger" disabled={deleting} onClick={handleDelete}>
          {deleting ? 'Removing…' : 'Remove'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
