import { useState } from 'react'
import { Button, Checkbox, FormControl, Select, Textarea, TextInput } from '@primer/react'
import { useNavigate, useParams } from 'react-router-dom'
import { createMergeRequest } from '../api/endpoints'
import { useAuth } from '../auth'
import { MemberMultiPicker } from '../components/repo/MemberPicker'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { FormCard } from '../components/SettingsCard'
import { ErrorBanner, NoPermission } from '../components/ui'
import { errorMessage } from '../hooks'
import { useOrgMembers } from '../orgHooks'

export default function NewMergeRequestPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const { branches, branchesLoading, permissionsLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [draft, setDraft] = useState(false)
  const [reviewers, setReviewers] = useState<string[]>([])
  const [assignees, setAssignees] = useState<string[]>([])
  const { me } = useAuth()
  const { data: members } = useOrgMembers(org)
  const reviewerCandidates = (members ?? []).filter((member) => member.user_id !== me?.id)
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const canSubmit = title.trim() !== '' && source !== '' && target !== '' && !saving
  const shell = {
    org,
    project,
    active: 'merges' as const,
    rev: defaultBranch,
    canAdmin,
    canWrite,
    heading: 'New merge request',
    crumbs: [{ label: 'Merge requests', to: `/${org}/${project}/merges` }, { label: 'New merge request' }],
  }

  if (branchesLoading || permissionsLoading || !canWrite) {
    return (
      <RepoPageShell {...shell}>
        <NoPermission loading={branchesLoading || permissionsLoading}>
          You need project write permission to create merge requests.
        </NoPermission>
      </RepoPageShell>
    )
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setActionError(null)
    try {
      const request = await createMergeRequest(org, project, title.trim(), description, source, target, draft, {
        reviewerIds: reviewers,
        assigneeIds: assignees,
      })
      navigate(`/${org}/${project}/merges/${request.number}`)
    } catch (err) {
      setActionError(errorMessage(err))
      setSaving(false)
    }
  }

  return (
    <RepoPageShell {...shell}>
      <ErrorBanner error={actionError} />
      <FormCard onSubmit={handleSubmit}>
        <FormControl required>
          <FormControl.Label>Title</FormControl.Label>
          <TextInput block autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
        </FormControl>
        <FormControl>
          <FormControl.Label>Description</FormControl.Label>
          <Textarea block rows={4} value={description} onChange={(event) => setDescription(event.target.value)} />
        </FormControl>
        <FormControl required>
          <FormControl.Label>Source branch</FormControl.Label>
          <Select block value={source} onChange={(event) => setSource(event.target.value)}>
            <Select.Option value="">select a branch</Select.Option>
            {branches?.map((branch) => (
              <Select.Option key={branch.id} value={branch.name}>
                {branch.name}
              </Select.Option>
            ))}
          </Select>
        </FormControl>
        <FormControl required>
          <FormControl.Label>Target branch</FormControl.Label>
          <Select block value={target} onChange={(event) => setTarget(event.target.value)}>
            <Select.Option value="">select a branch</Select.Option>
            {branches?.map((branch) => (
              <Select.Option key={branch.id} value={branch.name}>
                {branch.name}
                {branch.is_default ? ' (default)' : ''}
              </Select.Option>
            ))}
          </Select>
        </FormControl>
        {(members ?? []).length > 0 && (
          <>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span style={{ fontSize: 14, fontWeight: 600 }}>Reviewers</span>
              <MemberMultiPicker
                label="Add reviewer"
                placeholder="Add reviewer…"
                members={reviewerCandidates}
                selected={reviewers}
                onChange={setReviewers}
                disabled={saving}
              />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span style={{ fontSize: 14, fontWeight: 600 }}>Assignees</span>
              <MemberMultiPicker
                label="Add assignee"
                placeholder="Add assignee…"
                members={members ?? []}
                selected={assignees}
                onChange={setAssignees}
                disabled={saving}
              />
            </div>
          </>
        )}
        <FormControl>
          <Checkbox checked={draft} onChange={(event) => setDraft(event.target.checked)} />
          <FormControl.Label>Create as draft (cannot be merged until marked ready)</FormControl.Label>
        </FormControl>
        <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving} style={{ alignSelf: 'flex-start' }}>
          Create merge request
        </Button>
      </FormCard>
    </RepoPageShell>
  )
}
