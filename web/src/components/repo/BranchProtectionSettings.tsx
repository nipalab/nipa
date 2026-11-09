import { useState, type ReactNode } from 'react'
import { Button, Checkbox, FormControl, IconButton, Label, Select, Text, TextInput } from '@primer/react'
import { GitBranchIcon, PencilIcon, ShieldLockIcon, TrashIcon, XIcon } from '@primer/octicons-react'
import type { BranchResponse, OrgMemberResponse } from '../../api/models'
import { ActorAvatar, actorName } from '../ActorAvatar'
import { SettingsCard } from '../SettingsCard'
import { EmptyState, Mono } from '../ui'

export interface ProtectionRule {
  approvals: number
  dismissStale: boolean
  requireChecks: boolean
  checks: string[]
  reviewers: string[]
}

const MAX_APPROVALS = 10

function ruleOf(branch: BranchResponse | undefined): ProtectionRule {
  return {
    approvals: branch?.is_protected ? (branch.required_approvals ?? 0) : 1,
    dismissStale: branch?.is_protected ? branch.dismiss_stale_approvals : true,
    requireChecks: branch?.is_protected ? branch.require_status_checks : false,
    checks: branch?.is_protected ? (branch.required_checks ?? []) : [],
    reviewers: branch?.is_protected ? (branch.required_reviewers ?? []).map((reviewer) => reviewer.user_id) : [],
  }
}

export function protectionSummary(branch: BranchResponse): string[] {
  const parts: string[] = []
  const approvals = branch.required_approvals ?? 0
  if (approvals > 0) {
    parts.push(`${approvals} approval${approvals === 1 ? '' : 's'} required`)
    if (branch.dismiss_stale_approvals) parts.push('stale approvals dismissed')
  }
  const reviewers = branch.required_reviewers ?? []
  if (reviewers.length > 0) parts.push(`${reviewers.length} required reviewer${reviewers.length === 1 ? '' : 's'}`)
  if (branch.require_status_checks) {
    const checks = branch.required_checks ?? []
    parts.push(checks.length > 0 ? `${checks.length} required check${checks.length === 1 ? '' : 's'}` : 'status checks required')
  }
  if (parts.length === 0) parts.push('Merge requests only')
  return parts
}

// Option is one GitHub-style protection setting: a checkbox with a title and
// description, revealing its nested settings while checked.
function Option({
  id,
  checked,
  onChange,
  title,
  description,
  children,
}: {
  id: string
  checked: boolean
  onChange: (checked: boolean) => void
  title: string
  description: string
  children?: ReactNode
}) {
  return (
    <div className="nipa-protect-option">
      <div className="nipa-protect-option-head">
        <Checkbox id={id} checked={checked} onChange={(event) => onChange(event.target.checked)} />
        <label htmlFor={id}>
          <span className="nipa-protect-option-title">{title}</span>
          <span className="nipa-protect-option-desc">{description}</span>
        </label>
      </div>
      {checked && children && <div className="nipa-protect-option-body">{children}</div>}
    </div>
  )
}

