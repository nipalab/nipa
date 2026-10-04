import { useState } from 'react'
import type { CSSProperties, ReactNode } from 'react'
import {
  Button,
  Dialog,
  FormControl,
  Heading,
  NavList,
  Stack,
  Text,
  TextInput,
  ToggleSwitch,
} from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import {
  createProjectRule,
  deleteProjectDefault,
  deleteProjectRule,
  listProjectDefaults,
  listProjectRules,
  setBranchProtection,
  setProjectDefault,
} from '../api/endpoints'
import type { BranchResponse, PBACRuleResponse, PermissionEntry } from '../api/models'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { WebhookSettings } from '../components/repo/WebhookSettings'
import { PermissionBadge } from '../components/PermissionBadge'
import { PermissionCheckboxes } from '../components/PermissionCheckboxes'
import { UserGroupPicker } from '../components/UserGroupPicker'
import { ErrorBanner, Loading, Mono } from '../components/ui'
import { useAsync } from '../hooks'

const SETTINGS_TABS = [
  { key: 'access', label: 'Access' },
  { key: 'branches', label: 'Branches' },
  { key: 'webhooks', label: 'Webhooks' },
] as const

type SettingsTab = (typeof SETTINGS_TABS)[number]['key']

function isSettingsTab(value: string | null): value is SettingsTab {
  return SETTINGS_TABS.some((tab) => tab.key === value)
}

function Box({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        border: '1px solid var(--borderColor-default)',
        borderRadius: 6,
        overflow: 'hidden',
        ...style,
      }}
    >
      {children}
    </div>
  )
}

function SettingsCard({
  title,
  description,
  actions,
  footer,
  children,
}: {
  title: string
  description?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  children: ReactNode
}) {
  return (
    <Box>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 16,
          padding: '12px 16px',
          background: 'var(--bgColor-muted)',
        }}
      >
        <div style={{ minWidth: 0 }}>
          <Heading as="h2" style={{ margin: 0, fontSize: 16 }}>
            {title}
          </Heading>
          {description && (
            <Text as="p" style={{ margin: '4px 0 0', fontSize: 12, color: 'var(--fgColor-muted)' }}>
              {description}
            </Text>
          )}
        </div>
        {actions}
      </div>
      {children}
      {footer && (
        <div
          style={{
            borderTop: '1px solid var(--borderColor-default)',
            background: 'var(--bgColor-muted)',
            padding: '12px 16px',
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 8,
          }}
        >
          {footer}
        </div>
      )}
    </Box>
  )
}

function SettingsRow({
  label,
  subtext,
  children,
}: {
  label: ReactNode
  subtext?: ReactNode
  children?: ReactNode
}) {
  return (
    <div
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-start',
        gap: 16,
        padding: 16,
        borderTop: '1px solid var(--borderColor-muted)',
      }}
    >
      <div style={{ flex: '0 0 220px', minWidth: 0 }}>
        <div style={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{label}</div>
        {subtext && (
          <div style={{ color: 'var(--fgColor-muted)', fontSize: 12, overflowWrap: 'anywhere' }}>{subtext}</div>
        )}
      </div>
      {children !== undefined && <div style={{ flex: '1 1 300px', minWidth: 0 }}>{children}</div>}
    </div>
  )
}

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
  const [searchParams] = useSearchParams()

  const requestedTab = searchParams.get('tab')
  const tab: SettingsTab = isSettingsTab(requestedTab) ? requestedTab : 'access'

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

      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 32, marginTop: 24 }}>
        <div style={{ flex: '0 0 200px', minWidth: 0 }}>
          <NavList aria-label="Repository settings">
            {SETTINGS_TABS.map((item) => (
              <NavList.Item
                key={item.key}
                as={Link}
                to={`/${org}/${project}/settings?tab=${item.key}`}
                aria-current={item.key === tab ? 'page' : undefined}
              >
                {item.label}
              </NavList.Item>
            ))}
          </NavList>
        </div>

        <div style={{ flex: '1 1 0', minWidth: 0, display: 'flex', flexDirection: 'column', gap: 24 }}>
          {tab === 'access' && (
            <>
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
            </>
          )}

          {tab === 'branches' && (
            <BranchProtectionCard
              branches={branches ?? []}
              onToggle={(branch, protect) =>
                run(async () => {
                  await setBranchProtection(org, project, branch.name, protect)
                  reloadBranches()
                })
              }
            />
          )}

          {tab === 'webhooks' && <WebhookSettings org={org} project={project} />}
        </div>
      </div>

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
    </RepoPageShell>
  )
}

