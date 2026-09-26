import { useState } from 'react'
import { Button, Stack, Text } from '@primer/react'
import { useParams } from 'react-router-dom'
import {
  closeMergeRequest,
  getMergeRequest,
  getMergeRequestDiff,
  getMergeRequestReviewState,
  getMergeRequestTimeline,
  listMergeRequestReviewRequests,
  listMergeRequestReviews,
  listMergeRequestThreads,
  mergeMergeRequest,
  reopenMergeRequest,
} from '../api/endpoints'
import { useAuth } from '../auth'
import { MergeRequestDiff } from '../components/repo/MergeRequestDiff'
import { DiffAnchor, ReviewPanel } from '../components/repo/ReviewPanel'
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
  const [draftAnchor, setDraftAnchor] = useState<DiffAnchor | null>(null)
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null)

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

      <Stack direction="vertical" gap="normal">
        <Text style={{ fontWeight: 600 }}>Reviews</Text>
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
          draftAnchor={draftAnchor}
          activeThreadId={activeThreadId}
          canWrite={canWrite && request?.status === 'open'}
          onChanged={reloadAll}
          onCancelDraft={() => setDraftAnchor(null)}
          onOpenThread={setActiveThreadId}
        />
      </Stack>

      {diffLoading && <Loading />}
      <MergeRequestDiff
        files={diff?.files ?? []}
        threads={threads ?? []}
        canComment={canWrite && request?.status === 'open'}
        onStartThread={(filePath, oldLine, newLine) => {
          setActiveThreadId(null)
          setDraftAnchor({ filePath, oldLine, newLine })
        }}
        onOpenThread={setActiveThreadId}
      />
    </RepoPageShell>
  )
}
