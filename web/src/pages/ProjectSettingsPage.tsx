import { useEffect, useState } from 'react'
import { Button, Checkbox, FormControl, IconButton, Select, Stack, Text, TextInput, ToggleSwitch } from '@primer/react'
import { XIcon } from '@primer/octicons-react'
import { useParams, useSearchParams } from 'react-router-dom'
import {
  createProjectRule,
  deleteProjectDefault,
  deleteProjectRule,
  listProjectDefaults,
  listProjectRules,
  setBranchProtection,
  setProjectDefault,
} from '../api/endpoints'
import type { BranchResponse, OrgMemberResponse, PBACRuleResponse, PermissionEntry } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { PermissionBadge } from '../components/PermissionBadge'
import { PermissionCheckboxes } from '../components/PermissionCheckboxes'
import { EmailSettings } from '../components/repo/EmailSettings'
import { ProjectGeneralSettings } from '../components/repo/ProjectGeneralSettings'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { WebhookSettings } from '../components/repo/WebhookSettings'
import { SettingsCard, SettingsRow } from '../components/SettingsCard'
import { SettingsLayout } from '../components/SettingsLayout'
import { UserGroupPicker } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, Mono, NoPermission } from '../components/ui'
import { useAction, useAsync } from '../hooks'
import { useOrgMembers } from '../orgHooks'

const SETTINGS_TABS = [
  { key: 'general', label: 'General' },
  { key: 'access', label: 'Access' },
  { key: 'branches', label: 'Branches' },
  { key: 'webhooks', label: 'Webhooks' },
  { key: 'email', label: 'Email deliveries' },
] as const

type SettingsTab = (typeof SETTINGS_TABS)[number]['key']

function isSettingsTab(value: string | null): value is SettingsTab {
  return SETTINGS_TABS.some((tab) => tab.key === value)
}

