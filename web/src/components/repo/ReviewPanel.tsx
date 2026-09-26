import { useState } from 'react'
import { Button, FormControl, Select, Stack, Text, Textarea } from '@primer/react'
import {
  addMergeRequestComment,
  dismissMergeRequestReview,
  listOrgMembers,
  removeMergeRequestReviewRequest,
  requestMergeRequestReview,
  submitMergeRequestReview,
  withdrawMergeRequestReview,
} from '../../api/endpoints'
import type {
  MergeRequestResponse,
  ReviewActorResponse,
  ReviewRequestResponse,
  ReviewResponse,
  ReviewState,
  ReviewStateResponse,
  ThreadResponse,
  TimelineItemResponse,
} from '../../api/models'
import { useAsync } from '../../hooks'
import { EmptyState, ErrorBanner, Mono } from '../ui'
import { ThreadCard } from './ThreadCard'

const STATE_LABELS: Record<ReviewState, string> = {
  approved: 'Approve',
  changes_requested: 'Request changes',
  commented: 'Comment',
}

function actorName(actor: ReviewActorResponse | undefined): string {
  if (!actor) return 'unknown'
  return actor.name || actor.user_id
}

function ReviewBadge({ review }: { review: ReviewResponse }) {
  const color =
    review.state === 'approved'
      ? 'var(--fgColor-success)'
      : review.state === 'changes_requested'
        ? 'var(--fgColor-danger)'
        : 'var(--fgColor-muted)'
  return (
    <span style={{ color, fontWeight: 600 }}>
      {STATE_LABELS[review.state]}
      {review.stale && ' (outdated)'}
    </span>
  )
}

