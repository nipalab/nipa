import { useState } from 'react'
import { Button, Label, Link as PrimerLink, TextInput } from '@primer/react'
import {
  GitMergeIcon,
  GitPullRequestClosedIcon,
  GitPullRequestDraftIcon,
  GitPullRequestIcon,
  ListUnorderedIcon,
  SearchIcon,
  XIcon,
  type Icon,
} from '@primer/octicons-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listMergeRequests } from '../api/endpoints'
import type { MergeRequestResponse } from '../api/models'
import { ActorAvatar, actorName } from '../components/ActorAvatar'
import { FilterButton, branchOptions, memberOptions } from '../components/repo/MemberPicker'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { Timestamp } from '../components/Timestamp'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'
import { useOrgMembers } from '../orgHooks'

const STATUS_TABS: { value: string; label: string; Icon: Icon }[] = [
  { value: 'open', label: 'Open', Icon: GitPullRequestIcon },
  { value: 'draft', label: 'Draft', Icon: GitPullRequestDraftIcon },
  { value: 'merged', label: 'Merged', Icon: GitMergeIcon },
  { value: 'closed', label: 'Closed', Icon: GitPullRequestClosedIcon },
  { value: '', label: 'All', Icon: ListUnorderedIcon },
]

function stateIcon(request: MergeRequestResponse): { Icon: Icon; color: string; label: string } {
  if (request.status === 'merged') return { Icon: GitMergeIcon, color: 'var(--fgColor-done)', label: 'Merged' }
  if (request.status === 'closed') return { Icon: GitPullRequestClosedIcon, color: 'var(--fgColor-closed)', label: 'Closed' }
  if (request.draft) return { Icon: GitPullRequestDraftIcon, color: 'var(--fgColor-muted)', label: 'Draft' }
  return { Icon: GitPullRequestIcon, color: 'var(--fgColor-open)', label: 'Open' }
}

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
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
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
  const filtered = Boolean(search || author || assignee || source || target || status !== 'open')
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
      heading="Merge requests"
      actions={
        canWrite && (
          <Button variant="primary" onClick={() => navigate(`/${org}/${project}/merges/new`)}>
            New merge request
          </Button>
        )
      }
    >
      <TextInput
        block
        leadingVisual={SearchIcon}
        aria-label="Search merge requests"
        placeholder="Search titles and descriptions"
        value={search}
        onChange={(event) => changeFilter(() => setSearch(event.target.value))}
      />
      <ErrorBanner error={error ?? moreError} />
      <div className="nipa-list-box">
        <div className="nipa-list-header">
          <nav className="nipa-state-tabs" aria-label="Merge request state">
            {STATUS_TABS.map((entry) => (
              <button
                key={entry.value}
                type="button"
                aria-pressed={status === entry.value}
                className={status === entry.value ? 'nipa-state-tab nipa-state-tab-active' : 'nipa-state-tab'}
                onClick={() => changeFilter(() => setStatus(entry.value))}
              >
                <entry.Icon />
                {entry.label}
              </button>
            ))}
          </nav>
          <div className="nipa-list-filters">
            <FilterButton
              label="Author"
              title="Filter by author"
              options={memberOptions(members ?? [])}
              value={author}
              onChange={(value) => changeFilter(() => setAuthor(value))}
            />
            <FilterButton
              label="Assignee"
              title="Filter by assignee"
              options={memberOptions(members ?? [])}
              value={assignee}
              onChange={(value) => changeFilter(() => setAssignee(value))}
            />
            <FilterButton
              label="Source"
              title="Filter by source branch"
              options={branchOptions(branches ?? [])}
              value={source}
              onChange={(value) => changeFilter(() => setSource(value))}
            />
            <FilterButton
              label="Target"
              title="Filter by target branch"
              options={branchOptions(branches ?? [])}
              value={target}
              onChange={(value) => changeFilter(() => setTarget(value))}
            />
          </div>
        </div>
        {filtered && (
          <div className="nipa-list-clear">
            <Button
              variant="invisible"
              size="small"
              leadingVisual={XIcon}
              onClick={() =>
                changeFilter(() => {
                  setSearch('')
                  setAuthor('')
                  setAssignee('')
                  setSource('')
                  setTarget('')
                  setStatus('open')
                })
              }
            >
              Clear current search and filters
            </Button>
          </div>
        )}
        {loading && <Loading />}
        {!loading && requests.length === 0 && (
          <EmptyState bordered={false}>{filtered ? 'No merge requests match these filters.' : 'No merge requests.'}</EmptyState>
        )}
        {requests.map((request) => {
          const state = stateIcon(request)
          const authorName = (members ?? []).find((member) => member.user_id === request.created_by)
          return (
            <div key={request.id} className="nipa-list-row">
              <span className="nipa-list-row-icon" style={{ color: state.color }} title={state.label}>
                <state.Icon />
              </span>
              <div style={{ minWidth: 0, flex: 1 }}>
                <PrimerLink as={Link} to={`/${org}/${project}/merges/${request.number}`} className="nipa-list-row-title">
                  {request.title}
                </PrimerLink>
                {request.draft && request.status === 'open' && (
                  <Label size="small" style={{ marginLeft: 8 }}>
                    Draft
                  </Label>
                )}
                <div className="nipa-list-row-meta">
                  #{request.number} opened <Timestamp value={request.created_at} />
                  {authorName && <> by {authorName.name || authorName.email}</>} ·{' '}
                  <Mono>{request.source_branch}</Mono> → <Mono>{request.target_branch}</Mono>
                  {request.review && request.review.changes_requested > 0 ? (
                    <span style={{ color: 'var(--fgColor-danger)' }}> · Changes requested</span>
                  ) : request.review && request.review.approvals > 0 ? (
                    <span style={{ color: 'var(--fgColor-success)' }}>
                      {' '}
                      · {request.review.approvals} approved
                    </span>
                  ) : null}
                </div>
              </div>
              {(request.assignees ?? []).length > 0 && (
                <span className="nipa-list-row-assignees">
                  {(request.assignees ?? []).slice(0, 3).map((entry) => (
                    <span key={entry.user_id} title={actorName(entry)}>
                      <ActorAvatar actor={entry} size={20} />
                    </span>
                  ))}
                </span>
              )}
            </div>
          )
        })}
      </div>
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