function RuleEditor({
  branches,
  members,
  initial,
  busy,
  onCancel,
  onSave,
}: {
  branches: BranchResponse[]
  members: OrgMemberResponse[]
  initial?: BranchResponse
  busy: boolean
  onCancel: () => void
  onSave: (branch: BranchResponse, rule: ProtectionRule) => void
}) {
  const choices = branches.filter((branch) => !branch.is_protected)
  const [name, setName] = useState(initial?.name ?? choices.find((branch) => branch.is_default)?.name ?? choices[0]?.name ?? '')
  const [rule, setRule] = useState<ProtectionRule>(() => ruleOf(initial))
  const [requireApprovals, setRequireApprovals] = useState(Boolean(initial) && rule.approvals > 0)
  const [requireReviewers, setRequireReviewers] = useState(rule.reviewers.length > 0)
  const [checkInput, setCheckInput] = useState('')
  const [reviewerPick, setReviewerPick] = useState('')
  const update = (change: Partial<ProtectionRule>) => setRule((current) => ({ ...current, ...change }))
  const branch = initial ?? branches.find((entry) => entry.name === name)

  const addCheck = () => {
    const check = checkInput.trim()
    if (check && !rule.checks.includes(check)) update({ checks: [...rule.checks, check] })
    setCheckInput('')
  }
  const reviewerActor = (userId: string) => {
    const known = (initial?.required_reviewers ?? []).find((reviewer) => reviewer.user_id === userId)
    const member = members.find((entry) => entry.user_id === userId)
    return known ?? (member ? { user_id: member.user_id, name: member.name || member.email } : { user_id: userId })
  }
  const candidates = members.filter((member) => !rule.reviewers.includes(member.user_id))

  const save = () => {
    if (!branch) return
    onSave(branch, {
      approvals: requireApprovals ? Math.max(1, rule.approvals) : 0,
      dismissStale: rule.dismissStale,
      requireChecks: rule.requireChecks,
      checks: rule.requireChecks ? rule.checks : [],
      reviewers: requireReviewers ? rule.reviewers : [],
    })
  }

  return (
    <SettingsCard
      title={initial ? `Edit protection for ${initial.name}` : 'Add branch protection rule'}
      description="Protected branches cannot be pushed to directly or deleted; changes land through merge requests."
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant="primary" disabled={busy || !branch} onClick={save}>
            {initial ? 'Save changes' : 'Create'}
          </Button>
        </div>
      }
    >
      <div className="nipa-protect-editor">
        {initial ? (
          <div className="nipa-protect-branch">
            <GitBranchIcon />
            <Mono>{initial.name}</Mono>
            {initial.is_default && <Label size="small">Default</Label>}
          </div>
        ) : (
          <FormControl>
            <FormControl.Label>Branch</FormControl.Label>
            {choices.length === 0 ? (
              <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>Every branch is already protected.</Text>
            ) : (
              <Select value={name} onChange={(event) => setName(event.target.value)}>
                {choices.map((entry) => (
                  <Select.Option key={entry.id} value={entry.name}>
                    {entry.name}
                    {entry.is_default ? ' (default)' : ''}
                  </Select.Option>
                ))}
              </Select>
            )}
          </FormControl>
        )}

        <Text as="h3" className="nipa-protect-heading">
          Protect matching branches
        </Text>

        <Option
          id="protect-approvals"
          checked={requireApprovals}
          onChange={setRequireApprovals}
          title="Require approvals"
          description="Merge requests targeting this branch need approving reviews before they can be merged."
        >
          <FormControl>
            <FormControl.Label>Required number of approvals before merging</FormControl.Label>
            <Select
              value={String(Math.max(1, rule.approvals))}
              onChange={(event) => update({ approvals: Number(event.target.value) })}
            >
              {Array.from({ length: MAX_APPROVALS }, (_, index) => index + 1).map((count) => (
                <Select.Option key={count} value={String(count)}>
                  {count}
                </Select.Option>
              ))}
            </Select>
          </FormControl>
          <div className="nipa-protect-nested">
            <Option
              id="protect-dismiss-stale"
              checked={rule.dismissStale}
              onChange={(dismissStale) => update({ dismissStale })}
              title="Dismiss stale approvals when new commits are pushed"
              description="New commits pushed to the source branch dismiss approvals given for the previous head."
            />
          </div>
        </Option>

        <Option
          id="protect-reviewers"
          checked={requireReviewers}
          onChange={setRequireReviewers}
          title="Require review from specific people"
          description="Each of these reviewers must approve before a merge request can be merged."
        >
          {rule.reviewers.length > 0 && (
            <ul className="nipa-protect-list" aria-label="Required reviewers">
              {rule.reviewers.map((userId) => {
                const actor = reviewerActor(userId)
                return (
                  <li key={userId}>
                    <ActorAvatar actor={actor} size={20} />
                    <span>{actorName(actor)}</span>
                    <IconButton
                      size="small"
                      variant="invisible"
                      icon={XIcon}
                      aria-label={`Remove required reviewer ${actorName(actor)}`}
                      onClick={() => update({ reviewers: rule.reviewers.filter((entry) => entry !== userId) })}
                    />
                  </li>
                )
              })}
            </ul>
          )}
          {candidates.length > 0 && (
            <div className="nipa-protect-add">
              <Select aria-label="Add required reviewer" value={reviewerPick} onChange={(event) => setReviewerPick(event.target.value)}>
                <Select.Option value="">Choose a member…</Select.Option>
                {candidates.map((member) => (
                  <Select.Option key={member.user_id} value={member.user_id}>
                    {member.name || member.email}
                  </Select.Option>
                ))}
              </Select>
              <Button
                disabled={!reviewerPick}
                onClick={() => {
                  update({ reviewers: [...rule.reviewers, reviewerPick] })
                  setReviewerPick('')
                }}
              >
                Add reviewer
              </Button>
            </div>
          )}
        </Option>

        <Option
          id="protect-checks"
          checked={rule.requireChecks}
          onChange={(requireChecks) => update({ requireChecks })}
          title="Require status checks to pass before merging"
          description="The named checks must succeed for the current source head before a merge request can be merged."
        >
          {rule.checks.length > 0 && (
            <ul className="nipa-protect-list" aria-label="Required checks">
              {rule.checks.map((check) => (
                <li key={check}>
                  <Mono>{check}</Mono>
                  <IconButton
                    size="small"
                    variant="invisible"
                    icon={XIcon}
                    aria-label={`Remove required check ${check}`}
                    onClick={() => update({ checks: rule.checks.filter((entry) => entry !== check) })}
                  />
                </li>
              ))}
            </ul>
          )}
          <div className="nipa-protect-add">
            <TextInput
              aria-label="Status check name"
              placeholder="e.g. build"
              value={checkInput}
              onChange={(event) => setCheckInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  addCheck()
                }
              }}
            />
            <Button disabled={!checkInput.trim()} onClick={addCheck}>
              Add check
            </Button>
          </div>
        </Option>
      </div>
    </SettingsCard>
  )
}

