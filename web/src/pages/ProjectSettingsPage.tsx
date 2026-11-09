import { useState } from 'react'
import { Button, FormControl, Stack, Text, TextInput } from '@primer/react'
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
import { PERMISSION_READ, type BranchResponse, type PBACRuleResponse, type PermissionEntry } from '../api/models'
import { PickedSubject } from '../components/PickedSubject'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { PermissionBadge } from '../components/PermissionBadge'
import { PermissionCheckboxes } from '../components/PermissionCheckboxes'
import { EmailSettings } from '../components/repo/EmailSettings'
import { BranchProtectionSettings, type ProtectionRule } from '../components/repo/BranchProtectionSettings'
import { ProjectGeneralSettings } from '../components/repo/ProjectGeneralSettings'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { WebhookSettings } from '../components/repo/WebhookSettings'
import { ServiceTokenSettings } from '../components/repo/ServiceTokenSettings'
import { SettingsCard, SettingsRow } from '../components/SettingsCard'
import { SettingsLayout } from '../components/SettingsLayout'
import { UserGroupPicker, type PickerEntry } from '../components/UserGroupPicker'
import { EmptyState, ErrorBanner, Loading, Mono, NoPermission } from '../components/ui'
import { useAction, useAsync } from '../hooks'
import { useOrgMembers } from '../orgHooks'

const SETTINGS_TABS = [
  { key: 'general', label: 'General' },
  { key: 'access', label: 'Access' },
  { key: 'branches', label: 'Branches' },
  { key: 'webhooks', label: 'Webhooks' },
  { key: 'tokens', label: 'Service tokens' },
  { key: 'email', label: 'Email deliveries' },
] as const

type SettingsTab = (typeof SETTINGS_TABS)[number]['key']

function isSettingsTab(value: string | null): value is SettingsTab {
  return SETTINGS_TABS.some((tab) => tab.key === value)
}

export default function ProjectSettingsPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { branches, reloadBranches, canAdmin, defaultBranch, permissionsLoading } = useRepoChrome(
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
  const { run, busy: actionBusy, error: actionError, clearError } = useAction()

  const [showAddRule, setShowAddRule] = useState(false)
  const [defaultDialog, setDefaultDialog] = useState<{ editing: PermissionEntry | null } | null>(null)
  const [searchParams] = useSearchParams()

  const requestedTab = searchParams.get('tab')
  const tab: SettingsTab = isSettingsTab(requestedTab) ? requestedTab : 'general'
  const base = `/${org}/${project}/settings`

  const shell = { org, project, active: 'settings' as const, rev: defaultBranch, canAdmin, heading: 'Settings' }

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

  async function saveProtection(branch: BranchResponse, rule: ProtectionRule): Promise<boolean> {
    const saved = await run(async () => {
      const result = await setBranchProtection(
        org,
        project,
        branch.name,
        true,
        rule.approvals,
        rule.dismissStale,
        rule.requireChecks,
        rule.reviewers,
        rule.checks,
      )
      reloadBranches()
      return result
    })
    return Boolean(saved)
  }

  async function removeProtection(branch: BranchResponse) {
    const ok = await confirm({
      title: `Delete the protection rule for ${branch.name}?`,
      content: 'Anyone with write access will be able to push to and delete this branch, and its review and check requirements are cleared.',
      confirmLabel: 'Delete rule',
    })
    if (!ok) return
    await run(async () => {
      await setBranchProtection(org, project, branch.name, false, 0, true, false, [], [])
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
            <BranchProtectionSettings
              branches={branches ?? []}
              members={members ?? []}
              busy={actionBusy}
              onSave={saveProtection}
              onRemove={(branch) => void removeProtection(branch)}
            />
          )}

          {tab === 'webhooks' && <WebhookSettings org={org} project={project} />}
          {tab === 'tokens' && <ServiceTokenSettings org={org} project={project} />}
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

function PathPrefixField({
  value,
  onChange,
  disabled = false,
  caption,
}: {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
  caption: string
}) {
  return (
    <FormControl disabled={disabled}>
      <FormControl.Label>Path</FormControl.Label>
      <TextInput
        block
        leadingVisual={() => <span style={{ color: 'var(--fgColor-muted)' }}>/</span>}
        placeholder="e.g. assets/textures"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <FormControl.Caption>{caption}</FormControl.Caption>
    </FormControl>
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
  const [subject, setSubject] = useState<PickerEntry | null>(null)
  const [prefix, setPrefix] = useState('')
  const [permission, setPermission] = useState(PERMISSION_READ)

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
        {subject ? (
          <PickedSubject subject={subject} onChange={() => setSubject(null)} />
        ) : (
          <UserGroupPicker
            org={org}
            kind="user-or-group"
            inline
            onSelect={setSubject}
            placeholder="Search users or groups…"
          />
        )}
      </FormControl>
      <PathPrefixField
        value={prefix}
        onChange={setPrefix}
        caption="Applies to this folder and everything under it. Leave empty for the whole repository."
      />
      <PermissionCheckboxes value={permission} onChange={setPermission} />
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
  const [permission, setPermission] = useState(editing?.permission ?? PERMISSION_READ)

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
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)', fontSize: 14 }}>
        Path defaults grant permissions to every project member on a path, before user and group rules are applied.
      </Text>
      <PathPrefixField
        value={prefix}
        onChange={setPrefix}
        disabled={editing !== null}
        caption={
          editing
            ? 'The path identifies this default and cannot be changed. Delete it and add a new one to move it.'
            : 'Applies to this folder and everything under it. Leave empty for the whole repository.'
        }
      />
      <PermissionCheckboxes label="Default permissions" value={permission} onChange={setPermission} />
    </FormDialog>
  )
}