function ruleSubject(rule: PBACRuleResponse): { name: string; email?: string } | null {
  if (rule.user_id) {
    return rule.user_name ? { name: rule.user_name, email: rule.user_email } : null
  }
  return rule.group_name ? { name: rule.group_name } : null
}

function BranchProtectionCard({
  branches,
  onToggle,
}: {
  branches: BranchResponse[]
  onToggle: (branch: BranchResponse, protect: boolean) => void
}) {
  return (
    <SettingsCard
      title="Protected branches"
      description="Branch protection blocks force pushes and deletions, so history cannot be rewritten by accident."
    >
      {branches.length === 0 && <div style={{ padding: 16, color: 'var(--fgColor-muted)' }}>No branches yet.</div>}
      {branches.map((branch, index) => (
        <SettingsRow
          key={branch.id}
          label={<span id={`branch-protection-${index}`}>{branch.name}</span>}
          subtext={branch.is_default ? 'Default branch' : undefined}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
            <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12, margin: 0 }}>
              {branch.is_protected
                ? 'Protected — pushes and deletions are restricted.'
                : 'Not protected — anyone with write access can push.'}
            </Text>
            <ToggleSwitch
              aria-labelledby={`branch-protection-${index}`}
              checked={Boolean(branch.is_protected)}
              onChange={(value) => onToggle(branch, value)}
            />
          </div>
        </SettingsRow>
      ))}
    </SettingsCard>
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
    <SettingsCard
      title="Access rules"
      description="Grant specific users or groups access to paths in this repository."
      actions={
        <Button size="small" onClick={onAdd}>
          Add access rule
        </Button>
      }
    >
      {loading && (
        <div style={{ padding: 16 }}>
          <Loading />
        </div>
      )}
      {!loading && rules.length === 0 && (
        <div style={{ padding: 16, color: 'var(--fgColor-muted)' }}>
          No access rules. Everyone with write access can reach the whole repository.
        </div>
      )}
      {rules.map((rule) => {
        const subject = ruleSubject(rule)
        const fallbackId = rule.user_id || rule.group_id || rule.id
        return (
          <SettingsRow
            key={rule.id}
            label={subject?.name ?? fallbackId}
            subtext={subject?.email ?? (subject ? undefined : fallbackId)}
          >
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                flexWrap: 'wrap',
                gap: 16,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Mono>{rule.path_prefix || '/'}</Mono>
                <PermissionBadge permission={rule.permission} />
              </div>
              <Button size="small" variant="danger" onClick={() => onDelete(rule)}>
                Revoke
              </Button>
            </div>
          </SettingsRow>
        )
      })}
    </SettingsCard>
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
    <SettingsCard
      title="Path defaults"
      description="Default permissions for paths without a matching access rule."
      actions={
        <Button size="small" onClick={onAdd}>
          Add path default
        </Button>
      }
    >
      {defaults.length === 0 && (
        <div style={{ padding: 16, color: 'var(--fgColor-muted)' }}>
          No path defaults. The repository is accessible to all members by default.
        </div>
      )}
      {defaults.map((entry) => (
        <SettingsRow key={entry.path_prefix} label={<Mono>{entry.path_prefix || '/'}</Mono>}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
            <PermissionBadge permission={entry.permission} />
            <Stack direction="horizontal" gap="condensed">
              <Button size="small" onClick={() => onEdit(entry)}>
                Edit
              </Button>
              <Button size="small" variant="danger" onClick={() => onDelete(entry)}>
                Remove
              </Button>
            </Stack>
          </div>
        </SettingsRow>
      ))}
    </SettingsCard>
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
            <FormControl disabled={editing !== null}>
              <FormControl.Label>Path prefix</FormControl.Label>
              <TextInput
                block
                placeholder="e.g. assets/textures (empty = whole repository)"
                value={prefix}
                onChange={(e) => setPrefix(e.target.value)}
              />
              <FormControl.Caption>
                {editing
                  ? 'The path prefix identifies this default and cannot be changed. Delete it and add a new one to move it.'
                  : 'Leave empty to apply to the entire repository.'}
              </FormControl.Caption>
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
  const subject = ruleSubject(rule)
  return (
    <Dialog title="Revoke access rule" onClose={onClose} width="medium">
      <Dialog.Body>
        <Text as="p">
          Revoke access for{' '}
          <strong>
            {rule.user_id
              ? `user: ${subject ? `${subject.name}${subject.email ? ` (${subject.email})` : ''}` : rule.user_id}`
              : `group: ${subject?.name ?? rule.group_id}`}
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
