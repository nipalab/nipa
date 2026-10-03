import { useState } from 'react'
import { Button, Link as PrimerLink } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listMergeRequests } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function MergeRequestsPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const [status, setStatus] = useState('open')
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: requests, error, loading } = useAsync(
    () => listMergeRequests(org, project, status),
    [org, project, status],
  )

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="merges"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Merge requests"
      actions={
        canWrite && (
          <Button variant="primary" onClick={() => navigate(`/${org}/${project}/merges/new`)}>
            New merge request
          </Button>
        )
      }
    >
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <select value={status} onChange={(event) => setStatus(event.target.value)} style={{ padding: 4 }}>
          <option value="open">open</option>
          <option value="merged">merged</option>
          <option value="closed">closed</option>
          <option value="">all</option>
        </select>
      </div>
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && requests && requests.length === 0 && <EmptyState>No merge requests.</EmptyState>}
      {requests?.map((request) => (
        <div
          key={request.id}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <PrimerLink as={Link} to={`/${org}/${project}/merges/${request.number}`} style={{ fontWeight: 600 }}>
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
    </RepoPageShell>
  )
}
