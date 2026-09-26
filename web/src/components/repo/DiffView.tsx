import { Fragment, useState } from 'react'
import { Button, Stack, Text, Textarea } from '@primer/react'
import type { DiffFileResponse, DiffHunkResponse, DiffLineResponse, ThreadResponse } from '../../api/models'
import { Mono, StatusLabel } from '../ui'
import { ThreadCard } from './ThreadCard'

export interface DiffAnchor {
  filePath: string
  oldLine?: number
  newLine?: number
}

type ViewMode = 'split' | 'unified'

interface LineRow {
  kind: 'line'
  left?: DiffLineResponse
  right?: DiffLineResponse
}

interface HunkRow {
  kind: 'hunk'
  hunk: DiffHunkResponse
}

type Row = LineRow | HunkRow

const ADD_BG = 'var(--bgColor-success-muted)'
const REMOVE_BG = 'var(--bgColor-danger-muted)'
const EMPTY_BG = 'var(--bgColor-neutral-muted)'
const CONTEXT_BG = 'transparent'

function lineBackground(line: DiffLineResponse | undefined, side: 'left' | 'right'): string {
  if (!line) return EMPTY_BG
  if (side === 'left') return line.kind === 'remove' ? REMOVE_BG : CONTEXT_BG
  return line.kind === 'add' ? ADD_BG : CONTEXT_BG
}

function sign(kind: string | undefined): string {
  if (kind === 'add') return '+'
  if (kind === 'remove') return '-'
  return ' '
}

// buildRows turns a hunk's line stream into split-view rows: a remove run is
// paired one-to-one with the add run that follows it, the shorter side is
// padded, and context lines sit on both sides of one row.
function buildRows(hunks: DiffHunkResponse[]): Row[] {
  const rows: Row[] = []
  for (const hunk of hunks) {
    rows.push({ kind: 'hunk', hunk })
    let i = 0
    while (i < hunk.lines.length) {
      const line = hunk.lines[i]
      if (line.kind === 'context') {
        rows.push({ kind: 'line', left: line, right: line })
        i++
        continue
      }
      const removes: DiffLineResponse[] = []
      const adds: DiffLineResponse[] = []
      while (i < hunk.lines.length && hunk.lines[i].kind === 'remove') {
        removes.push(hunk.lines[i])
        i++
      }
      while (i < hunk.lines.length && hunk.lines[i].kind === 'add') {
        adds.push(hunk.lines[i])
        i++
      }
      const pairs = Math.max(removes.length, adds.length)
      for (let j = 0; j < pairs; j++) {
        rows.push({ kind: 'line', left: removes[j], right: adds[j] })
      }
    }
  }
  return rows
}

function threadOnLeft(thread: ThreadResponse, line: DiffLineResponse | undefined): boolean {
  return Boolean(line) && thread.side === 'left' && thread.old_line === line?.old_line
}

function threadOnRight(thread: ThreadResponse, line: DiffLineResponse | undefined): boolean {
  return Boolean(line) && thread.side !== 'left' && thread.new_line === line?.new_line
}

// A renamed file keeps comments on its removed lines under the old path, so a
// thread belongs to the file when it names either path.
function threadMatchesFile(thread: ThreadResponse, file: DiffFileResponse): boolean {
  if (thread.file_path === file.path) return true
  return Boolean(file.old_path) && thread.file_path === file.old_path
}

function threadsOnRow(threads: ThreadResponse[], file: DiffFileResponse, row: LineRow): ThreadResponse[] {
  return threads.filter(
    (thread) =>
      threadMatchesFile(thread, file) && (threadOnLeft(thread, row.left) || threadOnRight(thread, row.right)),
  )
}

function rowHasDraft(anchor: DiffAnchor | null | undefined, file: DiffFileResponse, row: LineRow): boolean {
  if (!anchor) return false
  if (anchor.filePath !== file.path && !(file.old_path && anchor.filePath === file.old_path)) return false
  if (anchor.newLine !== undefined) return row.right?.new_line === anchor.newLine
  if (anchor.oldLine !== undefined) return row.left?.old_line === anchor.oldLine
  return false
}

