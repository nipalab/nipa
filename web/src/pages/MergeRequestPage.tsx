import { useState } from 'react'
import { Button, Dialog, FormControl, Heading, Stack, StateLabel, TextInput } from '@primer/react'
import { useParams, useSearchParams } from 'react-router-dom'
import {
  addMergeRequestComment,
  deleteMergeRequestComment,
  getMergeRequest,
  getMergeRequestDiff,
  getMergeRequestReviewState,
  getMergeRequestTimeline,
  listMergeRequestCommits,
  listMergeRequestReviewRequests,
  listMergeRequestReviews,
  listMergeRequestThreads,
  listOrgMembers,
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
import { ActorAvatar, actorName, actorsFrom, type ActorLike } from '../components/repo/ActorAvatar'
import { ChangesView } from '../components/repo/ChangesView'
import { MergeRequestCommits } from '../components/repo/MergeRequestCommits'
import { MergeRequestOverview } from '../components/repo/MergeRequestOverview'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { Tabs } from '../components/repo/Tabs'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading, Mono } from '../components/ui'
import { useAsync } from '../hooks'

export default function MergeRequestPage() {
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
  const { data: members } = useAsync(() => listOrgMembers(org), [org])
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

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="merges"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Heading as="h2" style={{ margin: 0 }}>
          #{request?.number} {request?.title}
        </Heading>
        {request && <StateLabel status={stateLabelStatus(request.status)}>{stateLabelText(request.status)}</StateLabel>}
        {canEdit && (
          <Button size="small" style={{ marginLeft: 'auto' }} onClick={() => setEditOpen(true)}>
            Edit
          </Button>
        )}
      </div>
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
          <ActorAvatar actor={author} size={20} />
          <span>
            <strong style={{ color: 'var(--fgColor-default)' }}>{actorName(author)}</strong> wants to merge{' '}
            {commits?.length ?? 0} commit{(commits?.length ?? 0) === 1 ? '' : 's'} into{' '}
            <Mono style={{ color: 'var(--fgColor-accent)' }}>{request.target_branch}</Mono> from{' '}
            <Mono style={{ color: 'var(--fgColor-accent)' }}>{request.source_branch}</Mono>
          </span>
        </div>
      )}
      <ErrorBanner error={actionError ?? error ?? diffError ?? commitsError} />
      {loading && <Loading />}

      <Tabs
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
          reviewRequests={reviewRequests ?? []}
          timeline={timeline ?? []}
          members={members ?? []}
          files={files}
          canWrite={canWrite}
          busy={commentBusy}
          onChanged={reloadAll}
          onMerge={() => run(() => mergeMergeRequest(org, project, id))}
          onClose={() => run(() => closeMergeRequest(org, project, id))}
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
          onSaved={() => {
            setEditOpen(false)
            reloadAll()
          }}
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
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const canSubmit = title.trim() !== '' && !saving

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setError(null)
    try {
      await updateMergeRequest(org, project, id, title.trim(), description)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setSaving(false)
    }
  }

  return (
    <Dialog title={`Edit merge request #${request.number}`} onClose={onClose} width="large">
      <Dialog.Body>
        <ErrorBanner error={error} />
        <form onSubmit={handleSubmit}>
          <Stack direction="vertical" gap="normal">
            <FormControl required>
              <FormControl.Label>Title</FormControl.Label>
              <TextInput block autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
            </FormControl>
            <FormControl>
              <FormControl.Label>Description</FormControl.Label>
              <TextInput block value={description} onChange={(event) => setDescription(event.target.value)} />
            </FormControl>
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!canSubmit} onClick={handleSubmit}>
          {saving ? 'Saving…' : 'Save changes'}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}

function stateLabelStatus(status: string): 'pullOpened' | 'pullClosed' | 'pullMerged' {
  if (status === 'merged') return 'pullMerged'
  if (status === 'closed') return 'pullClosed'
  return 'pullOpened'
}

function stateLabelText(status: string): string {
  if (status === 'merged') return 'Merged'
  if (status === 'closed') return 'Closed'
  return 'Open'
}