export default function ProjectSettingsPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { branches, reloadBranches, canWrite, canAdmin, defaultBranch, permissionsLoading } = useRepoChrome(
    org,
    project,
  )

  const { data: rules, error: rulesError, loading: rulesLoading, reload: reloadRules } = useAsync(
    () => listProjectRules(org, project),
    [org, project],
  )
  const { data: defaults, error: defaultsError, reload: reloadDefaults } = useAsync(
    () => listProjectDefaults(org, project),
    [org, project],
  )
  const { data: members, error: membersError } = useOrgMembers(org)
  const { run, error: actionError, clearError } = useAction()

  const [showAddRule, setShowAddRule] = useState(false)
  const [defaultDialog, setDefaultDialog] = useState<{ editing: PermissionEntry | null } | null>(null)
  const [searchParams] = useSearchParams()

  const requestedTab = searchParams.get('tab')
  const tab: SettingsTab = isSettingsTab(requestedTab) ? requestedTab : 'general'
  const base = `/${org}/${project}/settings`

  const shell = { org, project, active: 'settings' as const, rev: defaultBranch, canAdmin, canWrite, heading: 'Settings' }

  if (permissionsLoading || !canAdmin) {
    return (
      <RepoPageShell {...shell}>
        <ErrorBanner error={rulesError} />
        <NoPermission loading={permissionsLoading}>
          You need project admin permission to manage this repository.
        </NoPermission>
      </RepoPageShell>
    )
  }

  async function revokeRule(rule: PBACRuleResponse) {
    const subject = ruleSubject(rule)
    const who = rule.user_id
      ? `user: ${subject ? `${subject.name}${subject.email ? ` (${subject.email})` : ''}` : rule.user_id}`
      : `group: ${subject?.name ?? rule.group_id}`
    const ok = await confirm({
      title: 'Revoke access rule',
      content: (
        <>
          Revoke access for <strong>{who}</strong> on path <Mono>{rule.path_prefix || '/'}</Mono>?
        </>
      ),
      confirmLabel: 'Revoke',
    })
    if (ok) {
      await run(async () => {
        await deleteProjectRule(org, project, rule.id)
        reloadRules()
      })
    }
  }

  async function removeDefault(entry: PermissionEntry) {
    const ok = await confirm({
      title: 'Remove path default',
      content: (
        <>
          Remove default permissions for path <Mono>{entry.path_prefix || '/'}</Mono>?
        </>
      ),
      confirmLabel: 'Remove',
    })
    if (ok) {
      await run(async () => {
        await deleteProjectDefault(org, project, entry.path_prefix)
        reloadDefaults()
      })
    }
  }

  async function changeProtection(
    branch: BranchResponse,
    change: Partial<{
      protect: boolean
      approvals: number
      dismissStale: boolean
      requireChecks: boolean
      reviewers: string[]
      checks: string[]
    }>,
  ) {
    if (change.protect === false && branch.is_protected) {
      const ok = await confirm({
        title: `Unprotect ${branch.name}?`,
        content: 'Anyone with write access will be able to push to and delete this branch.',
        confirmLabel: 'Unprotect',
      })
      if (!ok) return
    }
    await run(async () => {
      await setBranchProtection(
        org,
        project,
        branch.name,
        change.protect ?? branch.is_protected,
        change.approvals,
        change.dismissStale,
        change.requireChecks,
        change.reviewers,
        change.checks,
      )
      reloadBranches()
    })
  }

  return (
    <RepoPageShell {...shell}>
      <ErrorBanner
        error={actionError ?? rulesError ?? defaultsError ?? membersError}
        onDismiss={actionError ? clearError : undefined}
      />

      <SettingsLayout
        label="Repository settings"
        active={tab}
        items={SETTINGS_TABS.map((item) => ({ key: item.key, label: item.label, to: `${base}?tab=${item.key}` }))}
      >
        <Stack direction="vertical" gap="spacious">
          {tab === 'general' && <ProjectGeneralSettings org={org} project={project} />}

          {tab === 'access' && (
            <>
              <AccessRulesSection
                rules={rules ?? []}
                loading={rulesLoading}
                onAdd={() => setShowAddRule(true)}
                onDelete={(rule) => void revokeRule(rule)}
              />
              <PathDefaultsSection
                defaults={defaults ?? []}
                onAdd={() => setDefaultDialog({ editing: null })}
                onEdit={(entry) => setDefaultDialog({ editing: entry })}
                onDelete={(entry) => void removeDefault(entry)}
              />
            </>
          )}

          {tab === 'branches' && (
            <BranchProtectionCard
              branches={branches ?? []}
              members={members ?? []}
              onChange={(branch, change) => void changeProtection(branch, change)}
            />
          )}

          {tab === 'webhooks' && <WebhookSettings org={org} project={project} />}
          {tab === 'email' && <EmailSettings org={org} project={project} />}
        </Stack>
      </SettingsLayout>

      {showAddRule && (
        <AddRuleDialog
          org={org}
          project={project}
          onClose={() => setShowAddRule(false)}
          onCreated={reloadRules}
        />
      )}

      {defaultDialog && (
        <AddDefaultDialog
          org={org}
          project={project}
          editing={defaultDialog.editing}
          onClose={() => setDefaultDialog(null)}
          onSaved={reloadDefaults}
        />
      )}
    </RepoPageShell>
  )
}

function ruleSubject(rule: PBACRuleResponse): { name: string; email?: string } | null {
  if (rule.user_id) {
    return rule.user_name ? { name: rule.user_name, email: rule.user_email } : null
  }
  return rule.group_name ? { name: rule.group_name } : null
}

type ProtectionChange = Partial<{
  protect: boolean
  approvals: number
  dismissStale: boolean
  requireChecks: boolean
  reviewers: string[]
  checks: string[]
}>

