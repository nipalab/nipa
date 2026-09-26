import { useState } from 'react'
import { Button, Stack, Text } from '@primer/react'
import { useParams } from 'react-router-dom'
import {
  addMergeRequestComment,
  closeMergeRequest,
  deleteMergeRequestComment,
  getMergeRequest,
  getMergeRequestDiff,
  getMergeRequestReviewState,
  getMergeRequestTimeline,
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
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading, Mono, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function PullPage() {
  const { org = '', project = '', id = '' } = useParams()
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

  const canComment = canWrite && request?.status === 'open'

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

  const mergeable = request?.mergeability?.status === 'mergeable'
  const canToggle = request?.status === 'open' || request?.status === 'closed'

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
      <ErrorBanner error={actionError ?? error ?? diffError} />
      {loading && <Loading />}
      {request && (
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
        </Stack>
      )}

      <ReviewPanel
        org={org}
        project={project}
        id={id}
        me={me?.id}
        request={request ?? undefined}
        state={state}
        reviews={reviews ?? []}
        threads={threads ?? []}
        reviewRequests={reviewRequests ?? []}
        timeline={timeline ?? []}
        canWrite={canComment}
        busy={commentBusy}
        onChanged={reloadAll}
        onReplyThread={(threadId, body) => runComment(() => replyMergeRequestThread(org, project, id, threadId, body))}
        onResolveThread={(threadId, resolved) =>
          runComment(() => resolveMergeRequestThread(org, project, id, threadId, resolved))
        }
        onEditComment={(threadId, commentId, body) =>
          runComment(() => updateMergeRequestComment(org, project, id, threadId, commentId, body))
        }
        onDeleteComment={(threadId, commentId) =>
          runComment(() => deleteMergeRequestComment(org, project, id, threadId, commentId))
        }
      />

      {diffLoading && <Loading />}
      <DiffView
        files={diff?.files ?? []}
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
        onReplyThread={(threadId, body) => runComment(() => replyMergeRequestThread(org, project, id, threadId, body))}
        onResolveThread={(threadId, resolved) =>
          runComment(() => resolveMergeRequestThread(org, project, id, threadId, resolved))
        }
        onEditComment={(threadId, commentId, body) =>
          runComment(() => updateMergeRequestComment(org, project, id, threadId, commentId, body))
        }
        onDeleteComment={(threadId, commentId) =>
          runComment(() => deleteMergeRequestComment(org, project, id, threadId, commentId))
        }
      />
    </RepoPageShell>
  )
}