export function BranchProtectionSettings({
  branches,
  members,
  busy,
  onSave,
  onRemove,
}: {
  branches: BranchResponse[]
  members: OrgMemberResponse[]
  busy: boolean
  onSave: (branch: BranchResponse, rule: ProtectionRule) => Promise<boolean>
  onRemove: (branch: BranchResponse) => void
}) {
  const [editing, setEditing] = useState<{ branch?: BranchResponse } | null>(null)
  const protectedBranches = branches.filter((branch) => branch.is_protected)
  const unprotected = branches.length - protectedBranches.length

  if (editing) {
    return (
      <RuleEditor
        key={editing.branch?.name ?? 'new'}
        branches={branches}
        members={members}
        initial={editing.branch}
        busy={busy}
        onCancel={() => setEditing(null)}
        onSave={async (branch, rule) => {
          if (await onSave(branch, rule)) setEditing(null)
        }}
      />
    )
  }

  return (
    <SettingsCard
      title="Branch protection rules"
      description="Protect important branches: block direct pushes and deletion, and require reviews or status checks before merging."
      actions={
        <Button disabled={unprotected === 0} onClick={() => setEditing({})}>
          Add rule
        </Button>
      }
    >
      {protectedBranches.length === 0 ? (
        <EmptyState bordered={false}>
          No branch protection rules yet. Add a rule to protect a branch such as your default branch.
        </EmptyState>
      ) : (
        <ul className="nipa-protect-rules" aria-label="Branch protection rules">
          {protectedBranches.map((branch) => (
            <li key={branch.id}>
              <ShieldLockIcon className="nipa-protect-rule-icon" />
              <div style={{ minWidth: 0, flex: 1 }}>
                <div className="nipa-protect-rule-name">
                  <Mono>{branch.name}</Mono>
                  {branch.is_default && <Label size="small">Default</Label>}
                </div>
                <Text as="p" className="nipa-protect-rule-summary">
                  {protectionSummary(branch).join(' · ')}
                </Text>
              </div>
              <Button size="small" leadingVisual={PencilIcon} onClick={() => setEditing({ branch })}>
                Edit
              </Button>
              <IconButton
                size="small"
                variant="danger"
                icon={TrashIcon}
                aria-label={`Delete protection rule for ${branch.name}`}
                disabled={busy}
                onClick={() => onRemove(branch)}
              />
            </li>
          ))}
        </ul>
      )}
    </SettingsCard>
  )
}
