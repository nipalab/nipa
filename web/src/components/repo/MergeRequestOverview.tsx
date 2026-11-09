import { lazy, Suspense, useState, type ReactNode } from 'react'
import { Button, Checkbox, IconButton, Label, Link as PrimerLink, Select, Text, Textarea } from '@primer/react'
import {
  CheckCircleFillIcon,
  CheckIcon,
  ClockIcon,
  CommentIcon,
  DotFillIcon,
  EyeIcon,
  FileDiffIcon,
  GitCommitIcon,
  GitMergeIcon,
  GitPullRequestClosedIcon,
  GitPullRequestDraftIcon,
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
  setMergeRequestAssignees,
  submitMergeRequestReview,
  withdrawMergeRequestReview,
} from '../../api/endpoints'
import type {
  DiffFileResponse,
  MergeRequestCheckResponse,
  MergeRequestResponse,
  OrgMemberResponse,
  ReviewRequestResponse,
  ReviewResponse,
  ReviewState,
  ReviewStateResponse,
  ThreadResponse,
  TimelineItemResponse,
} from '../../api/models'
import { Mono, ErrorBanner } from '../ui'
import { ActorAvatar, actorName, actorsFrom, type ActorLike } from '../ActorAvatar'
import { useConfirmAction } from '../confirm'
import { Timestamp } from '../Timestamp'
import { errorMessage } from '../../hooks'
import { ThreadCodeContext } from './DiffView'
import { ThreadCard } from './ThreadCard'
import { MemberSelect } from './MemberPicker'

const Markdown = lazy(() => import('./Markdown').then((module) => ({ default: module.Markdown })))

type ReviewerStatus = 'pending' | 'approved' | 'changes_requested' | 'commented' | 'dismissed'
type Tone = 'success' | 'danger' | 'attention' | 'done' | 'muted' | 'accent'

interface ReviewerEntry {
  user: ActorLike
  status: ReviewerStatus
  review?: ReviewResponse
  requestId?: string
}

const toneColor: Record<Tone, string> = {
  success: 'var(--fgColor-success)',
  danger: 'var(--fgColor-danger)',
  attention: 'var(--fgColor-attention)',
  done: 'var(--fgColor-done)',
  muted: 'var(--fgColor-muted)',
  accent: 'var(--fgColor-accent)',
}

const badgeFill: Partial<Record<Tone, string>> = {
  success: 'var(--bgColor-success-emphasis)',
  danger: 'var(--bgColor-danger-emphasis)',
  done: 'var(--bgColor-done-emphasis)',
}

export function MarkdownBody({ children }: { children: string }) {
  return (
    <Suspense fallback={<Text as="p" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{children}</Text>}>
      <Markdown>{children}</Markdown>
    </Suspense>
  )
}

// TimelineComment is a GitHub-style comment box: the avatar sits on the
// timeline rail and the box carries a header bar and the body.
function TimelineComment({
  actor,
  header,
  labels,
  actions,
  children,
  tone,
}: {
  actor?: ActorLike
  header: ReactNode
  labels?: ReactNode
  actions?: ReactNode
  children: ReactNode
  tone?: 'accent'
}) {
  return (
    <div className="nipa-tl-item nipa-tl-comment">
      <span className="nipa-tl-avatar">
        <ActorAvatar actor={actor} size={40} />
      </span>
      <div className={`nipa-tl-box${tone === 'accent' ? ' nipa-tl-box-accent' : ''}`}>
        <div className="nipa-tl-box-header">
          <span className="nipa-tl-box-title">{header}</span>
          {labels}
          {actions && <span className="nipa-tl-box-actions">{actions}</span>}
        </div>
        <div className="nipa-tl-box-body">{children}</div>
      </div>
    </div>
  )
}

// TimelineEvent is a one-line entry with an icon badge on the rail.
function TimelineEvent({ icon, tone = 'muted', children }: { icon: ReactNode; tone?: Tone; children: ReactNode }) {
  const fill = badgeFill[tone]
  return (
    <div className="nipa-tl-item nipa-tl-event">
      <span
        className="nipa-tl-badge"
        style={fill ? { background: fill, color: 'var(--fgColor-onEmphasis)' } : { color: toneColor[tone] }}
      >
        {icon}
      </span>
      <div className="nipa-tl-event-text">{children}</div>
    </div>
  )
}

function SideSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="nipa-mr-side-section" aria-label={title}>
      <h3>{title}</h3>
      <div className="nipa-mr-side-body">{children}</div>
    </section>
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
      return { icon: <DotFillIcon />, color: 'var(--fgColor-attention)', label: 'review pending' }
  }
}

function reviewVerb(review: ReviewResponse): string {
  if (review.dismissed_at) return 'had their review dismissed'
  if (review.state === 'approved') return review.stale ? 'approved these changes (outdated)' : 'approved these changes'
  if (review.state === 'changes_requested') return 'requested changes'
  return 'reviewed'
}

function reviewTone(review: ReviewResponse): Tone {
  if (review.dismissed_at || review.stale) return 'muted'
  if (review.state === 'approved') return 'success'
  if (review.state === 'changes_requested') return 'danger'
  return 'muted'
}

function reviewIcon(review: ReviewResponse) {
  if (review.state === 'approved') return <CheckIcon />
  if (review.state === 'changes_requested') return <FileDiffIcon />
  return <EyeIcon />
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
      return <GitPullRequestClosedIcon />
    case 'reopened':
      return <GitPullRequestIcon />
    case 'ready_for_review':
      return <EyeIcon />
    default:
      return <CommentIcon />
  }
}

function eventTone(kind: string): Tone {
  if (kind === 'merged') return 'done'
  if (kind === 'closed') return 'danger'
  if (kind === 'reopened') return 'success'
  return 'muted'
}

function eventLabel(item: TimelineItemResponse, org: string, project: string): ReactNode {
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
                <Mono>{item.commit_hash?.slice(0, 7) || item.commit_id.slice(0, 7)}</Mono>
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
          {actor} merged commit <Mono>{item.commit_hash?.slice(0, 7) || item.commit_id?.slice(0, 7) || ''}</Mono>
        </>
      )
    case 'closed':
      return <>{actor} closed this merge request</>
    case 'reopened':
      return <>{actor} reopened this merge request</>
    case 'ready_for_review':
      return <>{actor} marked this merge request as ready for review</>
    default:
      return (
        <>
          {actor} {item.kind.replace(/_/g, ' ')}
        </>
      )
  }
}

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
  draft: {
    title: 'This merge request is still a work in progress.',
    hint: 'Mark it ready for review before it can be merged.',
  },
  changes_requested: {
    title: 'Changes were requested on this merge request.',
    hint: 'Address the review feedback; a new review clears the block.',
  },
  insufficient_approvals: {
    title: 'This merge request does not have enough approvals yet.',
    hint: 'The target branch requires approvals before merging.',
  },
  required_reviewers: {
    title: 'A required reviewer has not approved this merge request.',
    hint: 'Every required reviewer must approve before merging.',
  },
  status_checks: {
    title: 'Required status checks have not passed.',
    hint: 'All required checks must succeed for the current source head.',
  },
}

