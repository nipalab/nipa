import { useState } from 'react'
import { Button, Checkbox, FormControl, Text, Textarea, TextInput } from '@primer/react'
import { ArrowLeftIcon } from '@primer/octicons-react'
import { useNavigate, useParams } from 'react-router-dom'
import { createMergeRequest } from '../api/endpoints'
import { useAuth } from '../auth'
import { BranchPicker, MemberMultiPicker } from '../components/repo/MemberPicker'
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

  const targetBranch = target || defaultBranch
  const canSubmit = title.trim() !== '' && source !== '' && targetBranch !== '' && source !== targetBranch && !saving
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
      const request = await createMergeRequest(org, project, title.trim(), description, source, targetBranch, draft, {
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
        <div className="nipa-branch-pair">
          <div>
            <span className="nipa-field-label">Base (target)</span>
            <BranchPicker label="Choose the target branch" branches={branches ?? []} value={targetBranch} onChange={setTarget} />
          </div>
          <ArrowLeftIcon className="nipa-branch-arrow" />
          <div>
            <span className="nipa-field-label">Compare (source)</span>
            <BranchPicker label="Choose the source branch" branches={branches ?? []} value={source} onChange={setSource} />
          </div>
        </div>
        {source !== '' && source === targetBranch && (
          <Text style={{ color: 'var(--fgColor-attention)', fontSize: 12 }}>The source and target branches are the same.</Text>
        )}
        {(members ?? []).length > 0 && (
          <>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span className="nipa-field-label">Reviewers</span>
              <MemberMultiPicker
                label="Add reviewers"
                members={reviewerCandidates}
                selected={reviewers}
                onChange={setReviewers}
                disabled={saving}
              />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span className="nipa-field-label">Assignees</span>
              <MemberMultiPicker
                label="Add assignees"
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