function DraftComment({
  anchor,
  busy,
  error,
  onCancel,
  onSubmit,
}: {
  anchor: DiffAnchor
  busy: boolean
  error: string | null
  onCancel: () => void
  onSubmit: (anchor: DiffAnchor, body: string) => void
}) {
  const [body, setBody] = useState('')
  const target = `${anchor.filePath}:${anchor.newLine ?? anchor.oldLine ?? '?'}`
  return (
    <div style={{ padding: '8px 10px', background: 'var(--bgColor-inset)' }}>
      <Text as="p" style={{ fontSize: 12, color: 'var(--fgColor-muted)' }}>
        New comment on <Mono>{target}</Mono>
      </Text>
      <Textarea
        value={body}
        placeholder="Leave a comment"
        aria-label="New inline comment"
        onChange={(event) => setBody(event.target.value)}
      />
      {error && (
        <Text as="p" style={{ color: 'var(--fgColor-danger)', fontSize: 12 }}>
          {error}
        </Text>
      )}
      <Stack direction="horizontal" gap="condensed" style={{ marginTop: 6 }}>
        <Button size="small" variant="primary" disabled={busy || !body.trim()} onClick={() => onSubmit(anchor, body.trim())}>
          Comment
        </Button>
        <Button size="small" variant="invisible" onClick={onCancel}>
          Cancel
        </Button>
      </Stack>
    </div>
  )
}

function CommentButton({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={onClick}
      style={{
        border: 'none',
        background: 'transparent',
        color: 'var(--fgColor-muted)',
        cursor: 'pointer',
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        padding: '0 2px',
      }}
    >
      +
    </button>
  )
}

function LineCell({
  line,
  side,
  canComment,
  onComment,
}: {
  line: DiffLineResponse | undefined
  side: 'left' | 'right'
  canComment: boolean
  onComment: (line: DiffLineResponse) => void
}) {
  const number = side === 'left' ? line?.old_line : line?.new_line
  return (
    <>
      <td
        style={{
          background: lineBackground(line, side),
          padding: '0 4px',
          textAlign: 'right',
          color: 'var(--fgColor-muted)',
          userSelect: 'none',
          whiteSpace: 'nowrap',
          width: 48,
        }}
      >
        {number ?? ''}
      </td>
      <td
        style={{
          background: lineBackground(line, side),
          padding: '0 4px',
          width: 14,
          userSelect: 'none',
        }}
      >
        {line ? sign(line.kind) : ''}
      </td>
      <td
        style={{
          background: lineBackground(line, side),
          padding: '0 6px',
          whiteSpace: 'pre',
          width: '50%',
        }}
      >
        {line?.text}
        {line?.no_newline && (
          <span style={{ color: 'var(--fgColor-muted)', fontStyle: 'italic' }}> \ No newline at end of file</span>
        )}
        {canComment && line && (
          <span style={{ float: 'right' }}>
            <CommentButton
              label={`Comment on ${side} line ${number}`}
              onClick={() => onComment(line)}
            />
          </span>
        )}
      </td>
    </>
  )
}

