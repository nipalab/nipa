import { useState } from 'react'
import { Button, Link as PrimerLink, Select, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listMergeRequests } from '../api/endpoints'
import type { MergeRequestResponse } from '../api/models'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { Row, RowList } from '../components/RowList'
import { StateBadge } from '../components/StateBadge'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'
import { useOrgMembers } from '../orgHooks'

export default function MergeRequestsPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const [status, setStatus] = useState('open')
  const [author, setAuthor] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [search, setSearch] = useState('')
  const [assignee, setAssignee] = useState('')
  const [extra, setExtra] = useState<MergeRequestResponse[]>([])
  const [extraCursor, setExtraCursor] = useState<string | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState<string | null>(null)
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const draftFilter = status === 'draft'
  const listParams = (after?: string) => ({
    ...(draftFilter ? { status: 'open', draft: true } : { status }),
    author,
    source,
    target,
    search,
    assignee,
    ...(after ? { after } : {}),
  })
  const { data, error, loading } = useAsync(
    () => listMergeRequests(org, project, listParams()),
    [org, project, status, author, source, target, search, assignee],
  )
  const { data: members } = useOrgMembers(org)

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
      const page = await listMergeRequests(org, project, listParams(nextCursor))
      setExtra((current) => [...current, ...page.merge_requests])
      setExtraCursor(page.next_cursor ?? '')
    } catch (err) {
      setMoreError(errorMessage(err))
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
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--base-size-8)', flexWrap: 'wrap' }}>
        <TextInput
          aria-label="Search merge requests"
          placeholder="search title or description"
          value={search}
          onChange={(event) => changeFilter(() => setSearch(event.target.value))}
        />
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
        <Select aria-label="Filter by author" value={author} onChange={(event) => changeFilter(() => setAuthor(event.target.value))}>
          <Select.Option value="">any author</Select.Option>
          {(members ?? []).map((member) => (
            <Select.Option key={member.user_id} value={member.user_id}>
              {member.name}
            </Select.Option>
          ))}
        </Select>
        <Select aria-label="Filter by assignee" value={assignee} onChange={(event) => changeFilter(() => setAssignee(event.target.value))}>
          <Select.Option value="">any assignee</Select.Option>
          {(members ?? []).map((member) => (
            <Select.Option key={member.user_id} value={member.user_id}>
              {member.name}
            </Select.Option>
          ))}
        </Select>
        <Select aria-label="Filter by status" value={status} onChange={(event) => changeFilter(() => setStatus(event.target.value))}>
          <Select.Option value="open">open</Select.Option>
          <Select.Option value="draft">draft</Select.Option>
          <Select.Option value="merged">merged</Select.Option>
          <Select.Option value="closed">closed</Select.Option>
          <Select.Option value="">all</Select.Option>
        </Select>
      </div>
      <ErrorBanner error={error ?? moreError} />
      {loading && <Loading />}
      {!loading && requests.length === 0 && <EmptyState>No merge requests.</EmptyState>}
      {requests.length > 0 && (
        <RowList>
          {requests.map((request, index) => (
            <Row
              key={request.id}
              first={index === 0}
              title={
                <PrimerLink as={Link} to={`/${org}/${project}/merges/${request.number}`}>
                  #{request.number} {request.title}
                </PrimerLink>
              }
              meta={
                <>
                  {request.source_branch} → {request.target_branch} ·{' '}
                  <StateBadge status={request.draft ? 'draft' : request.status} />
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
                </>
              }
            />
          ))}
        </RowList>
      )}
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
