import { useState } from 'react'
import { Button, Checkbox, FormControl, Stack, StateLabel, Textarea, TextInput } from '@primer/react'
import { useParams, useSearchParams } from 'react-router-dom'
import {
  addMergeRequestComment,
  deleteMergeRequestComment,
  deleteMergeRequestThread,
  getMergeRequest,
  getMergeRequestDiff,
  getMergeRequestReviewState,
  getMergeRequestTimeline,
  listMergeRequestCommits,
  listMergeRequestChecks,
  listMergeRequestReviewRequests,
  listMergeRequestReviews,
  listMergeRequestThreads,
  mergeMergeRequest,
  closeMergeRequest,
  reopenMergeRequest,
  replyMergeRequestThread,
  resolveMergeRequestThread,
  updateMergeRequest,
  updateMergeRequestComment,
} from '../api/endpoints'
import type { MergeRequestResponse } from '../api/models'
import { useAuth } from '../auth'
import { DiffAnchor } from '../components/repo/DiffView'
import { actorName, actorsFrom, type ActorLike } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { ChangesView } from '../components/repo/ChangesView'
import { MergeRequestCommits } from '../components/repo/MergeRequestCommits'
import { MergeRequestOverview } from '../components/repo/MergeRequestOverview'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { MergeRequestTabs } from '../components/repo/MergeRequestTabs'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading, Mono } from '../components/ui'
import { errorMessage, useAsync } from '../hooks'
import { useOrgMembers } from '../orgHooks'

export default function MergeRequestPage() {
  const { org = '', project = '', id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const { me } = useAuth()
  const confirm = useConfirmAction()
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
  const { data: state, error: stateError, reload: reloadState } = useAsync(
    () => getMergeRequestReviewState(org, project, id),
    [org, project, id],
  )
  const { data: reviews, error: reviewsError, reload: reloadReviewList } = useAsync(
    () => listMergeRequestReviews(org, project, id),
    [org, project, id],
  )
  const { data: threads, error: threadsError, reload: reloadThreads } = useAsync(
    () => listMergeRequestThreads(org, project, id),
    [org, project, id],
  )
  const { data: reviewRequests, error: reviewRequestsError, reload: reloadReviewRequests } = useAsync(
    () => listMergeRequestReviewRequests(org, project, id),
    [org, project, id],
  )
  const { data: timeline, error: timelineError, reload: reloadTimeline } = useAsync(
    () => getMergeRequestTimeline(org, project, id),
    [org, project, id],
  )
  const { data: members, error: membersError } = useOrgMembers(org)
  const { data: checks, error: checksError, reload: reloadChecks } = useAsync(
    () => listMergeRequestChecks(org, project, id),
    [org, project, id],
  )
  const [actionError, setActionError] = useState<string | null>(null)
  const [commentBusy, setCommentBusy] = useState(false)
  const [commentError, setCommentError] = useState<string | null>(null)
  const [draftAnchor, setDraftAnchor] = useState<DiffAnchor | null>(null)
  const [editOpen, setEditOpen] = useState(false)

  const tab = params.get('tab') ?? 'overview'
  const files = diff?.files ?? []
  const canComment = canWrite && request?.status === 'open'
  const createdBy = request?.created_by ?? ''
  const canEdit = Boolean(
    request && request.status === 'open' && (me?.id === request.created_by || canAdmin),
  )
  const author: ActorLike | undefined =
    actorsFrom(
      (threads ?? []).flatMap((thread) => [thread.created_by, ...thread.comments.map((comment) => comment.user)]),
      (reviews ?? []).flatMap((review) => [review.reviewer, review.dismissed_by]),
      (reviewRequests ?? []).flatMap((entry) => [entry.reviewer, entry.requested_by]),
      (timeline ?? []).flatMap((item) => [item.actor, item.subject]),
    ).get(createdBy) ?? (members ?? []).find((member) => member.user_id === createdBy)

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
    reloadChecks()
  }

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reloadAll()
    } catch (err) {
      setActionError(errorMessage(err))
    }
  }

  async function closeRequest() {
    const ok = await confirm({
      title: 'Close merge request?',
      content: 'The request can be reopened later.',
      confirmLabel: 'Close merge request',
    })
    if (ok) await run(() => closeMergeRequest(org, project, id))
  }

  async function runComment(action: () => Promise<unknown>, inline = false) {
    setCommentBusy(true)
    setCommentError(null)
    try {
      await action()
      reloadThreads()
      return true
    } catch (err) {
      const message = errorMessage(err)
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
      onDeleteThread: (threadId: string) => runComment(() => deleteMergeRequestThread(org, project, id, threadId)),
      canAdmin,
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
      crumbs={[{ label: 'Merge requests', to: `/${org}/${project}/merges` }, { label: `#${id}` }]}
      heading={
        request && (
          <span>
            {request.title}{' '}
            <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}>#{request.number}</span>
          </span>
        )
      }
      actions={
        <>
          {canEdit && request?.draft && (
            <Button size="small" onClick={() => void run(() => updateMergeRequest(org, project, id, '', '', false))}>
              Ready for review
            </Button>
          )}
          {canEdit && (
            <Button size="small" onClick={() => setEditOpen(true)}>
              Edit
            </Button>
          )}
        </>
      }
    >
      {request && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            flexWrap: 'wrap',
            color: 'var(--fgColor-muted)',
            fontSize: 13,
          }}
        >
          <StateLabel status={request.draft && request.status === 'open' ? 'draft' : stateLabelStatus(request.status)}>
            {request.draft && request.status === 'open' ? 'Draft' : stateLabelText(request.status)}
          </StateLabel>
          <span>
            <strong style={{ color: 'var(--fgColor-default)' }}>{actorName(author)}</strong>{' '}
            {request.status === 'merged' ? 'merged' : 'wants to merge'} {commits?.length ?? 0} commit
            {(commits?.length ?? 0) === 1 ? '' : 's'} into{' '}
            <Mono style={BRANCH_CHIP}>{request.target_branch}</Mono> from{' '}
            <Mono style={BRANCH_CHIP}>{request.source_branch}</Mono>
          </span>
        </div>
      )}
      <ErrorBanner
        error={
          actionError ??
          error ??
          diffError ??
          commitsError ??
          stateError ??
          reviewsError ??
          threadsError ??
          reviewRequestsError ??
          timelineError ??
          checksError ??
          membersError
        }
        onDismiss={actionError ? () => setActionError(null) : undefined}
      />
      {loading && <Loading />}

      <MergeRequestTabs
        tabs={[
          { id: 'overview', label: 'Conversation' },
          { id: 'commits', label: 'Commits', count: commits?.length },
          { id: 'files', label: 'File changes', count: diff?.files.length },
        ]}
        active={tab}
        onChange={selectTab}
      />

      {tab === 'overview' && request && (
        <MergeRequestOverview
          org={org}
          project={project}
          id={id}
          me={me?.id}
          request={request}
          state={state}
          reviews={reviews ?? []}
          threads={threads ?? []}
          checks={checks ?? []}
          reviewRequests={reviewRequests ?? []}
          timeline={timeline ?? []}
          members={members ?? []}
          files={files}
          canWrite={canWrite}
          busy={commentBusy}
          onChanged={reloadAll}
          onMerge={(strategy, deleteSource) =>
            run(() => mergeMergeRequest(org, project, id, strategy, deleteSource))
          }
          onClose={closeRequest}
          onReopen={() => run(() => reopenMergeRequest(org, project, id))}
          {...threadActions()}
        />
      )}

      {tab === 'commits' && (
        <Stack direction="vertical" gap="normal">
          {commitsLoading && <Loading />}
          {!commitsLoading && <MergeRequestCommits org={org} project={project} commits={commits ?? []} />}
        </Stack>
      )}

      {tab === 'files' && (
        <Stack direction="vertical" gap="normal">
          {diffLoading && <Loading />}
          <ChangesView
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

      {editOpen && request && (
        <EditMergeRequestDialog
          org={org}
          project={project}
          id={id}
          request={request}
          onClose={() => setEditOpen(false)}
          onSaved={reloadAll}
        />
      )}
    </RepoPageShell>
  )
}

