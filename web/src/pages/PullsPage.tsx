import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { createMergeRequest, listMergeRequests } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function PullsPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const [status, setStatus] = useState('open')
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: requests, error, loading, reload } = useAsync(
    () => listMergeRequests(org, project, status),
    [org, project, status],
  )
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

  async function handleCreate(event: React.FormEvent) {
    event.preventDefault()
    setActionError(null)
    try {
      const request = await createMergeRequest(org, project, title, description, source, target)
      setTitle('')
      setDescription('')
      reload()
      navigate(`/${org}/${project}/pulls/${request.number}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="pulls"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Merge requests"
    >
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <select value={status} onChange={(event) => setStatus(event.target.value)} style={{ padding: 4 }}>
          <option value="open">open</option>
          <option value="merged">merged</option>
          <option value="closed">closed</option>
          <option value="">all</option>
        </select>
      </div>
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}
      {!loading && requests && requests.length === 0 && <EmptyState>No merge requests.</EmptyState>}
      {requests?.map((request) => (
        <div
          key={request.id}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <PrimerLink as={Link} to={`/${org}/${project}/pulls/${request.number}`} style={{ fontWeight: 600 }}>
            #{request.number} {request.title}
          </PrimerLink>
          <div style={{ color: 'var(--fgColor-muted)', fontSize: 13 }}>
            {request.source_branch} → {request.target_branch} · <StatusLabel status={request.status} />
            {request.review && (
              <>
                {' · '}
                <span style={{ color: 'var(--fgColor-success)' }}>{request.review.approvals} approved</span>
                {request.review.changes_requested > 0 && (
                  <span style={{ color: 'var(--fgColor-danger)' }}>
                    {' '}
                    · {request.review.changes_requested} changes requested
                  </span>
                )}
              </>
            )}
          </div>
        </div>
      ))}

      <form
        onSubmit={handleCreate}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>New merge request</strong>
          <FormControl required>
            <FormControl.Label>Title</FormControl.Label>
            <TextInput block value={title} onChange={(event) => setTitle(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Description</FormControl.Label>
            <TextInput block value={description} onChange={(event) => setDescription(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Source branch</FormControl.Label>
            <select value={source} onChange={(event) => setSource(event.target.value)} style={{ padding: 6 }}>
              <option value="">select a branch</option>
              {branches?.map((branch) => (
                <option key={branch.id} value={branch.name}>
                  {branch.name}
                </option>
              ))}
            </select>
          </FormControl>
          <FormControl required>
            <FormControl.Label>Target branch</FormControl.Label>
            <select value={target} onChange={(event) => setTarget(event.target.value)} style={{ padding: 6 }}>
              <option value="">select a branch</option>
              {branches?.map((branch) => (
                <option key={branch.id} value={branch.name}>
                  {branch.name}
                  {branch.is_default ? ' (default)' : ''}
                </option>
              ))}
            </select>
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Open merge request
          </Button>
        </Stack>
      </form>
    </RepoPageShell>
  )
}
