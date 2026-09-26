import { useState } from 'react'
import { Button, Link as PrimerLink, Stack, Text } from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import {
  addMergeRequestComment,
  closeMergeRequest,
  deleteMergeRequestComment,
  getMergeRequest,
  getMergeRequestDiff,
  getMergeRequestReviewState,
  getMergeRequestTimeline,
  listMergeRequestCommits,
  listMergeRequestReviewRequests,
  listMergeRequestReviews,
  listMergeRequestThreads,
  mergeMergeRequest,
  reopenMergeRequest,
  replyMergeRequestThread,
  resolveMergeRequestThread,
  updateMergeRequestComment,
} from '../api/endpoints'
import { useAuth } from '../auth'
import { DiffAnchor, DiffView } from '../components/repo/DiffView'
import { ReviewPanel } from '../components/repo/ReviewPanel'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { Tabs } from '../components/repo/Tabs'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, Mono, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function PullPage() {
  const { org = '', project = '', id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const { me } = useAuth()
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: request, error, loading, reload } = useAsync(
    () => getMergeRequest(org, project, id),
    [org, project, id],
  )
  const { data: diff, error: diffError, loading: diffLoading } = useAsync(
    () => getMergeRequestDiff(org, project, id),
    [org, project, id],
  )
  const { data: commits, error: commitsError, loading: commitsLoading } = useAsync(
    () => listMergeRequestCommits(org, project, id),
    [org, project, id],
  )
  const { data: state, reload: reloadState } = useAsync(
    () => getMergeRequestReviewState(org, project, id),
    [org, project, id],
  )
  const { data: reviews, reload: reloadReviewList } = useAsync(
    () => listMergeRequestReviews(org, project, id),
    [org, project, id],
  )
  const { data: threads, reload: reloadThreads } = useAsync(
    () => listMergeRequestThreads(org, project, id),
    [org, project, id],
  )
  const { data: reviewRequests, reload: reloadReviewRequests } = useAsync(
    () => listMergeRequestReviewRequests(org, project, id),
    [org, project, id],
  )
  const { data: timeline, reload: reloadTimeline } = useAsync(
    () => getMergeRequestTimeline(org, project, id),
    [org, project, id],
  )
  const [actionError, setActionError] = useState<string | null>(null)
  const [commentBusy, setCommentBusy] = useState(false)
  const [commentError, setCommentError] = useState<string | null>(null)
  const [draftAnchor, setDraftAnchor] = useState<DiffAnchor | null>(null)

  const tab = params.get('tab') ?? 'overview'
  const files = diff?.files ?? []
  const canComment = canWrite && request?.status === 'open'

  function selectTab(next: string) {
    const nextParams = new URLSearchParams(params)
    if (next === 'overview') {
      nextParams.delete('tab')
    } else {
      nextParams.set('tab', next)
    }
    setParams(nextParams, { replace: true })
  }

  function reloadAll() {
    reload()
    reloadState()
    reloadReviewList()
    reloadThreads()
    reloadReviewRequests()
    reloadTimeline()
  }

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reloadAll()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function runComment(action: () => Promise<unknown>, inline = false) {
    setCommentBusy(true)
    setCommentError(null)
    try {
      await action()
      reloadThreads()
      return true
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      if (inline) {
        setCommentError(message)
      } else {
        setActionError(message)
      }
      return false
    } finally {
      setCommentBusy(false)
    }
  }

  async function submitDraft(anchor: DiffAnchor, body: string) {
    const ok = await runComment(
      () =>
        addMergeRequestComment(org, project, id, {
          file_path: anchor.filePath,
          old_line: anchor.oldLine,
          new_line: anchor.newLine,
          body,
        }),
      true,
    )
    if (ok) setDraftAnchor(null)
  }

  function threadActions() {
    return {
      onReplyThread: (threadId: string, body: string) =>
        runComment(() => replyMergeRequestThread(org, project, id, threadId, body)),
      onResolveThread: (threadId: string, resolved: boolean) =>
        runComment(() => resolveMergeRequestThread(org, project, id, threadId, resolved)),
      onEditComment: (threadId: string, commentId: string, body: string) =>
        runComment(() => updateMergeRequestComment(org, project, id, threadId, commentId, body)),
      onDeleteComment: (threadId: string, commentId: string) =>
        runComment(() => deleteMergeRequestComment(org, project, id, threadId, commentId)),
    }
  }

  const mergeable = request?.mergeability?.status === 'mergeable'
  const canToggle = request?.status === 'open' || request?.status === 'closed'
  const totalAdditions = files.reduce((sum, file) => sum + file.additions, 0)
  const totalDeletions = files.reduce((sum, file) => sum + file.deletions, 0)

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="pulls"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading={request ? `#${request.number} ${request.title}` : 'Merge request'}
    >
      <Text style={{ color: 'var(--fgColor-muted)' }}>
        {request?.source_branch ?? ''} → {request?.target_branch ?? ''}
      </Text>
      <ErrorBanner error={actionError ?? error ?? diffError ?? commitsError} />
      {loading && <Loading />}

      <Tabs
        tabs={[
          { id: 'overview', label: 'Overview' },
          { id: 'commits', label: 'Commits', count: commits?.length },
          { id: 'files', label: 'File changes', count: diff?.files.length },
        ]}
        active={tab}
        onChange={selectTab}
      />

      {tab === 'overview' && request && (
        <Stack direction="vertical" gap="normal">
          <Text>
            Status: <StatusLabel status={request.status} />{' '}
            {request.mergeability && (
              <>
                · mergeability: <StatusLabel status={request.mergeability.status} />
              </>
            )}
          </Text>
          {request.description && <Text>{request.description}</Text>}
          <Mono>#{request.number}</Mono>
          <Stack direction="horizontal" gap="normal">
            {request.status === 'open' && (
              <Button
                variant="primary"
                disabled={!mergeable}
                onClick={() => run(() => mergeMergeRequest(org, project, id))}
              >
                Merge
              </Button>
            )}
            {canToggle && (
              <Button
                onClick={() =>
                  run(() =>
                    request.status === 'open'
                      ? closeMergeRequest(org, project, id)
                      : reopenMergeRequest(org, project, id),
                  )
                }
              >
                {request.status === 'open' ? 'Close' : 'Reopen'}
              </Button>
            )}
          </Stack>
          {request.status === 'open' && !mergeable && (
            <Text style={{ color: 'var(--fgColor-attention)' }}>
              The source branch must contain the target before it can merge (update the branch first).
            </Text>
          )}

          <ReviewPanel
            org={org}
            project={project}
            id={id}
            me={me?.id}
            request={request}
            state={state}
            reviews={reviews ?? []}
            threads={threads ?? []}
            reviewRequests={reviewRequests ?? []}
            timeline={timeline ?? []}
            canWrite={canComment}
            busy={commentBusy}
            onChanged={reloadAll}
            {...threadActions()}
          />
        </Stack>
      )}

      {tab === 'commits' && (
        <Stack direction="vertical" gap="normal">
          {commitsLoading && <Loading />}
          {!commitsLoading && commits && commits.length === 0 && <EmptyState>No commits on this request.</EmptyState>}
          {commits && commits.length > 0 && (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <tbody>
                {commits.map((commit) => (
                  <tr key={commit.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                    <td style={{ padding: '6px 4px' }}>
                      <PrimerLink
                        as={Link}
                        to={`/${org}/${project}/commits/${commit.id}`}
                        style={{ fontWeight: 600 }}
                      >
                        {commit.message}
                      </PrimerLink>
                    </td>
                    <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                      {commit.author_name || commit.author_email || ''}
                    </td>
                    <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                      <Mono>{commit.id.slice(0, 10)}</Mono>
                    </td>
                    <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                      {new Date(commit.created_at).toLocaleString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Stack>
      )}

      {tab === 'files' && (
        <Stack direction="vertical" gap="normal">
          <Text style={{ color: 'var(--fgColor-muted)' }}>
            {files.length} file{files.length === 1 ? '' : 's'} changed{' '}
            <Text style={{ color: 'var(--fgColor-success)' }}>+{totalAdditions}</Text>{' '}
            <Text style={{ color: 'var(--fgColor-danger)' }}>-{totalDeletions}</Text>
          </Text>
          {files.length > 1 && (
            <div
              style={{
                border: '1px solid var(--borderColor-muted)',
                borderRadius: 6,
                maxHeight: 220,
                overflowY: 'auto',
              }}
            >
              {files.map((file) => {
                const open = (threads ?? []).filter(
                  (thread) =>
                    (thread.file_path === file.path ||
                      (Boolean(file.old_path) && thread.file_path === file.old_path)) &&
                    !thread.resolved,
                ).length
                return (
                  <a
                    key={file.path}
                    href={`#file-${file.path}`}
                    style={{
                      display: 'flex',
                      gap: 8,
                      alignItems: 'center',
                      padding: '4px 10px',
                      color: 'inherit',
                      textDecoration: 'none',
                      borderBottom: '1px solid var(--borderColor-muted)',
                    }}
                  >
                    <Mono>{file.old_path ? `${file.old_path} → ${file.path}` : file.path}</Mono>
                    {!file.binary && (
                      <>
                        <span style={{ color: 'var(--fgColor-success)', fontSize: 12 }}>+{file.additions}</span>
                        <span style={{ color: 'var(--fgColor-danger)', fontSize: 12 }}>-{file.deletions}</span>
                      </>
                    )}
                    {open > 0 && (
                      <span style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
                        {open} open thread{open === 1 ? '' : 's'}
                      </span>
                    )}
                  </a>
                )
              })}
            </div>
          )}
          {diffLoading && <Loading />}
          <DiffView
            files={files}
            threads={threads ?? []}
            canComment={canComment}
            me={me?.id}
            busy={commentBusy}
            draftAnchor={draftAnchor}
            draftBusy={commentBusy}
            draftError={commentError}
            onStartThread={(anchor) => {
              setCommentError(null)
              setDraftAnchor(anchor)
            }}
            onCancelDraft={() => {
              setDraftAnchor(null)
              setCommentError(null)
            }}
            onSubmitDraft={submitDraft}
            {...threadActions()}
          />
        </Stack>
      )}
    </RepoPageShell>
  )
}
