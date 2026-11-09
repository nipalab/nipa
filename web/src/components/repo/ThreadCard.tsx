import { lazy, Suspense, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, IconButton, Label, Stack, Text, Textarea } from '@primer/react'
import { PencilIcon, TrashIcon } from '@primer/octicons-react'
import type { ThreadResponse } from '../../api/models'
import { Mono } from '../ui'
import { ActorAvatar, actorName } from '../ActorAvatar'
import { useConfirmAction } from '../confirm'
import { Timestamp } from '../Timestamp'

const Markdown = lazy(() => import('./Markdown').then((module) => ({ default: module.Markdown })))

function CommentBody({ body, markdown }: { body: string; markdown: boolean }) {
  const plain = (
    <Text as="p" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
      {body}
    </Text>
  )
  if (!markdown) return plain
  return (
    <Suspense fallback={plain}>
      <Markdown>{body}</Markdown>
    </Suspense>
  )
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
  authorId,
  onReply,
  onResolve,
  onEditComment,
  onDeleteComment,
  onDeleteThread,
  canAdmin,
}: {
  thread: ThreadResponse
  me?: string
  canWrite: boolean
  busy?: boolean
  variant?: 'inline' | 'conversation'
  context?: ReactNode
  headerAction?: ReactNode
  snippet?: ReactNode
  authorId?: string
  onReply?: (threadId: string, body: string) => void
  onResolve?: (threadId: string, resolved: boolean) => void
  onEditComment?: (threadId: string, commentId: string, body: string) => void
  onDeleteComment?: (threadId: string, commentId: string) => void
  onDeleteThread?: (threadId: string) => void
  canAdmin?: boolean
}) {
  const confirm = useConfirmAction()
  const [replyOpen, setReplyOpen] = useState(false)
  const [replyBody, setReplyBody] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editBody, setEditBody] = useState('')

  const conversation = variant === 'conversation'
  const fileThread = Boolean(thread.file_path)
  const label = fileThread
    ? `${thread.side === 'left' ? 'old' : 'new'} line ${thread.new_line ?? thread.old_line ?? '?'}`
    : 'conversation'

  const commentActions = (commentId: string, body: string) => (
    <Stack direction="horizontal" gap="none">
      <IconButton
        icon={PencilIcon}
        aria-label="Edit comment"
        size="small"
        variant="invisible"
        disabled={busy}
        onClick={() => {
          setEditingId(commentId)
          setEditBody(body)
        }}
      />
      <IconButton
        icon={TrashIcon}
        aria-label="Delete comment"
        size="small"
        variant="invisible"
        disabled={busy}
        onClick={async () => {
          const ok = await confirm({
            title: 'Delete comment?',
            content: 'This comment will be permanently removed.',
            confirmLabel: 'Delete',
          })
          if (ok) onDeleteComment?.(thread.id, commentId)
        }}
      />
    </Stack>
  )

  const editor = (
    <Stack direction="vertical" gap="condensed">
      <Textarea block value={editBody} onChange={(event) => setEditBody(event.target.value)} aria-label="Edit comment" />
      <Stack direction="horizontal" gap="condensed" justify="end">
        <Button size="small" onClick={() => setEditingId(null)}>
          Cancel
        </Button>
        <Button
          size="small"
          variant="primary"
          disabled={busy || !editBody.trim()}
          onClick={() => {
            onEditComment?.(thread.id, editingId ?? '', editBody.trim())
            setEditingId(null)
          }}
        >
          Save
        </Button>
      </Stack>
    </Stack>
  )

  const footer = canWrite && (
    <div className={conversation ? 'nipa-thread-footer' : undefined} style={conversation ? undefined : { padding: '6px 10px' }}>
      {replyOpen ? (
        <Stack direction="vertical" gap="condensed">
          <Textarea
            block
            value={replyBody}
            placeholder="Write a reply…"
            onChange={(event) => setReplyBody(event.target.value)}
            aria-label="Reply to thread"
          />
          <Stack direction="horizontal" gap="condensed" justify={conversation ? 'end' : 'start'}>
            {conversation && (
              <Button size="small" onClick={() => setReplyOpen(false)}>
                Cancel
              </Button>
            )}
            <Button
              size="small"
              variant={conversation ? 'primary' : 'default'}
              disabled={busy || !replyBody.trim()}
              onClick={() => {
                onReply?.(thread.id, replyBody.trim())
                setReplyBody('')
                setReplyOpen(false)
              }}
            >
              Reply
            </Button>
            {!conversation && (
              <Button size="small" onClick={() => setReplyOpen(false)}>
                Cancel
              </Button>
            )}
          </Stack>
        </Stack>
      ) : (
        <Stack direction="horizontal" gap="condensed" align="center">
          {conversation ? (
            <button type="button" className="nipa-thread-reply" onClick={() => setReplyOpen(true)}>
              Reply
            </button>
          ) : (
            <Button size="small" onClick={() => setReplyOpen(true)}>
              Reply
            </Button>
          )}
          {onResolve && (!conversation || fileThread) && (
            <Button size="small" disabled={busy} onClick={() => onResolve(thread.id, !thread.resolved)}>
              {conversation
                ? thread.resolved
                  ? 'Unresolve conversation'
                  : 'Resolve conversation'
                : thread.resolved
                  ? 'Reopen'
                  : 'Resolve'}
            </Button>
          )}
          {onDeleteThread && (me === thread.created_by.user_id || canAdmin) && (
            <Button
              size="small"
              variant="danger"
              disabled={busy}
              onClick={async () => {
                const ok = await confirm({
                  title: 'Delete thread?',
                  content: 'The whole thread and all of its comments will be permanently removed.',
                  confirmLabel: 'Delete thread',
                })
                if (ok) onDeleteThread(thread.id)
              }}
            >
              Delete thread
            </Button>
          )}
        </Stack>
      )}
    </div>
  )

  if (conversation) {
    return (
      <div className="nipa-tl-box nipa-thread" style={{ opacity: thread.resolved ? 0.85 : 1 }}>
        {fileThread && (
          <div className="nipa-tl-box-header">
            <span className="nipa-tl-box-title">{context}</span>
            {thread.outdated && (
              <Label variant="attention" size="small">
                Outdated
              </Label>
            )}
            {thread.resolved && (
              <Label variant="success" size="small">
                Resolved
              </Label>
            )}
            {headerAction && <span className="nipa-tl-box-actions">{headerAction}</span>}
          </div>
        )}
        {fileThread && snippet && <div className="nipa-thread-snippet">{snippet}</div>}
        {thread.comments.map((comment, index) => (
          <div
            key={comment.id}
            className={!fileThread && index === 0 ? 'nipa-thread-comment nipa-thread-comment-lead' : 'nipa-thread-comment'}
          >
            <div className="nipa-thread-comment-header">
              {(fileThread || index > 0) && <ActorAvatar actor={comment.user} size={20} />}
              <strong>{actorName(comment.user)}</strong>
              <span className="nipa-thread-meta">
                {!fileThread && index === 0 ? 'commented ' : ''}
                <Timestamp value={comment.created_at} mode="absolute" />
                {comment.updated_at !== comment.created_at && ' · edited'}
              </span>
              {authorId && comment.user.user_id === authorId && (
                <Label variant="secondary" size="small">
                  Author
                </Label>
              )}
              {canWrite && me === comment.user.user_id && editingId !== comment.id && (
                <span style={{ marginLeft: 'auto' }}>{commentActions(comment.id, comment.body)}</span>
              )}
            </div>
            <div className="nipa-thread-comment-body">
              {editingId === comment.id ? editor : <CommentBody body={comment.body} markdown />}
            </div>
          </div>
        ))}
        {footer}
      </div>
    )
  }

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
      <div
        style={{
          padding: '4px 10px',
          borderBottom: '1px solid var(--borderColor-muted)',
          fontSize: 12,
          color: 'var(--fgColor-muted)',
        }}
      >
        <Mono>{label}</Mono> · {actorName(thread.created_by)} · <Timestamp value={thread.created_at} mode="absolute" />
        {thread.outdated && ' · outdated'}
        {thread.resolved && ` · resolved by ${actorName(thread.resolved_by)}`}
      </div>

      {thread.comments.map((comment) => (
        <div key={comment.id} style={{ borderBottom: '1px solid var(--borderColor-muted)', padding: '6px 10px' }}>
          <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>
            <strong>{actorName(comment.user)}</strong> · <Timestamp value={comment.created_at} mode="absolute" />
            {comment.updated_at !== comment.created_at && ' · edited'}
          </Text>
          {editingId === comment.id ? (
            editor
          ) : (
            <>
              <CommentBody body={comment.body} markdown={false} />
              {canWrite && me === comment.user.user_id && commentActions(comment.id, comment.body)}
            </>
          )}
        </div>
      ))}

      {footer}
    </div>
  )
}