export function ReviewPanel({
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
  canWrite,
  busy,
  onChanged,
  onReplyThread,
  onResolveThread,
  onEditComment,
  onDeleteComment,
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
  canWrite: boolean
  busy: boolean
  onChanged: () => void
  onReplyThread: (threadId: string, body: string) => void
  onResolveThread: (threadId: string, resolved: boolean) => void
  onEditComment: (threadId: string, commentId: string, body: string) => void
  onDeleteComment: (threadId: string, commentId: string) => void
}) {
  const { data: members } = useAsync(() => listOrgMembers(org), [org])
  const [reviewState, setReviewState] = useState<ReviewState>('commented')
  const [body, setBody] = useState('')
  const [reviewer, setReviewer] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)

  const isAuthor = Boolean(me && request && request.created_by === me)
  const canDecide = canWrite && request?.status === 'open' && !isAuthor
  const conversation = threads.filter((thread) => !thread.file_path)
  const candidates = (members ?? []).filter(
    (member) =>
      member.user_id !== request?.created_by &&
      !reviewRequests.some((entry) => entry.reviewer.user_id === member.user_id),
  )

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

  async function submitDecision() {
    await run(async () => {
      await submitMergeRequestReview(org, project, id, { state: reviewState, body: body.trim(), comments: [] })
      setBody('')
    })
  }

  async function postConversation() {
    await run(async () => {
      await addMergeRequestComment(org, project, id, { file_path: '', body: body.trim() })
      setBody('')
    })
  }

  const pending = busy || sending

  return (
    <Stack direction="vertical" gap="normal">
      <ErrorBanner error={error} />

      <Stack direction="horizontal" gap="normal" style={{ alignItems: 'center' }}>
        <Text style={{ color: 'var(--fgColor-success)', fontWeight: 600 }}>{state?.approvals ?? 0} approved</Text>
        <Text style={{ color: 'var(--fgColor-danger)', fontWeight: 600 }}>
          {state?.changes_requested ?? 0} changes requested
        </Text>
        {state?.dismissed_approvals ? (
          <Text style={{ color: 'var(--fgColor-muted)' }}>{state.dismissed_approvals} dismissed</Text>
        ) : null}
        {state?.head_commit_id && (
          <Mono style={{ color: 'var(--fgColor-muted)' }}>head {state.head_commit_id}</Mono>
        )}
      </Stack>

      {isAuthor && request?.status === 'open' && (
        <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
          This is your own merge request, so you can comment but not review it.
        </Text>
      )}

      {reviewRequests.length > 0 && (
        <Stack direction="vertical" gap="condensed">
          {reviewRequests.map((entry) => (
            <Stack key={entry.id} direction="horizontal" gap="condensed" style={{ alignItems: 'center' }}>
              <Text>
                <strong>{actorName(entry.reviewer)}</strong> was asked to review by {actorName(entry.requested_by)}
              </Text>
              {canWrite && (
                <Button
                  size="small"
                  variant="invisible"
                  disabled={pending}
                  onClick={() => run(() => removeMergeRequestReviewRequest(org, project, id, entry.reviewer.user_id))}
                >
                  cancel
                </Button>
              )}
            </Stack>
          ))}
        </Stack>
      )}

      {canWrite && request?.status === 'open' && candidates.length > 0 && (
        <Stack direction="horizontal" gap="condensed" style={{ alignItems: 'center' }}>
          <FormControl>
            <Select value={reviewer} onChange={(event) => setReviewer(event.target.value)}>
              <option value="">Ask someone to review…</option>
              {candidates.map((member) => (
                <option key={member.user_id} value={member.user_id}>
                  {member.name || member.email}
                </option>
              ))}
            </Select>
          </FormControl>
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

      {conversation.length > 0 && (
        <Stack direction="vertical" gap="condensed">
          <Text style={{ fontWeight: 600 }}>Conversation</Text>
          {conversation.map((thread) => (
            <ThreadCard
              key={thread.id}
              thread={thread}
              me={me}
              canWrite={canWrite}
              busy={pending}
              onReply={onReplyThread}
              onResolve={onResolveThread}
              onEditComment={onEditComment}
              onDeleteComment={onDeleteComment}
            />
          ))}
        </Stack>
      )}

      {canWrite && request?.status === 'open' && (
        <Stack direction="vertical" gap="condensed">
          <Textarea
            value={body}
            placeholder="Leave a comment"
            onChange={(event) => setBody(event.target.value)}
          />
          {canDecide ? (
            <Stack direction="horizontal" gap="condensed" style={{ alignItems: 'center' }}>
              <FormControl>
                <Select value={reviewState} onChange={(event) => setReviewState(event.target.value as ReviewState)}>
                  <option value="commented">Comment</option>
                  <option value="approved">Approve</option>
                  <option value="changes_requested">Request changes</option>
                </Select>
              </FormControl>
              <Button size="small" disabled={pending || !body.trim()} onClick={submitDecision}>
                Submit review
              </Button>
            </Stack>
          ) : (
            <Button
              size="small"
              style={{ alignSelf: 'flex-start' }}
              disabled={pending || !body.trim()}
              onClick={postConversation}
            >
              Comment
            </Button>
          )}
        </Stack>
      )}

      {reviews.length > 0 && (
        <Stack direction="vertical" gap="normal">
          <Text style={{ fontWeight: 600 }}>Reviews</Text>
          {reviews.map((review) => (
            <div
              key={review.id}
              style={{
                border: '1px solid var(--borderColor-muted)',
                borderRadius: 6,
                padding: 8,
                opacity: review.dismissed_at || review.stale ? 0.65 : 1,
              }}
            >
              <Text as="p">
                <strong>{actorName(review.reviewer)}</strong> <ReviewBadge review={review} />{' '}
                <span style={{ color: 'var(--fgColor-muted)', fontSize: 11 }}>
                  {new Date(review.created_at).toLocaleString()}
                </span>
              </Text>
              {review.body && <Text as="p">{review.body}</Text>}
              {review.dismissed_at && (
                <Text as="p" style={{ color: 'var(--fgColor-muted)', fontSize: 11 }}>
                  {review.dismissed_reason === 'new_commits' ? 'Dismissed by new commits' : 'Dismissed'} by{' '}
                  {actorName(review.dismissed_by)} · {new Date(review.dismissed_at).toLocaleString()}
                </Text>
              )}
              {canWrite && !review.dismissed_at && (
                <Stack direction="horizontal" gap="condensed">
                  {me === review.reviewer.user_id && (
                    <Button
                      size="small"
                      variant="invisible"
                      disabled={pending}
                      onClick={() => run(() => withdrawMergeRequestReview(org, project, id, review.id))}
                    >
                      Withdraw
                    </Button>
                  )}
                  {me !== review.reviewer.user_id && (
                    <Button
                      size="small"
                      variant="invisible"
                      disabled={pending}
                      onClick={() => run(() => dismissMergeRequestReview(org, project, id, review.id))}
                    >
                      Dismiss
                    </Button>
                  )}
                </Stack>
              )}
            </div>
          ))}
        </Stack>
      )}

      {timeline.length > 0 && (
        <Stack direction="vertical" gap="condensed">
          <Text style={{ fontWeight: 600 }}>Activity</Text>
          {timeline.map((item) => (
            <Text key={item.id} as="p" style={{ color: 'var(--fgColor-muted)' }}>
              {timelineLabel(item)} · {new Date(item.created_at).toLocaleString()}
            </Text>
          ))}
        </Stack>
      )}

      {!reviews.length && !reviewRequests.length && !threads.length && !timeline.length && (
        <EmptyState>No review activity yet.</EmptyState>
      )}
    </Stack>
  )
}

function timelineLabel(item: TimelineItemResponse): string {
  const who = actorName(item.actor)
  switch (item.kind) {
    case 'review_requested':
      return `${who} requested a review from ${actorName(item.subject)}`
    case 'review_request_removed':
      return `${who} removed the review request for ${actorName(item.subject)}`
    case 'review_submitted':
      return `${who} reviewed: ${item.body ? `“${item.body}”` : 'no comment'}`
    case 'review_dismissed':
      return `${who} dismissed a review`
    case 'pushed':
      return `${who} pushed new commits`
    default:
      return `${who} ${item.kind.replace(/_/g, ' ')}`
  }
}
