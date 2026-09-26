import { useState } from 'react'
import { Button, FormControl, Select, Stack, Text, Textarea } from '@primer/react'
import {
  addMergeRequestComment,
  dismissMergeRequestReview,
  listOrgMembers,
  removeMergeRequestReviewRequest,
  replyMergeRequestThread,
  requestMergeRequestReview,
  resolveMergeRequestThread,
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

export interface DiffAnchor {
  filePath: string
  oldLine?: number
  newLine?: number
}

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
  draftAnchor,
  activeThreadId,
  canWrite,
  onChanged,
  onCancelDraft,
  onOpenThread,
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
  draftAnchor: DiffAnchor | null
  activeThreadId: string | null
  canWrite: boolean
  onChanged: () => void
  onCancelDraft: () => void
  onOpenThread: (id: string | null) => void
}) {
  const { data: members } = useAsync(() => listOrgMembers(org), [org])
  const [reviewState, setReviewState] = useState<ReviewState>('commented')
  const [body, setBody] = useState('')
  const [reviewer, setReviewer] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const isAuthor = Boolean(me && request && request.created_by === me)
  const canDecide = canWrite && request?.status === 'open' && !isAuthor
  const activeThread = threads.find((thread) => thread.id === activeThreadId) ?? null
  const candidates = (members ?? []).filter(
    (member) => member.user_id !== request?.created_by && !reviewRequests.some((entry) => entry.reviewer.user_id === member.user_id),
  )

  async function run(action: () => Promise<unknown>) {
    setError(null)
    setBusy(true)
    try {
      await action()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function submit() {
    await run(async () => {
      await submitMergeRequestReview(org, project, id, { state: reviewState, body: body.trim(), comments: [] })
      setBody('')
    })
  }

  async function postDraft() {
    if (!body.trim()) return
    await run(async () => {
      if (activeThread) {
        await replyMergeRequestThread(org, project, id, activeThread.id, body.trim())
      } else if (draftAnchor) {
        await addMergeRequestComment(org, project, id, {
          file_path: draftAnchor.filePath,
          old_line: draftAnchor.oldLine,
          new_line: draftAnchor.newLine,
          body: body.trim(),
        })
      }
      setBody('')
      onCancelDraft()
      onOpenThread(null)
    })
  }

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
                  disabled={busy}
                  onClick={() =>
                    run(() => removeMergeRequestReviewRequest(org, project, id, entry.reviewer.user_id))
                  }
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
            disabled={busy || !reviewer}
            onClick={() => run(async () => {
              await requestMergeRequestReview(org, project, id, reviewer)
              setReviewer('')
            })}
          >
            Request review
          </Button>
        </Stack>
      )}

      {canWrite && request?.status === 'open' && (
        <Stack direction="vertical" gap="condensed">
          {(activeThread || draftAnchor) && (
            <Stack direction="vertical" gap="condensed">
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                {activeThread
                  ? `Replying to ${actorName(activeThread.created_by)}`
                  : `New comment on ${draftAnchor?.filePath}:${draftAnchor?.newLine ?? draftAnchor?.oldLine}`}
              </Text>
              {activeThread?.comments.map((comment) => (
                <Text key={comment.id} as="p">
                  <strong>{actorName(comment.user)}</strong>: {comment.body}
                </Text>
              ))}
              <Textarea
                value={body}
                placeholder="Comment"
                onChange={(event) => setBody(event.target.value)}
              />
              <Stack direction="horizontal" gap="condensed">
                <Button size="small" disabled={busy || !body.trim()} onClick={postDraft}>
                  {activeThread ? 'Reply' : 'Comment'}
                </Button>
                <Button
                  size="small"
                  variant="invisible"
                  onClick={() => {
                    setBody('')
                    onCancelDraft()
                    onOpenThread(null)
                  }}
                >
                  Cancel
                </Button>
              </Stack>
            </Stack>
          )}
          {!activeThread && !draftAnchor && (
            <Textarea
              value={body}
              placeholder="Leave a review comment"
              onChange={(event) => setBody(event.target.value)}
            />
          )}
          {canDecide ? (
            <Stack direction="horizontal" gap="condensed" style={{ alignItems: 'center' }}>
              <FormControl>
                <Select value={reviewState} onChange={(event) => setReviewState(event.target.value as ReviewState)}>
                  <option value="commented">Comment</option>
                  <option value="approved">Approve</option>
                  <option value="changes_requested">Request changes</option>
                </Select>
              </FormControl>
              <Button size="small" disabled={busy || !body.trim()} onClick={submit}>
                Submit review
              </Button>
            </Stack>
          ) : (
            !activeThread &&
            !draftAnchor && (
              <Button
                size="small"
                disabled={busy || !body.trim()}
                onClick={() =>
                  run(() =>
                    addMergeRequestComment(org, project, id, { file_path: '', body: body.trim() }),
                  ).then(() => setBody(''))
                }
              >
                Comment
              </Button>
            )
          )}
        </Stack>
      )}

      {threads.filter((thread) => !thread.file_path).length > 0 && (
        <Stack direction="vertical" gap="normal">
          <Text style={{ fontWeight: 600 }}>Conversation</Text>
          {threads
            .filter((thread) => !thread.file_path)
            .map((thread) => (
              <div
                key={thread.id}
                style={{
                  border: '1px solid var(--borderColor-muted)',
                  borderRadius: 6,
                  padding: 8,
                  opacity: thread.resolved ? 0.65 : 1,
                }}
              >
                {thread.comments.map((comment) => (
                  <Text key={comment.id} as="p">
                    <strong>{actorName(comment.user)}</strong>{' '}
                    <span style={{ color: 'var(--fgColor-muted)', fontSize: 11 }}>
                      {new Date(comment.created_at).toLocaleString()}
                    </span>
                    <br />
                    {comment.body}
                  </Text>
                ))}
                {canWrite && (
                  <Stack direction="horizontal" gap="condensed">
                    <Button size="small" variant="invisible" onClick={() => onOpenThread(thread.id)}>
                      Reply
                    </Button>
                    <Button
                      size="small"
                      variant="invisible"
                      disabled={busy}
                      onClick={() =>
                        run(() => resolveMergeRequestThread(org, project, id, thread.id, !thread.resolved))
                      }
                    >
                      {thread.resolved ? 'Reopen' : 'Resolve'}
                    </Button>
                  </Stack>
                )}
              </div>
            ))}
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
                      disabled={busy}
                      onClick={() => run(() => withdrawMergeRequestReview(org, project, id, review.id))}
                    >
                      Withdraw
                    </Button>
                  )}
                  {me !== review.reviewer.user_id && (
                    <Button
                      size="small"
                      variant="invisible"
                      disabled={busy}
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
