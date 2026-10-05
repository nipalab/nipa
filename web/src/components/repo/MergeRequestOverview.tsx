import { lazy, Suspense, useState } from 'react'
import { Button, Link as PrimerLink, Select, Stack, Text, Textarea } from '@primer/react'
import {
  CheckCircleFillIcon,
  CheckIcon,
  ClockIcon,
  CommentIcon,
  EyeIcon,
  GitCommitIcon,
  GitMergeIcon,
  GitPullRequestIcon,
  XCircleFillIcon,
  XCircleIcon,
  XIcon,
} from '@primer/octicons-react'
import { Link } from 'react-router-dom'
import {
  addMergeRequestComment,
  dismissMergeRequestReview,
  removeMergeRequestReviewRequest,
  requestMergeRequestReview,
  submitMergeRequestReview,
  withdrawMergeRequestReview,
} from '../../api/endpoints'
import type {
  DiffFileResponse,
  MergeRequestResponse,
  OrgMemberResponse,
  ReviewRequestResponse,
  ReviewResponse,
  ReviewState,
  ReviewStateResponse,
  ThreadResponse,
  TimelineItemResponse,
} from '../../api/models'
import { Mono, PRIMARY_BUTTON_STYLE } from '../ui'
import { ActorAvatar, actorName, actorsFrom, type ActorLike } from './ActorAvatar'
import { ThreadCodeContext } from './DiffView'
import { ThreadCard } from './ThreadCard'

const Markdown = lazy(() => import('./Markdown').then((module) => ({ default: module.Markdown })))

type ReviewerStatus = 'pending' | 'approved' | 'changes_requested' | 'commented' | 'dismissed'

interface ReviewerEntry {
  user: ActorLike
  status: ReviewerStatus
  review?: ReviewResponse
  requestId?: string
}

function timestamp(value: string | undefined): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}

function sidebarBox(title: string, children: React.ReactNode, action?: React.ReactNode) {
  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6 }}>
      <div
        style={{
          padding: '8px 12px',
          borderBottom: '1px solid var(--borderColor-muted)',
          background: 'var(--bgColor-muted)',
          fontSize: 12,
          fontWeight: 600,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 8,
        }}
      >
        <span>{title}</span>
        {action}
      </div>
      <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>{children}</div>
    </div>
  )
}

function reviewerStatus(status: ReviewerStatus) {
  switch (status) {
    case 'approved':
      return { icon: <CheckIcon />, color: 'var(--fgColor-success)', label: 'approved these changes' }
    case 'changes_requested':
      return { icon: <XIcon />, color: 'var(--fgColor-danger)', label: 'requested changes' }
    case 'commented':
      return { icon: <CommentIcon />, color: 'var(--fgColor-muted)', label: 'reviewed' }
    case 'dismissed':
      return { icon: <EyeIcon />, color: 'var(--fgColor-muted)', label: 'review dismissed' }
    default:
      return { icon: <ClockIcon />, color: 'var(--fgColor-attention)', label: 'review pending' }
  }
}

function reviewVerb(review: ReviewResponse): string {
  if (review.dismissed_at) return 'had their review dismissed'
  if (review.state === 'approved') return review.stale ? 'approved these changes (outdated)' : 'approved these changes'
  if (review.state === 'changes_requested') return 'requested changes'
  return 'reviewed'
}

function eventIcon(kind: string) {
  switch (kind) {
    case 'pushed':
      return <GitCommitIcon />
    case 'review_requested':
    case 'review_request_removed':
      return <EyeIcon />
    case 'review_dismissed':
      return <XCircleIcon />
    case 'merged':
      return <GitMergeIcon />
    case 'closed':
      return <XCircleIcon />
    case 'reopened':
      return <GitPullRequestIcon />
    default:
      return <CommentIcon />
  }
}

function eventLabel(
  item: TimelineItemResponse,
  org: string,
  project: string,
): React.ReactNode {
  const actor = <strong>{actorName(item.actor)}</strong>
  switch (item.kind) {
    case 'pushed':
      return (
        <>
          {actor} pushed new commits
          {item.commit_id && (
            <>
              {' '}
              <Link to={`/${org}/${project}/commits/${item.commit_id}`}>
                <Mono>{item.commit_hash?.slice(0, 10) || item.commit_id.slice(0, 10)}</Mono>
              </Link>
            </>
          )}
        </>
      )
    case 'review_requested':
      return (
        <>
          {actor} requested a review from <strong>{actorName(item.subject)}</strong>
        </>
      )
    case 'review_request_removed':
      return (
        <>
          {actor} removed the review request for <strong>{actorName(item.subject)}</strong>
        </>
      )
    case 'review_dismissed':
      return <>{actor} dismissed a review</>
    case 'merged':
      return (
        <>
          {actor} merged commit{' '}
          <Mono>{item.commit_hash?.slice(0, 10) || item.commit_id?.slice(0, 10) || ''}</Mono>
        </>
      )
    case 'closed':
      return <>{actor} closed this merge request</>
    case 'reopened':
      return <>{actor} reopened this merge request</>
    default:
      return (
        <>
          {actor} {item.kind.replace(/_/g, ' ')}
        </>
      )
  }
}

