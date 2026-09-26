import { useState } from 'react'
import { Button, Stack, Text, Textarea } from '@primer/react'
import type { ReviewActorResponse, ThreadResponse } from '../../api/models'
import { Mono } from '../ui'

function actorName(actor: ReviewActorResponse | undefined): string {
  if (!actor) return 'unknown'
  return actor.name || actor.user_id
}

function timestamp(value: string | undefined): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}

export function ThreadCard({
  thread,
  me,
  canWrite,
  busy,
  onReply,
  onResolve,
  onEditComment,
  onDeleteComment,
}: {
  thread: ThreadResponse
  me?: string
  canWrite: boolean
  busy?: boolean
  onReply?: (threadId: string, body: string) => void
  onResolve?: (threadId: string, resolved: boolean) => void
  onEditComment?: (threadId: string, commentId: string, body: string) => void
  onDeleteComment?: (threadId: string, commentId: string) => void
}) {
  const [replyOpen, setReplyOpen] = useState(false)
  const [replyBody, setReplyBody] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editBody, setEditBody] = useState('')

  const label = thread.file_path
    ? `${thread.side === 'left' ? 'old' : 'new'} line ${thread.new_line ?? thread.old_line ?? '?'}`
    : 'conversation'

  return (
    <div
      style={{
        border: '1px solid var(--borderColor-default)',
        borderRadius: 6,
        background: 'var(--bgColor-default)',
        opacity: thread.resolved ? 0.7 : 1,
      }}
    >
      <div
        style={{
          padding: '4px 10px',
          borderBottom: '1px solid var(--borderColor-muted)',
          fontSize: 12,
          color: 'var(--fgColor-muted)',
        }}
      >
        <Mono>{label}</Mono> · {actorName(thread.created_by)} · {timestamp(thread.created_at)}
        {thread.outdated && ' · outdated'}
        {thread.resolved && ` · resolved by ${actorName(thread.resolved_by)}`}
      </div>

      {thread.comments.map((comment) => (
        <div key={comment.id} style={{ padding: '6px 10px', borderBottom: '1px solid var(--borderColor-muted)' }}>
          {editingId === comment.id ? (
            <Stack direction="vertical" gap="condensed">
              <Textarea
                value={editBody}
                onChange={(event) => setEditBody(event.target.value)}
                aria-label="Edit comment"
              />
              <Stack direction="horizontal" gap="condensed">
                <Button
                  size="small"
                  disabled={busy || !editBody.trim()}
                  onClick={() => {
                    onEditComment?.(thread.id, comment.id, editBody.trim())
                    setEditingId(null)
                  }}
                >
                  Save
                </Button>
                <Button size="small" variant="invisible" onClick={() => setEditingId(null)}>
                  Cancel
                </Button>
              </Stack>
            </Stack>
          ) : (
            <>
              <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>
                <strong>{actorName(comment.user)}</strong> · {timestamp(comment.created_at)}
                {comment.updated_at !== comment.created_at && ' · edited'}
              </Text>
              <Text as="p" style={{ whiteSpace: 'pre-wrap' }}>
                {comment.body}
              </Text>
              {canWrite && me === comment.user.user_id && (
                <Stack direction="horizontal" gap="condensed">
                  <Button
                    size="small"
                    variant="invisible"
                    onClick={() => {
                      setEditingId(comment.id)
                      setEditBody(comment.body)
                    }}
                  >
                    Edit
                  </Button>
                  <Button
                    size="small"
                    variant="invisible"
                    disabled={busy}
                    onClick={() => onDeleteComment?.(thread.id, comment.id)}
                  >
                    Delete
                  </Button>
                </Stack>
              )}
            </>
          )}
        </div>
      ))}

      {canWrite && (
        <div style={{ padding: '6px 10px' }}>
          {replyOpen ? (
            <Stack direction="vertical" gap="condensed">
              <Textarea
                value={replyBody}
                placeholder="Reply"
                onChange={(event) => setReplyBody(event.target.value)}
                aria-label="Reply to thread"
              />
              <Stack direction="horizontal" gap="condensed">
                <Button
                  size="small"
                  disabled={busy || !replyBody.trim()}
                  onClick={() => {
                    onReply?.(thread.id, replyBody.trim())
                    setReplyBody('')
                    setReplyOpen(false)
                  }}
                >
                  Reply
                </Button>
                <Button size="small" variant="invisible" onClick={() => setReplyOpen(false)}>
                  Cancel
                </Button>
              </Stack>
            </Stack>
          ) : (
            <Stack direction="horizontal" gap="condensed">
              <Button size="small" variant="invisible" onClick={() => setReplyOpen(true)}>
                Reply
              </Button>
              {onResolve && (
                <Button
                  size="small"
                  variant="invisible"
                  disabled={busy}
                  onClick={() => onResolve(thread.id, !thread.resolved)}
                >
                  {thread.resolved ? 'Reopen' : 'Resolve'}
                </Button>
              )}
            </Stack>
          )}
        </div>
      )}
    </div>
  )
}