function EditMergeRequestDialog({
  org,
  project,
  id,
  request,
  onClose,
  onSaved,
}: {
  org: string
  project: string
  id: string
  request: MergeRequestResponse
  onClose: () => void
  onSaved: () => void
}) {
  const [title, setTitle] = useState(request.title)
  const [description, setDescription] = useState(request.description)
  const [draft, setDraft] = useState(request.draft)

  return (
    <FormDialog
      title={`Edit merge request #${request.number}`}
      width="large"
      submitLabel="Save changes"
      submitDisabled={title.trim() === ''}
      onClose={onClose}
      onSubmit={async () => {
        await updateMergeRequest(org, project, id, title.trim(), description, draft)
        onSaved()
      }}
    >
      <FormControl required>
        <FormControl.Label>Title</FormControl.Label>
        <TextInput block autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
      </FormControl>
      <FormControl>
        <FormControl.Label>Description</FormControl.Label>
        <Textarea block rows={4} value={description} onChange={(event) => setDescription(event.target.value)} />
      </FormControl>
      <FormControl>
        <Checkbox checked={draft} onChange={(event) => setDraft(event.target.checked)} />
        <FormControl.Label>Draft (cannot be merged until marked ready)</FormControl.Label>
      </FormControl>
    </FormDialog>
  )
}

const BRANCH_CHIP = {
  color: 'var(--fgColor-accent)',
  background: 'var(--bgColor-accent-muted)',
  padding: '2px 6px',
  borderRadius: 6,
  fontSize: 12,
}

function stateLabelStatus(status: string): 'pullOpened' | 'pullClosed' | 'pullMerged' | 'draft' {
  if (status === 'merged') return 'pullMerged'
  if (status === 'closed') return 'pullClosed'
  return 'pullOpened'
}

function stateLabelText(status: string): string {
  if (status === 'merged') return 'Merged'
  if (status === 'closed') return 'Closed'
  return 'Open'
}
