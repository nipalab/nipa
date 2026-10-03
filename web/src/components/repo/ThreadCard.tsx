import { useState } from 'react'
import type { ReactNode } from 'react'
import { Button, Stack, Text, Textarea } from '@primer/react'
import type { ThreadResponse } from '../../api/models'
import { Mono } from '../ui'
import { ActorAvatar, actorName } from './ActorAvatar'

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
  variant = 'inline',
  context,
  headerAction,
  snippet,
  onReply,
  onResolve,
  onEditComment,
  onDeleteComment,
}: {
  thread: ThreadResponse
  me?: string
  canWrite: boolean
  busy?: boolean
  variant?: 'inline' | 'conversation'
  context?: ReactNode
  headerAction?: ReactNode
  snippet?: ReactNode
  onReply?: (threadId: string, body: string) => void
  onResolve?: (threadId: string, resolved: boolean) => void
  onEditComment?: (threadId: string, commentId: string, body: string) => void
  onDeleteComment?: (threadId: string, commentId: string) => void
}) {
  const [replyOpen, setReplyOpen] = useState(false)
  const [replyBody, setReplyBody] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editBody, setEditBody] = useState('')

  const conversation = variant === 'conversation'
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
        overflow: 'hidden',
      }}
    >
      {!conversation && (
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
      )}

      {thread.comments.map((comment, index) => (
        <div key={comment.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
          {conversation && (
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
              <ActorAvatar actor={comment.user} size={20} />
              <strong>{actorName(comment.user)}</strong>
              <span
                style={{
                  color: 'var(--fgColor-muted)',
                  fontSize: 12,
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 4,
                  minWidth: 0,
                }}
              >
                commented on
                {context}
                <span>{timestamp(comment.created_at)}</span>
              </span>
              {comment.updated_at !== comment.created_at && (
                <span style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>· edited</span>
              )}
              {thread.outdated && (
                <span style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>· outdated</span>
              )}
              {thread.resolved && (
                <span style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>· resolved</span>
              )}
              {headerAction && <span style={{ marginLeft: 'auto' }}>{headerAction}</span>}
            </div>
          )}
          <div style={{ padding: conversation ? '10px 12px' : '6px 10px' }}>
            {!conversation && (
              <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>
                <strong>{actorName(comment.user)}</strong> · {timestamp(comment.created_at)}
                {comment.updated_at !== comment.created_at && ' · edited'}
              </Text>
            )}
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
                {conversation && index === 0 && snippet && <div style={{ marginBottom: 6 }}>{snippet}</div>}
                <Text as="p" style={{ whiteSpace: 'pre-wrap', margin: conversation ? 0 : undefined }}>
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
        </div>
      ))}

      {canWrite && (
        <div style={{ padding: conversation ? '8px 12px' : '6px 10px' }}>
          {replyOpen ? (
            <Stack direction="vertical" gap="condensed">
              <Textarea
                value={replyBody}
                placeholder="Write a reply…"
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
              {onResolve && (!conversation || thread.file_path) && (
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