function DiffFile({
  file,
  threads,
  mode,
  canComment,
  draftAnchor,
  draftBusy,
  draftError,
  me,
  busy,
  onStartThread,
  onCancelDraft,
  onSubmitDraft,
  onReplyThread,
  onResolveThread,
  onEditComment,
  onDeleteComment,
}: {
  file: DiffFileResponse
  threads: ThreadResponse[]
  mode: ViewMode
  canComment: boolean
  draftAnchor?: DiffAnchor | null
  draftBusy?: boolean
  draftError?: string | null
  me?: string
  busy?: boolean
  onStartThread?: (anchor: DiffAnchor) => void
  onCancelDraft?: () => void
  onSubmitDraft?: (anchor: DiffAnchor, body: string) => void
  onReplyThread?: (threadId: string, body: string) => void
  onResolveThread?: (threadId: string, resolved: boolean) => void
  onEditComment?: (threadId: string, commentId: string, body: string) => void
  onDeleteComment?: (threadId: string, commentId: string) => void
}) {
  const [onlyOpenThreads, setOnlyOpenThreads] = useState(false)
  const fileThreads = threads.filter((thread) => threadMatchesFile(thread, file))
  const openCount = fileThreads.filter((thread) => !thread.resolved).length

  let rows = buildRows(file.hunks ?? [])
  if (onlyOpenThreads) {
    rows = rows.filter(
      (row) => row.kind === 'line' && threadsOnRow(threads, file, row).some((thread) => !thread.resolved),
    )
  }

  const anchorForLine = (line: DiffLineResponse, side: 'left' | 'right'): DiffAnchor => {
    if (side === 'left' && line.kind === 'remove') {
      return { filePath: file.path, oldLine: line.old_line }
    }
    return { filePath: file.path, newLine: line.new_line }
  }

  const header = (
    <div
      style={{
        padding: '6px 12px',
        borderBottom: '1px solid var(--borderColor-muted)',
        display: 'flex',
        gap: 8,
        alignItems: 'center',
        background: 'var(--bgColor-muted)',
      }}
    >
      <Mono>{file.old_path ? `${file.old_path} → ${file.path}` : file.path}</Mono>{' '}
      <StatusLabel status={file.status} />{' '}
      {!file.binary && (
        <>
          <Text style={{ color: 'var(--fgColor-success)' }}>+{file.additions}</Text>{' '}
          <Text style={{ color: 'var(--fgColor-danger)' }}>-{file.deletions}</Text>
        </>
      )}
      {openCount > 0 && (
        <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
          {openCount} open thread{openCount === 1 ? '' : 's'}
        </Text>
      )}
      {fileThreads.length > 0 && (
        <Button size="small" variant="invisible" onClick={() => setOnlyOpenThreads((value) => !value)}>
          {onlyOpenThreads ? 'Show all lines' : 'Only open threads'}
        </Button>
      )}
    </div>
  )

  if (file.binary) {
    return (
      <div
        id={`file-${file.path}`}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden', scrollMarginTop: 16 }}
      >
        {header}
        <Text as="p" style={{ padding: 12, color: 'var(--fgColor-muted)' }}>
          Binary file, no diff shown.
        </Text>
      </div>
    )
  }

  const threadRows = (row: LineRow) => {
    const anchored = threadsOnRow(threads, file, row)
    const draft = draftAnchor && rowHasDraft(draftAnchor, file, row) ? draftAnchor : null
    if (anchored.length === 0 && !draft) return null
    return (
      <div style={{ padding: 8, display: 'flex', flexDirection: 'column', gap: 6 }}>
        {anchored.map((thread) => (
          <ThreadCard
            key={thread.id}
            thread={thread}
            me={me}
            canWrite={canComment}
            busy={busy}
            onReply={onReplyThread}
            onResolve={onResolveThread}
            onEditComment={onEditComment}
            onDeleteComment={onDeleteComment}
          />
        ))}
        {draft && (
          <DraftComment
            anchor={draft}
            busy={Boolean(draftBusy)}
            error={draftError ?? null}
            onCancel={() => onCancelDraft?.()}
            onSubmit={(anchor, body) => onSubmitDraft?.(anchor, body)}
          />
        )}
      </div>
    )
  }

  const splitRow = (row: Row, index: number) => {
    if (row.kind === 'hunk') {
      return (
        <tr key={`hunk-${index}`}>
          <td
            colSpan={6}
            style={{
              background: 'var(--bgColor-inset)',
              color: 'var(--fgColor-muted)',
              padding: '2px 8px',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            }}
          >
            @@ -{row.hunk.old_start},{row.hunk.old_lines} +{row.hunk.new_start},{row.hunk.new_lines} @@
          </td>
        </tr>
      )
    }
    const rowThreads = threadRows(row)
    return (
      <Fragment key={`row-${index}`}>
        <tr>
          <LineCell
            line={row.left}
            side="left"
            canComment={canComment && row.left !== undefined && row.left.kind === 'remove'}
            onComment={(line) => onStartThread?.(anchorForLine(line, 'left'))}
          />
          <LineCell
            line={row.right}
            side="right"
            canComment={canComment && row.right !== undefined}
            onComment={(line) => onStartThread?.(anchorForLine(line, 'right'))}
          />
        </tr>
        {rowThreads && (
          <tr>
            <td colSpan={6} style={{ padding: 0, borderTop: '1px solid var(--borderColor-muted)' }}>
              {rowThreads}
            </td>
          </tr>
        )}
      </Fragment>
    )
  }

  const unifiedRow = (row: Row, index: number) => {
    if (row.kind === 'hunk') {
      return (
        <tr key={`hunk-${index}`}>
          <td
            colSpan={4}
            style={{
              background: 'var(--bgColor-inset)',
              color: 'var(--fgColor-muted)',
              padding: '2px 8px',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            }}
          >
            @@ -{row.hunk.old_start},{row.hunk.old_lines} +{row.hunk.new_start},{row.hunk.new_lines} @@
          </td>
        </tr>
      )
    }
    const rowThreads = threadRows(row)
    const cell = (line: DiffLineResponse | undefined) => (
      <>
        <td
          style={{
            background: lineBackground(line, 'left'),
            padding: '0 4px',
            textAlign: 'right',
            color: 'var(--fgColor-muted)',
            userSelect: 'none',
            whiteSpace: 'nowrap',
            width: 48,
          }}
        >
          {line?.old_line ?? ''}
        </td>
        <td
          style={{
            background: lineBackground(line, 'left'),
            padding: '0 4px',
            textAlign: 'right',
            color: 'var(--fgColor-muted)',
            userSelect: 'none',
            whiteSpace: 'nowrap',
            width: 48,
          }}
        >
          {line?.new_line ?? ''}
        </td>
        <td
          style={{
            background: lineBackground(line, 'left'),
            padding: '0 4px',
            width: 14,
            userSelect: 'none',
          }}
        >
          {line ? sign(line.kind) : ''}
        </td>
        <td
          style={{
            background: lineBackground(line, 'left'),
            padding: '0 6px',
            whiteSpace: 'pre',
          }}
        >
          {line?.text}
          {line?.no_newline && (
            <span style={{ color: 'var(--fgColor-muted)', fontStyle: 'italic' }}> \ No newline at end of file</span>
          )}
          {canComment && line && (
            <span style={{ float: 'right' }}>
              <CommentButton
                label={`Comment on line ${line.new_line ?? line.old_line ?? ''}`}
                onClick={() =>
                  onStartThread?.(
                    line.kind === 'remove'
                      ? { filePath: file.path, oldLine: line.old_line }
                      : { filePath: file.path, newLine: line.new_line },
                  )
                }
              />
            </span>
          )}
        </td>
      </>
    )
    return (
      <Fragment key={`row-${index}`}>
        {row.left === row.right ? (
          <tr>{cell(row.left)}</tr>
        ) : (
          <>
            <tr>{cell(row.left)}</tr>
            <tr>{cell(row.right)}</tr>
          </>
        )}
        {rowThreads && (
          <tr>
            <td colSpan={4} style={{ padding: 0, borderTop: '1px solid var(--borderColor-muted)' }}>
              {rowThreads}
            </td>
          </tr>
        )}
      </Fragment>
    )
  }

  return (
    <div
      id={`file-${file.path}`}
      style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden', scrollMarginTop: 16 }}
    >
      {header}
      {rows.length === 0 ? (
        <Text as="p" style={{ padding: 12, color: 'var(--fgColor-muted)' }}>
          No lines to show.
        </Text>
      ) : (
        <div style={{ overflowX: 'auto', fontSize: 12, lineHeight: 1.4 }}>
          <table style={{ borderCollapse: 'collapse', width: '100%' }}>
            <tbody>{rows.map((row, index) => (mode === 'split' ? splitRow(row, index) : unifiedRow(row, index)))}</tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export function DiffView({
  files,
  threads = [],
  canComment = false,
  draftAnchor,
  draftBusy,
  draftError,
  me,
  busy,
  onStartThread,
  onCancelDraft,
  onSubmitDraft,
  onReplyThread,
  onResolveThread,
  onEditComment,
  onDeleteComment,
}: {
  files: DiffFileResponse[]
  threads?: ThreadResponse[]
  canComment?: boolean
  draftAnchor?: DiffAnchor | null
  draftBusy?: boolean
  draftError?: string | null
  me?: string
  busy?: boolean
  onStartThread?: (anchor: DiffAnchor) => void
  onCancelDraft?: () => void
  onSubmitDraft?: (anchor: DiffAnchor, body: string) => void
  onReplyThread?: (threadId: string, body: string) => void
  onResolveThread?: (threadId: string, resolved: boolean) => void
  onEditComment?: (threadId: string, commentId: string, body: string) => void
  onDeleteComment?: (threadId: string, commentId: string) => void
}) {
  const [mode, setMode] = useState<ViewMode>('split')

  if (files.length === 0) {
    return (
      <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
        No file changes.
      </Text>
    )
  }

  return (
    <Stack direction="vertical" gap="normal">
      <Stack direction="horizontal" gap="condensed" style={{ justifyContent: 'flex-end' }}>
        <Button
          size="small"
          variant={mode === 'unified' ? 'primary' : 'default'}
          onClick={() => setMode('unified')}
        >
          Unified
        </Button>
        <Button size="small" variant={mode === 'split' ? 'primary' : 'default'} onClick={() => setMode('split')}>
          Split
        </Button>
      </Stack>
      {files.map((file) => (
        <DiffFile
          key={file.path}
          file={file}
          threads={threads}
          mode={mode}
          canComment={canComment}
          draftAnchor={draftAnchor}
          draftBusy={draftBusy}
          draftError={draftError}
          me={me}
          busy={busy}
          onStartThread={onStartThread}
          onCancelDraft={onCancelDraft}
          onSubmitDraft={onSubmitDraft}
          onReplyThread={onReplyThread}
          onResolveThread={onResolveThread}
          onEditComment={onEditComment}
          onDeleteComment={onDeleteComment}
        />
      ))}
    </Stack>
  )
}
