import { useState } from 'react'
import { Button, Stack, Text } from '@primer/react'
import type { DiffFileResponse, DiffLineResponse, ThreadResponse } from '../../api/models'
import { Mono, StatusLabel } from '../ui'

const LINE_COLORS: Record<string, string> = {
  add: 'var(--bgColor-success-muted)',
  remove: 'var(--bgColor-danger-muted)',
  context: 'transparent',
}

function lineColor(kind: string, side: string): string {
  if (kind === 'add' || (side === 'right' && kind === 'context')) return LINE_COLORS.add
  if (kind === 'remove' || side === 'left') return LINE_COLORS.remove
  return 'transparent'
}

function sign(kind: string): string {
  if (kind === 'add') return '+'
  if (kind === 'remove') return '-'
  return ' '
}

function threadLine(thread: ThreadResponse): number | null {
  if (thread.side === 'left') return thread.old_line ?? null
  if (thread.side === 'right') return thread.new_line ?? null
  return null
}

function isOnLine(thread: ThreadResponse, line: DiffLineResponse): boolean {
  if (thread.file_path === undefined || thread.file_path === '') return false
  if (thread.side !== 'left') {
    return thread.new_line !== undefined && thread.new_line === line.new_line
  }
  return thread.old_line !== undefined && thread.old_line === line.old_line
}

function DiffFile({
  file,
  threads,
  canComment,
  onStartThread,
  onOpenThread,
}: {
  file: DiffFileResponse
  threads: ThreadResponse[]
  canComment: boolean
  onStartThread: (filePath: string, oldLine: number | undefined, newLine: number | undefined) => void
  onOpenThread: (id: string) => void
}) {
  const [showUnresolvedOnly, setShowUnresolvedOnly] = useState(false)
  const visible = (file.hunks ?? [])
    .flatMap((hunk) =>
      hunk.lines.map((line) => ({
        line,
        key: `${hunk.old_start}-${hunk.new_start}-${line.old_line ?? 0}-${line.new_line ?? 0}`,
      })),
    )
    .filter((entry) => {
      if (!showUnresolvedOnly) return true
      return threads.some(
        (thread) =>
          thread.file_path === file.path && !thread.resolved && isOnLine(thread, entry.line),
      )
    })

  const header = (
    <div style={{ padding: '6px 12px', borderBottom: '1px solid var(--borderColor-muted)', display: 'flex', gap: 8, alignItems: 'center' }}>
      <Mono>{file.old_path ? `${file.old_path} → ${file.path}` : file.path}</Mono> <StatusLabel status={file.status} />{' '}
      <Text style={{ color: 'var(--fgColor-success)' }}>+{file.additions}</Text>{' '}
      <Text style={{ color: 'var(--fgColor-danger)' }}>-{file.deletions}</Text>
      <Button size="small" variant="invisible" onClick={() => setShowUnresolvedOnly((value) => !value)}>
        {showUnresolvedOnly ? 'Show all lines' : 'Only open threads'}
      </Button>
    </div>
  )

  if (file.binary) {
    return (
      <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6 }}>
        {header}
        <Text as="p" style={{ padding: 12, color: 'var(--fgColor-muted)' }}>
          Binary file, no diff shown.
        </Text>
      </div>
    )
  }

  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6 }}>
      {header}
      {visible.length === 0 ? (
        <Text as="p" style={{ padding: 12, color: 'var(--fgColor-muted)' }}>
          No lines to show.
        </Text>
      ) : (
        <div style={{ overflowX: 'auto', fontSize: 12, lineHeight: 1.4 }}>
          <table style={{ borderCollapse: 'collapse', width: '100%' }}>
            <tbody>
              {visible.map((entry) => {
                const line = entry.line
                const own = threads.filter((thread) => thread.file_path === file.path && isOnLine(thread, line))
                const side = line.kind === 'add' ? 'right' : line.kind === 'remove' ? 'left' : 'right'
                return (
                  <tr key={entry.key}>
                    <td style={{ background: lineColor(line.kind, side), padding: '0 4px', textAlign: 'right', color: 'var(--fgColor-muted)', userSelect: 'none', whiteSpace: 'nowrap' }}>
                      {line.old_line ?? ''}
                    </td>
                    <td style={{ background: lineColor(line.kind, side), padding: '0 4px', textAlign: 'right', color: 'var(--fgColor-muted)', userSelect: 'none', whiteSpace: 'nowrap' }}>
                      {line.new_line ?? ''}
                    </td>
                    <td style={{ background: lineColor(line.kind, side), padding: '0 4px', width: 12, userSelect: 'none' }}>
                      {sign(line.kind)}
                    </td>
                    <td style={{ background: lineColor(line.kind, side), padding: '0 4px', whiteSpace: 'pre' }}>
                      {line.text}
                    </td>
                    <td style={{ padding: '0 4px', whiteSpace: 'nowrap' }}>
                      {canComment && (
                        <Button
                          size="small"
                          variant="invisible"
                          aria-label="Comment on this line"
                          onClick={() => onStartThread(file.path, line.old_line, line.new_line)}
                        >
                          comment
                        </Button>
                      )}
                      {own.length > 0 && (
                        <span style={{ fontSize: 11, color: 'var(--fgColor-muted)' }}>
                          {own.length} comment{own.length === 1 ? '' : 's'}
                        </span>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          {threads
            .filter((thread) => thread.file_path === file.path)
            .map((thread) => (
              <div
                key={thread.id}
                style={{
                  borderTop: '1px solid var(--borderColor-muted)',
                  padding: 8,
                  opacity: thread.resolved ? 0.65 : 1,
                  background: thread.outdated ? 'var(--bgColor-attention-muted)' : 'transparent',
                }}
              >
                <Text as="p" style={{ fontSize: 11, color: 'var(--fgColor-muted)' }}>
                  <Mono>
                    {thread.side}:{threadLine(thread) ?? '?'}
                  </Mono>{' '}
                  · {thread.created_by.name} · {new Date(thread.created_at).toLocaleString()}
                  {thread.outdated && ' · outdated'}
                  {thread.resolved && ` · resolved by ${thread.resolved_by?.name ?? 'someone'}`}
                </Text>
                {thread.comments.map((comment) => (
                  <Text key={comment.id} as="p" style={{ marginTop: 4 }}>
                    <strong>{comment.user.name}</strong>{' '}
                    <span style={{ color: 'var(--fgColor-muted)', fontSize: 11 }}>
                      {new Date(comment.created_at).toLocaleString()}
                    </span>
                    <br />
                    {comment.body}
                  </Text>
                ))}
                <Button size="small" variant="invisible" onClick={() => onOpenThread(thread.id)}>
                  Reply
                </Button>
              </div>
            ))}
        </div>
      )}
    </div>
  )
}

export function MergeRequestDiff({
  files,
  threads,
  canComment,
  onStartThread,
  onOpenThread,
}: {
  files: DiffFileResponse[]
  threads: ThreadResponse[]
  canComment: boolean
  onStartThread: (filePath: string, oldLine: number | undefined, newLine: number | undefined) => void
  onOpenThread: (id: string) => void
}) {
  if (files.length === 0) {
    return (
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        No file changes.
      </Text>
    )
  }
  return (
    <Stack gap="normal">
      {files.map((file) => (
        <DiffFile
          key={file.path}
          file={file}
          threads={threads}
          canComment={canComment}
          onStartThread={onStartThread}
          onOpenThread={onOpenThread}
        />
      ))}
    </Stack>
  )
}