function BranchProtectionCard({
  branches,
  members,
  onChange,
}: {
  branches: BranchResponse[]
  members: OrgMemberResponse[]
  onChange: (branch: BranchResponse, change: ProtectionChange) => void
}) {
  return (
    <SettingsCard
      title="Protected branches"
      description="Branch protection blocks force pushes and deletions, and can require approvals before a merge request lands."
    >
      {branches.length === 0 && <EmptyState bordered={false}>No branches yet.</EmptyState>}
      {branches.map((branch, index) => (
        <SettingsRow
          key={branch.id}
          label={<span id={`branch-protection-${index}`}>{branch.name}</span>}
          subtext={branch.is_default ? 'Default branch' : undefined}
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--base-size-16)' }}>
            <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12, margin: 0 }}>
              {branch.is_protected
                ? 'Protected — pushes and deletions are restricted.'
                : 'Not protected — anyone with write access can push.'}
            </Text>
            <Stack direction="horizontal" gap="condensed" align="center">
              <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12, whiteSpace: 'nowrap' }}>Required approvals</Text>
              <ApprovalsInput branch={branch} onCommit={(approvals) => onChange(branch, { approvals })} />
              <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, whiteSpace: 'nowrap' }}>
                <Checkbox
                  aria-label={`Dismiss stale approvals on push for ${branch.name}`}
                  checked={branch.dismiss_stale_approvals}
                  onChange={(event) => onChange(branch, { dismissStale: event.target.checked })}
                />
                Dismiss stale approvals on push
              </label>
              <ToggleSwitch
                aria-labelledby={`branch-protection-${index}`}
                checked={Boolean(branch.is_protected)}
                onChange={(value) => onChange(branch, { protect: value })}
              />
            </Stack>
          </div>
          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              gap: 'var(--base-size-12)',
              marginTop: 'var(--base-size-8)',
              fontSize: 12,
            }}
          >
            <label style={{ display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <Checkbox
                aria-label={`Require status checks for ${branch.name}`}
                checked={branch.require_status_checks}
                onChange={(event) => onChange(branch, { requireChecks: event.target.checked })}
              />
              Require status checks
            </label>
            <RequiredChecksInput branch={branch} onCommit={(checks) => onChange(branch, { checks })} />
            <RequiredReviewersPicker
              branch={branch}
              members={members}
              onCommit={(reviewers) => onChange(branch, { reviewers })}
            />
          </div>
        </SettingsRow>
      ))}
    </SettingsCard>
  )
}

function RequiredChecksInput({ branch, onCommit }: { branch: BranchResponse; onCommit: (checks: string[]) => void }) {
  const [value, setValue] = useState((branch.required_checks ?? []).join(', '))

  useEffect(() => {
    setValue((branch.required_checks ?? []).join(', '))
  }, [branch.required_checks])

  function commit() {
    const names = value
      .split(',')
      .map((name) => name.trim())
      .filter((name) => name !== '')
    const current = branch.required_checks ?? []
    if (names.join('\u0000') !== current.join('\u0000')) {
      onCommit(names)
    } else {
      setValue(current.join(', '))
    }
  }

  return (
    <TextInput
      aria-label={`Required checks for ${branch.name}`}
      placeholder="checks, comma separated"
      size="small"
      value={value}
      onChange={(event) => setValue(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') commit()
      }}
      style={{ width: 220 }}
    />
  )
}