export function MergeRequestOverview({
  org,
  project,
  id,
  me,
  request,
  state,
  reviews,
  threads,
  reviewRequests,
  timeline,
  members,
  files,
  canWrite,
  busy,
  onChanged,
  onReplyThread,
  onResolveThread,
  onEditComment,
  onDeleteComment,
  onMerge,
  onClose,
  onReopen,
}: {
  org: string
  project: string
  id: string
  me: string | undefined
  request: MergeRequestResponse | undefined
  state: ReviewStateResponse | null
  reviews: ReviewResponse[]
  threads: ThreadResponse[]
  reviewRequests: ReviewRequestResponse[]
  timeline: TimelineItemResponse[]
  members: OrgMemberResponse[]
  files: DiffFileResponse[]
  canWrite: boolean
  busy: boolean
  onChanged: () => void
  onReplyThread: (threadId: string, body: string) => void
  onResolveThread: (threadId: string, resolved: boolean) => void
  onEditComment: (threadId: string, commentId: string, body: string) => void
  onDeleteComment: (threadId: string, commentId: string) => void
  onMerge: () => void
  onClose: () => void
  onReopen: () => void
}) {
  const [body, setBody] = useState('')
  const [reviewer, setReviewer] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)

  const isAuthor = Boolean(me && request && request.created_by === me)
  const canDecide = canWrite && request?.status === 'open' && !isAuthor
  const pending = busy || sending
  const threadActors = (thread: ThreadResponse) => [thread.created_by, ...thread.comments.map((comment) => comment.user)]
  const author: ActorLike | undefined =
    actorsFrom(
      threads.flatMap(threadActors),
      reviews.flatMap((review) => [review.reviewer, review.dismissed_by]),
      reviewRequests.flatMap((entry) => [entry.reviewer, entry.requested_by]),
      timeline.flatMap((item) => [item.actor, item.subject]),
    ).get(request?.created_by ?? '') ??
    members.find((member) => member.user_id === request?.created_by)

  const reviewers: ReviewerEntry[] = []
  const reviewerIndex = new Map<string, ReviewerEntry>()
  for (const review of reviews) {
    const current = reviewerIndex.get(review.reviewer.user_id)
    if (current?.review && new Date(current.review.created_at) > new Date(review.created_at)) continue
    const entry: ReviewerEntry = {
      user: review.reviewer,
      status: review.dismissed_at ? 'dismissed' : (review.state as ReviewerStatus),
      review,
    }
    reviewerIndex.set(review.reviewer.user_id, entry)
    if (current) {
      reviewers.splice(reviewers.indexOf(current), 1, entry)
    } else {
      reviewers.push(entry)
    }
  }
  for (const requested of reviewRequests) {
    if (reviewerIndex.has(requested.reviewer.user_id)) continue
    const entry: ReviewerEntry = { user: requested.reviewer, status: 'pending', requestId: requested.id }
    reviewerIndex.set(requested.reviewer.user_id, entry)
    reviewers.push(entry)
  }

  const participants = actorsFrom(
    threads.flatMap(threadActors),
    reviews.flatMap((review) => [review.reviewer, review.dismissed_by]),
    reviewRequests.flatMap((entry) => [entry.reviewer, entry.requested_by]),
    timeline.flatMap((item) => [item.actor, item.subject]),
  )

  const candidates = members.filter(
    (member) =>
      member.user_id !== request?.created_by &&
      !reviewRequests.some((entry) => entry.reviewer.user_id === member.user_id),
  )

  const entries: (
    | { kind: 'event'; at: string; item: TimelineItemResponse }
    | { kind: 'review'; at: string; review: ReviewResponse }
    | { kind: 'thread'; at: string; thread: ThreadResponse }
  )[] = [
    ...threads.map((thread) => ({ kind: 'thread' as const, at: thread.created_at, thread })),
    ...reviews.map((review) => ({ kind: 'review' as const, at: review.created_at, review })),
    ...timeline
      .filter((item) => item.kind !== 'review_submitted' && item.kind !== 'opened')
      .map((item) => ({ kind: 'event' as const, at: item.created_at, item })),
  ].sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime())

  async function run(action: () => Promise<unknown>) {
    setError(null)
    setSending(true)
    try {
      await action()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSending(false)
    }
  }

  async function submitDecision(reviewState: ReviewState) {
    await run(async () => {
      await submitMergeRequestReview(org, project, id, { state: reviewState, body: body.trim(), comments: [] })
      setBody('')
    })
  }

  async function postComment() {
    await run(async () => {
      await addMergeRequestComment(org, project, id, { file_path: '', body: body.trim() })
      setBody('')
    })
  }

  const mergeability = request?.mergeability?.status
  const blockedBy = request?.mergeability?.blocked_by

  return (
    <div className="nipa-mr-layout">
      <div className="nipa-mr-main">
        {error && (
          <Text as="p" style={{ color: 'var(--fgColor-danger)' }}>
            {error}
          </Text>
        )}

        {request && (
          <div
            style={{
              border: '1px solid var(--borderColor-default)',
              borderRadius: 6,
              overflow: 'hidden',
            }}
          >
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                padding: '8px 12px',
                background: 'var(--bgColor-muted)',
                borderBottom: '1px solid var(--borderColor-muted)',
              }}
            >
              <ActorAvatar actor={author} size={20} />
              <strong>{actorName(author)}</strong>
              <span style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
                opened this merge request on {timestamp(request.created_at)}
              </span>
            </div>
            <div style={{ padding: '10px 12px' }}>
              {request.description ? (
                <Suspense
                  fallback={
                    <Text as="p" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
                      {request.description}
                    </Text>
                  }
                >
                  <Markdown>{request.description}</Markdown>
                </Suspense>
              ) : (
                <Text as="p" style={{ color: 'var(--fgColor-muted)', fontStyle: 'italic', margin: 0 }}>
                  No description provided.
                </Text>
              )}
            </div>
          </div>
        )}

        {entries.map((entry) => {
          if (entry.kind === 'event') {
            return (
              <div
                key={`event-${entry.item.id}`}
                style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--fgColor-muted)', fontSize: 13 }}
              >
                <span
                  style={{
                    color:
                      entry.item.kind === 'merged'
                        ? 'var(--fgColor-done)'
                        : entry.item.kind === 'closed'
                          ? 'var(--fgColor-danger)'
                          : 'var(--fgColor-muted)',
                    display: 'inline-flex',
                  }}
                >
                  {eventIcon(entry.item.kind)}
                </span>
                <span>{eventLabel(entry.item, org, project)}</span>
                <span style={{ fontSize: 12 }}>{timestamp(entry.item.created_at)}</span>
              </div>
            )
          }
          if (entry.kind === 'review') {
            const review = entry.review
            const dismissed = Boolean(review.dismissed_at) || review.stale
            return (
              <div
                key={`review-${review.id}`}
                style={{
                  border: '1px solid var(--borderColor-default)',
                  borderRadius: 6,
                  opacity: dismissed ? 0.7 : 1,
                  overflow: 'hidden',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 8,
                    padding: '8px 12px',
                    background: 'var(--bgColor-muted)',
                    borderBottom: '1px solid var(--borderColor-muted)',
                  }}
                >
                  <ActorAvatar actor={review.reviewer} size={20} />
                  <strong>{actorName(review.reviewer)}</strong>
                  <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>{reviewVerb(review)}</Text>
                  <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>{timestamp(review.created_at)}</Text>
                </div>
                <div style={{ padding: '10px 12px' }}>
                  {review.body ? (
                    <Text as="p" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
                      {review.body}
                    </Text>
                  ) : (
                    <Text as="p" style={{ color: 'var(--fgColor-muted)', margin: 0 }}>
                      No review comment.
                    </Text>
                  )}
                  {review.dismissed_at && (
                    <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 12, marginBottom: 0 }}>
                      {review.dismissed_reason === 'new_commits' ? 'Dismissed by new commits' : 'Dismissed'} by{' '}
                      {actorName(review.dismissed_by)} · {timestamp(review.dismissed_at)}
                    </Text>
                  )}
                  {canWrite && !review.dismissed_at && (
                    <Stack direction="horizontal" gap="condensed" style={{ marginTop: 4 }}>
                      {me === review.reviewer.user_id && (
                        <Button
                          size="small"
                          disabled={pending}
                          onClick={() => run(() => withdrawMergeRequestReview(org, project, id, review.id))}
                        >
                          Withdraw
                        </Button>
                      )}
                      {me !== review.reviewer.user_id && (
                        <Button
                          size="small"
                          disabled={pending}
                          onClick={() => run(() => dismissMergeRequestReview(org, project, id, review.id))}
                        >
                          Dismiss
                        </Button>
                      )}
                    </Stack>
                  )}
                </div>
              </div>
            )
          }
          const inline = Boolean(entry.thread.file_path)
          return (
            <ThreadCard
              key={`thread-${entry.thread.id}`}
              variant="conversation"
              thread={entry.thread}
              me={me}
              canWrite={canWrite}
              busy={pending}
              context={
                inline ? (
                  <Mono>
                    {entry.thread.file_path}:{entry.thread.new_line ?? entry.thread.old_line ?? '?'}
                  </Mono>
                ) : undefined
              }
              headerAction={
                inline ? (
                  <PrimerLink
                    as={Link}
                    to={`/${org}/${project}/merges/${id}?tab=files#file-${entry.thread.file_path}`}
                  >
                    View on file
                  </PrimerLink>
                ) : undefined
              }
              snippet={
                inline ? (
                  <ThreadCodeContext
                    files={files}
                    filePath={entry.thread.file_path}
                    side={entry.thread.side}
                    oldLine={entry.thread.old_line}
                    newLine={entry.thread.new_line}
                  />
                ) : undefined
              }
              onReply={onReplyThread}
              onResolve={onResolveThread}
              onEditComment={onEditComment}
              onDeleteComment={onDeleteComment}
            />
          )
        })}

        {canWrite && (
          <div>
            <Textarea
              block
              value={body}
              placeholder="Add a comment"
              onChange={(event) => setBody(event.target.value)}
              aria-label="Add a comment"
            />
            <div
              style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 8, marginTop: 8 }}
            >
              {canDecide ? (
                <>
                  <Button
                    disabled={pending || !body.trim()}
                    onClick={() => submitDecision('commented')}
                  >
                    Comment
                  </Button>
                  <Button
                    disabled={pending || !body.trim()}
                    style={PRIMARY_BUTTON_STYLE}
                    variant="primary"
                    onClick={() => submitDecision('approved')}
                  >
                    Approve
                  </Button>
                  <Button
                    variant="danger"
                    disabled={pending || !body.trim()}
                    onClick={() => submitDecision('changes_requested')}
                  >
                    Request changes
                  </Button>
                </>
              ) : (
                <>
                  {isAuthor && request?.status === 'open' && (
                    <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
                      You cannot review your own merge request.
                    </Text>
                  )}
                  <Button
                    variant="primary"
                    disabled={pending || !body.trim()}
                    onClick={postComment}
                  >
                    Comment
                  </Button>
                </>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="nipa-mr-sidebar">
        {sidebarBox(
          'Reviewers',
          <>
            {state && (state.approvals > 0 || state.changes_requested > 0) && (
              <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>
                {state.approvals} approved · {state.changes_requested} changes requested
              </Text>
            )}
            {reviewers.length === 0 && (
              <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>Still waiting for reviewers.</Text>
            )}
            {reviewers.map((entry) => {
              const status = reviewerStatus(entry.status)
              return (
                <div
                  key={entry.user.user_id}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}
                >
                  <span style={{ color: status.color, display: 'inline-flex' }} title={status.label}>
                    {status.icon}
                  </span>
                  <ActorAvatar actor={entry.user} size={20} />
                  <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {actorName(entry.user)}
                  </span>
                  {canWrite && entry.requestId && (
                    <Button
                      size="small"
                      aria-label={`cancel review request for ${actorName(entry.user)}`}
                      disabled={pending}
                      onClick={() =>
                        run(() =>
                          removeMergeRequestReviewRequest(org, project, id, entry.user.user_id),
                        )
                      }
                    >
                      <XIcon />
                    </Button>
                  )}
                </div>
              )
            })}
            {canWrite && request?.status === 'open' && candidates.length > 0 && (
              <Stack direction="vertical" gap="condensed">
                <Select
                  aria-label="Add reviewer"
                  value={reviewer}
                  onChange={(event) => setReviewer(event.target.value)}
                >
                  <option value="">Add reviewer…</option>
                  {candidates.map((member) => (
                    <option key={member.user_id} value={member.user_id}>
                      {member.name || member.email}
                    </option>
                  ))}
                </Select>
                <Button
                  size="small"
                  disabled={pending || !reviewer}
                  onClick={() =>
                    run(async () => {
                      await requestMergeRequestReview(org, project, id, reviewer)
                      setReviewer('')
                    })
                  }
                >
                  Request review
                </Button>
              </Stack>
            )}
          </>,
        )}

        {participants.size > 0 &&
          sidebarBox(
            `Participants (${participants.size})`,
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {[...participants.values()].map((participant) => (
                <span key={participant.user_id} title={actorName(participant)}>
                  <ActorAvatar actor={participant} size={24} />
                </span>
              ))}
            </div>,
          )}

        {mergeBox()}
      </div>
    </div>
  )

  function mergeBox() {
    if (!request) return null
    if (request.status === 'merged') {
      return (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <div style={{ padding: 12, background: 'var(--bgColor-done-muted)' }}>
            <Stack direction="horizontal" gap="condensed" align="center">
              <span style={{ color: 'var(--fgColor-done)', display: 'inline-flex' }}>
                <GitMergeIcon />
              </span>
              <Text style={{ fontWeight: 600, color: 'var(--fgColor-done)' }}>Merged</Text>
            </Stack>
            <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)', marginBottom: 0, marginTop: 4 }}>
              Commit <Mono>{request.merge_commit_id?.slice(0, 10)}</Mono> has been merged into{' '}
              <strong>{request.target_branch}</strong>.
            </Text>
          </div>
        </div>
      )
    }
    if (request.status === 'closed') {
      return (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <div style={{ padding: 12, background: 'var(--bgColor-danger-muted)' }}>
            <Stack direction="horizontal" gap="condensed" align="center">
              <span style={{ color: 'var(--fgColor-danger)', display: 'inline-flex' }}>
                <XCircleFillIcon />
              </span>
              <Text style={{ fontWeight: 600, color: 'var(--fgColor-danger)' }}>Closed</Text>
            </Stack>
            <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)', marginBottom: 0, marginTop: 4 }}>
              This merge request was closed without merging.
            </Text>
          </div>
          {canWrite && (
            <div style={{ padding: 12, borderTop: '1px solid var(--borderColor-muted)' }}>
              <Button block disabled={pending} onClick={onReopen}>
                Reopen merge request
              </Button>
            </div>
          )}
        </div>
      )
    }
    const mergeable = mergeability === 'mergeable' && !blockedBy
    const MERGE_BOX_TEXT: Record<string, { title: string; hint: string }> = {
      mergeable: {
        title: 'This branch has no conflicts with the base branch.',
        hint: 'Merging can be performed automatically.',
      },
      behind_target: {
        title: 'The source branch is behind the target branch.',
        hint: 'Update the source branch to include the target commits.',
      },
      up_to_date: {
        title: 'The source branch is already up to date with the target branch.',
        hint: 'There is nothing left to merge.',
      },
      invalid: {
        title: 'The source or target branch no longer exists.',
        hint: 'Recreate the missing branch or close this merge request.',
      },
    }
    const BLOCKED_BOX_TEXT: Record<string, { title: string; hint: string }> = {
      changes_requested: {
        title: 'Changes were requested on this merge request.',
        hint: 'Address the review feedback; a new review clears the block.',
      },
      insufficient_approvals: {
        title: 'This merge request does not have enough approvals yet.',
        hint: 'The target branch requires approvals before merging.',
      },
    }
    const mergeBoxText =
      (blockedBy ? BLOCKED_BOX_TEXT[blockedBy] : undefined) ??
      MERGE_BOX_TEXT[mergeability ?? ''] ?? {
        title: 'This merge request cannot be merged.',
        hint: 'Refresh the page for the current merge status.',
      }
    return (
      <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
        <div style={{ padding: 12, display: 'flex', gap: 8 }}>
          <span style={{ display: 'inline-flex' }}>
            {mergeable ? (
              <span style={{ color: 'var(--fgColor-success)', display: 'inline-flex' }}>
                <CheckCircleFillIcon />
              </span>
            ) : (
              <span style={{ color: 'var(--fgColor-danger)', display: 'inline-flex' }}>
                <XCircleFillIcon />
              </span>
            )}
          </span>
          <div style={{ fontSize: 13 }}>
            <Text as="p" style={{ margin: 0, fontWeight: 600 }}>
              {mergeBoxText.title}
            </Text>
            <Text as="p" style={{ margin: 0, fontSize: 12, color: 'var(--fgColor-muted)' }}>
              {mergeBoxText.hint}
            </Text>
          </div>
        </div>
        {canWrite && (
          <div style={{ padding: 12, borderTop: '1px solid var(--borderColor-muted)' }}>
            <Stack direction="vertical" gap="condensed">
              <Button
                block
                variant="primary"
                style={PRIMARY_BUTTON_STYLE}
                disabled={!mergeable || pending}
                onClick={onMerge}
              >
                Merge pull request
              </Button>
              <Button block disabled={pending} onClick={onClose}>
                Close merge request
              </Button>
            </Stack>
          </div>
        )}
      </div>
    )
  }
}