function MergeSection({ icon, tone, title, hint, children }: { icon: ReactNode; tone: Tone; title: string; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="nipa-merge-section">
      <span className="nipa-merge-section-icon" style={{ color: toneColor[tone] }}>
        {icon}
      </span>
      <div style={{ minWidth: 0, flex: 1 }}>
        <Text as="p" style={{ margin: 0, fontWeight: 600 }}>
          {title}
        </Text>
        {hint && (
          <Text as="p" style={{ margin: 0, fontSize: 12, color: 'var(--fgColor-muted)' }}>
            {hint}
          </Text>
        )}
        {children}
      </div>
    </div>
  )
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
  checks,
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
  onDeleteThread,
  canAdmin,
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
  checks: MergeRequestCheckResponse[]
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
  onDeleteThread?: (threadId: string) => void
  canAdmin?: boolean
  onMerge: (strategy: string, deleteSource: boolean) => void
  onClose: () => void
  onReopen: () => void
}) {
  const [body, setBody] = useState('')
  const [reviewer, setReviewer] = useState('')
  const [assignee, setAssignee] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const [strategy, setStrategy] = useState('ff')
  const [deleteSource, setDeleteSource] = useState(false)
  const [reviewOpen, setReviewOpen] = useState(false)
  const [reviewChoice, setReviewChoice] = useState<ReviewState>('commented')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const confirm = useConfirmAction()

  const open = request?.status === 'open'
  const isAuthor = Boolean(me && request && request.created_by === me)
  const canDecide = canWrite && open && !isAuthor
  const pending = busy || sending
  const threadActors = (thread: ThreadResponse) => [thread.created_by, ...thread.comments.map((comment) => comment.user)]
  const participants = actorsFrom(
    threads.flatMap(threadActors),
    reviews.flatMap((review) => [review.reviewer, review.dismissed_by]),
    reviewRequests.flatMap((entry) => [entry.reviewer, entry.requested_by]),
    timeline.flatMap((item) => [item.actor, item.subject]),
  )
  const memberActor = (userId: string | undefined): ActorLike | undefined => {
    if (!userId) return undefined
    const member = members.find((entry) => entry.user_id === userId)
    return participants.get(userId) ?? (member ? { user_id: member.user_id, name: member.name } : { user_id: userId })
  }
  const author = memberActor(request?.created_by)
  const meActor = memberActor(me)

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

  const candidates = members.filter(
    (member) =>
      member.user_id !== request?.created_by &&
      !reviewRequests.some((entry) => entry.reviewer.user_id === member.user_id),
  )
  const assigneeCandidates = members.filter(
    (member) => !(request?.assignees ?? []).some((entry) => entry.user_id === member.user_id),
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

  async function confirmThen(options: Parameters<typeof confirm>[0], action: () => Promise<unknown>) {
    if (await confirm(options)) await run(action)
  }

  async function run(action: () => Promise<unknown>) {
    setError(null)
    setSending(true)
    try {
      await action()
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSending(false)
    }
  }

  async function submitReview() {
    await run(async () => {
      await submitMergeRequestReview(org, project, id, { state: reviewChoice, body: body.trim(), comments: [] })
      setBody('')
      setReviewOpen(false)
      setReviewChoice('commented')
    })
  }

  async function postComment() {
    await run(async () => {
      await addMergeRequestComment(org, project, id, { file_path: '', body: body.trim() })
      setBody('')
    })
  }

  const toggleExpanded = (threadId: string) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(threadId)) next.delete(threadId)
      else next.add(threadId)
      return next
    })

  const authorLabel = (userId: string | undefined) =>
    userId && userId === request?.created_by ? (
      <Label variant="secondary" size="small">
        Author
      </Label>
    ) : null

  function renderReview(review: ReviewResponse) {
    const dismissed = Boolean(review.dismissed_at)
    const actions = canWrite && !dismissed && (
      <>
        {me === review.reviewer.user_id ? (
          <Button
            size="small"
            variant="invisible"
            disabled={pending}
            onClick={() =>
              confirmThen(
                { title: 'Withdraw review?', content: 'Your review will be removed from this merge request.', confirmLabel: 'Withdraw' },
                () => withdrawMergeRequestReview(org, project, id, review.id),
              )
            }
          >
            Withdraw
          </Button>
        ) : (
          <Button
            size="small"
            variant="invisible"
            disabled={pending}
            onClick={() =>
              confirmThen(
                { title: 'Dismiss review?', content: 'The review stays in the history but no longer counts.', confirmLabel: 'Dismiss' },
                () => dismissMergeRequestReview(org, project, id, review.id),
              )
            }
          >
            Dismiss
          </Button>
        )}
      </>
    )
    return (
      <div key={`review-${review.id}`} className="nipa-tl-group" style={{ opacity: dismissed || review.stale ? 0.75 : 1 }}>
        <TimelineEvent icon={reviewIcon(review)} tone={reviewTone(review)}>
          <ActorAvatar actor={review.reviewer} size={20} /> <strong>{actorName(review.reviewer)}</strong> {reviewVerb(review)}{' '}
          <Timestamp value={review.created_at} mode="absolute" />
          {actions && <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 4 }}>{actions}</span>}
        </TimelineEvent>
        {(review.body || review.dismissed_at) && (
          <TimelineComment
            actor={review.reviewer}
            header={
              <>
                <strong>{actorName(review.reviewer)}</strong> left a comment
              </>
            }
            labels={authorLabel(review.reviewer.user_id)}
          >
            {review.body ? (
              <MarkdownBody>{review.body}</MarkdownBody>
            ) : (
              <Text as="p" style={{ color: 'var(--fgColor-muted)', margin: 0 }}>
                No review comment.
              </Text>
            )}
            {review.dismissed_at && (
              <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 12, margin: '8px 0 0' }}>
                {review.dismissed_reason === 'new_commits' ? 'Dismissed by new commits' : 'Dismissed'} by{' '}
                {actorName(review.dismissed_by)} · <Timestamp value={review.dismissed_at} mode="absolute" />
              </Text>
            )}
          </TimelineComment>
        )}
      </div>
    )
  }

  function renderThread(thread: ThreadResponse) {
    const inline = Boolean(thread.file_path)
    if (inline && thread.resolved && !expanded.has(thread.id)) {
      return (
        <TimelineEvent key={`thread-${thread.id}`} icon={<CheckCircleFillIcon />} tone="muted">
          <strong>{actorName(thread.resolved_by ?? thread.created_by)}</strong> resolved a conversation on{' '}
          <Mono>{thread.file_path}</Mono>
          <Button size="small" variant="invisible" style={{ marginLeft: 'auto' }} onClick={() => toggleExpanded(thread.id)}>
            Show resolved
          </Button>
        </TimelineEvent>
      )
    }
    return (
      <div key={`thread-${thread.id}`} className="nipa-tl-item nipa-tl-comment">
        <span className="nipa-tl-avatar">
          <ActorAvatar actor={thread.created_by} size={40} />
        </span>
        <div style={{ minWidth: 0 }}>
          <ThreadCard
            variant="conversation"
            thread={thread}
            me={me}
            canWrite={canWrite}
            busy={pending}
            authorId={request?.created_by}
            context={inline ? <Mono>{thread.file_path}:{thread.new_line ?? thread.old_line ?? '?'}</Mono> : undefined}
            headerAction={
              inline ? (
                <>
                  {thread.resolved && (
                    <Button size="small" variant="invisible" onClick={() => toggleExpanded(thread.id)}>
                      Hide resolved
                    </Button>
                  )}
                  <PrimerLink as={Link} to={`/${org}/${project}/merges/${id}?tab=files#file-${thread.file_path}`}>
                    View on file
                  </PrimerLink>
                </>
              ) : undefined
            }
            snippet={
              inline ? (
                <ThreadCodeContext
                  files={files}
                  filePath={thread.file_path}
                  side={thread.side}
                  oldLine={thread.old_line}
                  newLine={thread.new_line}
                />
              ) : undefined
            }
            onReply={onReplyThread}
            onResolve={onResolveThread}
            onEditComment={onEditComment}
            onDeleteComment={onDeleteComment}
            onDeleteThread={onDeleteThread}
            canAdmin={canAdmin}
          />
        </div>
      </div>
    )
  }

  const mergeability = request?.mergeability?.status
  const blockedBy = request?.mergeability?.blocked_by

  return (
    <div className="nipa-mr-layout">
      <div className="nipa-mr-main">
        <ErrorBanner error={error} onDismiss={() => setError(null)} />

        <div className="nipa-timeline" aria-label="Conversation">
          {request && (
            <TimelineComment
              actor={author}
              tone="accent"
              header={
                <>
                  <strong>{actorName(author)}</strong> opened this merge request{' '}
                  <Timestamp value={request.created_at} mode="absolute" />
                </>
              }
              labels={
                <Label variant="secondary" size="small">
                  Author
                </Label>
              }
            >
              {request.description ? (
                <MarkdownBody>{request.description}</MarkdownBody>
              ) : (
                <Text as="p" style={{ color: 'var(--fgColor-muted)', fontStyle: 'italic', margin: 0 }}>
                  No description provided.
                </Text>
              )}
            </TimelineComment>
          )}

          {entries.map((entry) => {
            if (entry.kind === 'event') {
              return (
                <TimelineEvent key={`event-${entry.item.id}`} icon={eventIcon(entry.item.kind)} tone={eventTone(entry.item.kind)}>
                  <ActorAvatar actor={entry.item.actor} size={20} /> {eventLabel(entry.item, org, project)}{' '}
                  <Timestamp value={entry.item.created_at} mode="absolute" />
                </TimelineEvent>
              )
            }
            if (entry.kind === 'review') return renderReview(entry.review)
            return renderThread(entry.thread)
          })}

          {request && mergeBox()}
        </div>

        {canWrite && request && (
          <div className="nipa-tl-item nipa-tl-comment nipa-composer">
            <span className="nipa-tl-avatar">
              <ActorAvatar actor={meActor} size={40} />
            </span>
            <div className="nipa-tl-box">
              <div className="nipa-tl-box-header">
                <span className="nipa-tl-box-title">
                  <strong>Add a comment</strong>
                </span>
              </div>
              <div className="nipa-tl-box-body">
                <Textarea
                  block
                  rows={5}
                  resize="vertical"
                  value={body}
                  placeholder="Leave a comment"
                  onChange={(event) => setBody(event.target.value)}
                  aria-label="Add a comment"
                />
                {reviewOpen && canDecide && (
                  <fieldset className="nipa-review-choices" aria-label="Review decision">
                    {(
                      [
                        ['commented', 'Comment', 'Submit general feedback without explicit approval.'],
                        ['approved', 'Approve', 'Submit feedback and approve merging these changes.'],
                        ['changes_requested', 'Request changes', 'Submit feedback that must be addressed before merging.'],
                      ] as const
                    ).map(([value, label, hint]) => (
                      <label key={value}>
                        <input
                          type="radio"
                          name="review-choice"
                          value={value}
                          checked={reviewChoice === value}
                          onChange={() => setReviewChoice(value)}
                        />
                        <span>
                          <strong>{label}</strong>
                          <span className="nipa-review-hint">{hint}</span>
                        </span>
                      </label>
                    ))}
                  </fieldset>
                )}
                <div className="nipa-composer-actions">
                  <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)', marginRight: 'auto' }}>Markdown is supported</Text>
                  {open && <Button onClick={onClose}>Close merge request</Button>}
                  {request.status === 'closed' && (
                    <Button disabled={pending} onClick={onReopen}>
                      Reopen merge request
                    </Button>
                  )}
                  {canDecide &&
                    (reviewOpen ? (
                      <>
                        <Button onClick={() => setReviewOpen(false)}>Cancel review</Button>
                        <Button
                          variant={reviewChoice === 'changes_requested' ? 'danger' : 'primary'}
                          disabled={pending || !body.trim()}
                          onClick={submitReview}
                        >
                          Submit review
                        </Button>
                      </>
                    ) : (
                      <Button onClick={() => setReviewOpen(true)}>Review changes</Button>
                    ))}
                  {!reviewOpen && (
                    <Button variant="primary" disabled={pending || !body.trim()} onClick={postComment}>
                      Comment
                    </Button>
                  )}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      <aside className="nipa-mr-sidebar">
        <SideSection title="Reviewers">
          {reviewers.length === 0 && (
            <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>No reviews</Text>
          )}
          {reviewers.map((entry) => {
            const status = reviewerStatus(entry.status)
            return (
              <div key={entry.user.user_id} className="nipa-mr-side-row">
                <ActorAvatar actor={entry.user} size={20} />
                <span className="nipa-mr-side-name">{actorName(entry.user)}</span>
                <span style={{ color: status.color, display: 'inline-flex' }} title={status.label}>
                  {status.icon}
                </span>
                {canWrite && entry.requestId && (
                  <IconButton
                    size="small"
                    variant="invisible"
                    aria-label={`cancel review request for ${actorName(entry.user)}`}
                    disabled={pending}
                    onClick={() => run(() => removeMergeRequestReviewRequest(org, project, id, entry.user.user_id))}
                    icon={XIcon}
                  />
                )}
              </div>
            )
          })}
          {canWrite && open && candidates.length > 0 && (
            <div className="nipa-mr-side-picker">
              <MemberSelect label="Add reviewer" placeholder="Add reviewer…" members={candidates} value={reviewer} onChange={setReviewer} />
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
            </div>
          )}
        </SideSection>

        <SideSection title="Assignees">
          {(request?.assignees ?? []).length === 0 && (
            <Text style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>No one assigned</Text>
          )}
          {(request?.assignees ?? []).map((entry) => (
            <div key={entry.user_id} className="nipa-mr-side-row">
              <ActorAvatar actor={entry} size={20} />
              <span className="nipa-mr-side-name">{actorName(entry)}</span>
              {canWrite && open && (
                <IconButton
                  size="small"
                  variant="invisible"
                  aria-label={`unassign ${actorName(entry)}`}
                  disabled={pending}
                  onClick={() =>
                    run(() =>
                      setMergeRequestAssignees(
                        org,
                        project,
                        id,
                        (request?.assignees ?? []).filter((other) => other.user_id !== entry.user_id).map((other) => other.user_id),
                      ),
                    )
                  }
                  icon={XIcon}
                />
              )}
            </div>
          ))}
          {canWrite && open && assigneeCandidates.length > 0 && (
            <div className="nipa-mr-side-picker">
              <MemberSelect label="Add assignee" placeholder="Add assignee…" members={assigneeCandidates} value={assignee} onChange={setAssignee} />
              <Button
                size="small"
                disabled={pending || !assignee}
                onClick={() =>
                  run(async () => {
                    await setMergeRequestAssignees(org, project, id, [
                      ...(request?.assignees ?? []).map((entry) => entry.user_id),
                      assignee,
                    ])
                    setAssignee('')
                  })
                }
              >
                Assign
              </Button>
            </div>
          )}
        </SideSection>

        {participants.size > 0 && (
          <SideSection title={`${participants.size} participant${participants.size === 1 ? '' : 's'}`}>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {[...participants.values()].map((participant) => (
                <span key={participant.user_id} title={actorName(participant)}>
                  <ActorAvatar actor={participant} size={26} />
                </span>
              ))}
            </div>
          </SideSection>
        )}
      </aside>
    </div>
  )

  function mergeBox() {
    if (!request) return null
    if (request.status === 'merged') {
      return (
        <div className="nipa-tl-item nipa-tl-merge">
          <span className="nipa-tl-merge-badge" style={{ background: 'var(--bgColor-done-emphasis)' }}>
            <GitMergeIcon size={24} />
          </span>
          <div className="nipa-tl-box nipa-merge-box" style={{ borderColor: 'var(--borderColor-done-emphasis)' }}>
            <MergeSection
              icon={<GitMergeIcon />}
              tone="done"
              title="Merge request successfully merged"
              hint={
                <>
                  Commit <Mono>{request.merge_commit_id?.slice(0, 10)}</Mono> has been merged into{' '}
                  <strong>{request.target_branch}</strong>.
                </>
              }
            />
          </div>
        </div>
      )
    }
    if (request.status === 'closed') {
      return (
        <div className="nipa-tl-item nipa-tl-merge">
          <span className="nipa-tl-merge-badge" style={{ background: 'var(--bgColor-danger-emphasis)' }}>
            <GitPullRequestClosedIcon size={24} />
          </span>
          <div className="nipa-tl-box nipa-merge-box">
            <MergeSection
              icon={<XCircleFillIcon />}
              tone="danger"
              title="Closed with unmerged commits"
              hint="This merge request was closed without merging."
            />
          </div>
        </div>
      )
    }

    const divergedMergeable = strategy !== 'ff' && mergeability === 'behind_target'
    const mergeable = (mergeability === 'mergeable' || divergedMergeable) && !blockedBy
    const status = divergedMergeable
      ? { title: 'The source branch is behind the target branch.', hint: 'Merging will combine both sides.' }
      : ((blockedBy ? BLOCKED_BOX_TEXT[blockedBy] : undefined) ??
        MERGE_BOX_TEXT[mergeability ?? ''] ?? {
          title: 'This merge request cannot be merged.',
          hint: 'Refresh the page for the current merge status.',
        })
    const approvals = state?.approvals ?? 0
    const changesRequested = state?.changes_requested ?? 0
    const failed = checks.filter((check) => check.state === 'failed').length
    const passed = checks.filter((check) => check.state === 'success').length
    const running = checks.length - failed - passed
    const badgeColor = mergeable ? 'var(--bgColor-success-emphasis)' : 'var(--bgColor-neutral-emphasis)'

    return (
      <div className="nipa-tl-item nipa-tl-merge">
        <span className="nipa-tl-merge-badge" style={{ background: badgeColor }}>
          {request.draft ? <GitPullRequestDraftIcon size={24} /> : <GitMergeIcon size={24} />}
        </span>
        <div className="nipa-tl-box nipa-merge-box" aria-label="Merge status">
          {changesRequested > 0 ? (
            <MergeSection
              icon={<XCircleFillIcon />}
              tone="danger"
              title="Changes requested"
              hint={`${changesRequested} review${changesRequested === 1 ? '' : 's'} requesting changes`}
            />
          ) : approvals > 0 ? (
            <MergeSection
              icon={<CheckCircleFillIcon />}
              tone="success"
              title="Changes approved"
              hint={`${approvals} approved${approvals === 1 ? ' review' : ' reviews'}`}
            />
          ) : (
            <MergeSection
              icon={<ClockIcon />}
              tone={blockedBy === 'insufficient_approvals' || blockedBy === 'required_reviewers' ? 'attention' : 'muted'}
              title={blockedBy === 'insufficient_approvals' || blockedBy === 'required_reviewers' ? 'Review required' : 'No reviews yet'}
              hint={
                blockedBy === 'insufficient_approvals' || blockedBy === 'required_reviewers'
                  ? 'The target branch requires an approving review before merging.'
                  : 'Request a review from the sidebar to get feedback.'
              }
            />
          )}

          {checks.length > 0 && (
            <MergeSection
              icon={failed > 0 ? <XCircleFillIcon /> : running > 0 ? <DotFillIcon /> : <CheckCircleFillIcon />}
              tone={failed > 0 ? 'danger' : running > 0 ? 'attention' : 'success'}
              title={failed > 0 ? 'Some checks were not successful' : running > 0 ? "Some checks haven't completed yet" : 'All checks have passed'}
              hint={[
                passed > 0 && `${passed} successful`,
                failed > 0 && `${failed} failing`,
                running > 0 && `${running} pending`,
              ]
                .filter(Boolean)
                .join(', ')}
            >
              <ul className="nipa-merge-checks">
                {checks.map((check) => (
                  <li key={check.id}>
                    <span
                      style={{
                        color: toneColor[check.state === 'success' ? 'success' : check.state === 'failed' ? 'danger' : 'attention'],
                        display: 'inline-flex',
                      }}
                    >
                      {check.state === 'success' ? <CheckIcon /> : check.state === 'failed' ? <XIcon /> : <DotFillIcon />}
                    </span>
                    <strong>{check.name}</strong>
                    <span style={{ color: 'var(--fgColor-muted)', textTransform: 'capitalize' }}>{check.state}</span>
                    {check.details_url && (
                      <PrimerLink href={check.details_url} target="_blank" rel="noreferrer" style={{ marginLeft: 'auto' }}>
                        Details
                      </PrimerLink>
                    )}
                  </li>
                ))}
              </ul>
            </MergeSection>
          )}

          <MergeSection
            icon={mergeable ? <CheckCircleFillIcon /> : request.draft ? <GitPullRequestDraftIcon /> : <XCircleFillIcon />}
            tone={mergeable ? 'success' : request.draft ? 'muted' : 'danger'}
            title={status.title}
            hint={status.hint}
          />

          {canWrite && (
            <div className="nipa-merge-actions">
              <Button variant="primary" disabled={!mergeable || pending} onClick={() => onMerge(strategy, deleteSource)}>
                Merge request
              </Button>
              <Select aria-label="Merge strategy" value={strategy} onChange={(event) => setStrategy(event.target.value)}>
                <Select.Option value="ff">Fast-forward</Select.Option>
                <Select.Option value="merge">Create a merge commit</Select.Option>
                <Select.Option value="squash">Squash and merge</Select.Option>
                <Select.Option value="rebase">Rebase and merge</Select.Option>
              </Select>
              <label className="nipa-merge-delete">
                <Checkbox
                  aria-label="Delete source branch"
                  checked={deleteSource}
                  onChange={(event) => setDeleteSource(event.target.checked)}
                />
                Delete <Mono>{request.source_branch}</Mono> after merging
              </label>
            </div>
          )}
        </div>
      </div>
    )
  }
}