function RequiredReviewersPicker({
  branch,
  members,
  onCommit,
}: {
  branch: BranchResponse
  members: OrgMemberResponse[]
  onCommit: (reviewerIDs: string[]) => void
}) {
  const [pick, setPick] = useState('')
  const required = branch.required_reviewers ?? []
  const candidates = members.filter((member) => !required.some((reviewer) => reviewer.user_id === member.user_id))

  return (
    <Stack direction="horizontal" gap="condensed" align="center">
      {required.map((reviewer) => (
        <span key={reviewer.user_id} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
          {reviewer.name || reviewer.user_id}
          <IconButton
            size="small"
            icon={XIcon}
            aria-label={`remove required reviewer ${reviewer.name || reviewer.user_id}`}
            onClick={() =>
              onCommit(required.filter((entry) => entry.user_id !== reviewer.user_id).map((entry) => entry.user_id))
            }
          />
        </span>
      ))}
      {candidates.length > 0 && (
        <>
          <Select
            aria-label={`Add required reviewer for ${branch.name}`}
            value={pick}
            onChange={(event) => setPick(event.target.value)}
            size="small"
          >
            <Select.Option value="">add required reviewer…</Select.Option>
            {candidates.map((member) => (
              <Select.Option key={member.user_id} value={member.user_id}>
                {member.name || member.email}
              </Select.Option>
            ))}
          </Select>
          <Button
            size="small"
            disabled={!pick}
            onClick={() => {
              onCommit([...required.map((entry) => entry.user_id), pick])
              setPick('')
            }}
          >
            Add
          </Button>
        </>
      )}
    </Stack>
  )
}

function ApprovalsInput({ branch, onCommit }: { branch: BranchResponse; onCommit: (value: number) => void }) {
  const [value, setValue] = useState(String(branch.required_approvals ?? 0))

  useEffect(() => {
    setValue(String(branch.required_approvals ?? 0))
  }, [branch.required_approvals])

  function commit() {
    const parsed = Number.parseInt(value, 10)
    const next = Number.isFinite(parsed) && parsed > 0 ? parsed : 0
    setValue(String(next))
    if (next !== branch.required_approvals) onCommit(next)
  }

  return (
    <TextInput
      aria-label={`Required approvals for ${branch.name}`}
      type="number"
      min={0}
      size="small"
      value={value}
      onChange={(event) => setValue(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') commit()
      }}
      style={{ width: 72 }}
    />
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
      {loading && <Loading variant="skeleton" />}
      {!loading && rules.length === 0 && (
        <EmptyState bordered={false}>
          No access rules. Everyone with write access can reach the whole repository.
        </EmptyState>
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
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 'var(--base-size-16)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--base-size-8)' }}>
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
        <EmptyState bordered={false}>No path defaults. The repository is accessible to all members by default.</EmptyState>
      )}
      {defaults.map((entry) => (
        <SettingsRow key={entry.path_prefix} label={<Mono>{entry.path_prefix || '/'}</Mono>}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--base-size-16)' }}>
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
}: {
  org: string
  project: string
  onClose: () => void
  onCreated: () => void
}) {
  const [subject, setSubject] = useState<{ id: string; label: string; isGroup: boolean } | null>(null)
  const [prefix, setPrefix] = useState('')
  const [permission, setPermission] = useState(0)

  return (
    <FormDialog
      title="Add access rule"
      submitLabel="Add rule"
      submitDisabled={!subject || permission === 0}
      onClose={onClose}
      onSubmit={async () => {
        if (!subject) return
        await createProjectRule(
          org,
          project,
          subject.isGroup ? '' : subject.id,
          subject.isGroup ? subject.id : '',
          prefix,
          permission,
        )
        onCreated()
      }}
    >
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
    </FormDialog>
  )
}

function AddDefaultDialog({
  org,
  project,
  editing,
  onClose,
  onSaved,
}: {
  org: string
  project: string
  editing: PermissionEntry | null
  onClose: () => void
  onSaved: () => void
}) {
  const [prefix, setPrefix] = useState(editing?.path_prefix ?? '')
  const [permission, setPermission] = useState(editing?.permission ?? 0)

  return (
    <FormDialog
      title={editing ? 'Edit path default' : 'Add path default'}
      submitLabel={editing ? 'Save' : 'Add default'}
      submitDisabled={permission === 0}
      onClose={onClose}
      onSubmit={async () => {
        await setProjectDefault(org, project, prefix, permission)
        onSaved()
      }}
    >
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
    </FormDialog>
  )
}
