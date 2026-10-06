import { useState } from 'react'
import { Button, Link as PrimerLink, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listMergeRequests, listOrgMembers } from '../api/endpoints'
import type { MergeRequestResponse } from '../api/models'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function MergeRequestsPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const [status, setStatus] = useState('open')
  const [author, setAuthor] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [extra, setExtra] = useState<MergeRequestResponse[]>([])
  const [extraCursor, setExtraCursor] = useState<string | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState<string | null>(null)
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data, error, loading } = useAsync(
    () => listMergeRequests(org, project, { status, author, source, target }),
    [org, project, status, author, source, target],
  )
  const { data: members } = useAsync(() => listOrgMembers(org), [org])

  const requests = [...(data?.merge_requests ?? []), ...extra]
  const nextCursor = extraCursor ?? data?.next_cursor ?? ''

  function changeFilter(apply: () => void) {
    apply()
    setExtra([])
    setExtraCursor(null)
  }

  async function loadMore() {
    if (!nextCursor || loadingMore) return
    setLoadingMore(true)
    setMoreError(null)
    try {
      const page = await listMergeRequests(org, project, { status, author, source, target, after: nextCursor })
      setExtra((current) => [...current, ...page.merge_requests])
      setExtraCursor(page.next_cursor ?? '')
    } catch (err) {
      setMoreError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoadingMore(false)
    }
  }

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
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'flex-end',
          gap: 8,
          flexWrap: 'wrap',
        }}
      >
        <TextInput
          aria-label="Filter by source branch"
          placeholder="source branch"
          value={source}
          onChange={(event) => changeFilter(() => setSource(event.target.value))}
        />
        <TextInput
          aria-label="Filter by target branch"
          placeholder="target branch"
          value={target}
          onChange={(event) => changeFilter(() => setTarget(event.target.value))}
        />
        <select
          aria-label="Filter by author"
          value={author}
          onChange={(event) => changeFilter(() => setAuthor(event.target.value))}
          style={{ padding: 4 }}
        >
          <option value="">any author</option>
          {(members ?? []).map((member) => (
            <option key={member.user_id} value={member.user_id}>
              {member.name}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by status"
          value={status}
          onChange={(event) => changeFilter(() => setStatus(event.target.value))}
          style={{ padding: 4 }}
        >
          <option value="open">open</option>
          <option value="merged">merged</option>
          <option value="closed">closed</option>
          <option value="">all</option>
        </select>
      </div>
      <ErrorBanner error={error ?? moreError} />
      {loading && <Loading />}
      {!loading && requests.length === 0 && <EmptyState>No merge requests.</EmptyState>}
      {requests.map((request) => (
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
      {nextCursor && !loading && (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          <Button onClick={loadMore} loading={loadingMore}>
            Load more
          </Button>
        </div>
      )}
    </RepoPageShell>
  )
}
