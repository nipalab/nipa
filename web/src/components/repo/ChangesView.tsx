import { useState } from 'react'
import { Button, Stack, Text, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import type { DiffFileResponse, ThreadResponse } from '../../api/models'
import { EmptyState } from '../ui'
import { DiffAnchor, DiffFile, FileStatusIcon } from './DiffView'

type ViewMode = 'split' | 'unified'

function DiffStat({ additions, deletions }: { additions: number; deletions: number }) {
  const total = additions + deletions
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <span
        style={{
          display: 'inline-flex',
          width: 80,
          height: 8,
          borderRadius: 2,
          overflow: 'hidden',
          background: 'var(--bgColor-neutral-muted)',
        }}
      >
        {total > 0 && (
          <>
            <span style={{ width: `${(additions / total) * 100}%`, background: 'var(--fgColor-success)' }} />
            <span style={{ flex: 1, background: 'var(--fgColor-danger)' }} />
          </>
        )}
      </span>
      <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
        +{additions} −{deletions}
      </Text>
    </span>
  )
}

export function ChangesView({
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
  const [filter, setFilter] = useState('')
  const [viewed, setViewed] = useState<Set<string>>(new Set())
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  const query = filter.trim().toLowerCase()
  const visible = query
    ? files.filter(
        (file) =>
          file.path.toLowerCase().includes(query) || Boolean(file.old_path?.toLowerCase().includes(query)),
      )
    : files

  const additions = files.reduce((sum, file) => sum + file.additions, 0)
  const deletions = files.reduce((sum, file) => sum + file.deletions, 0)

  function toggleValue(set: Set<string>, path: string, value: boolean) {
    const next = new Set(set)
    if (value) {
      next.add(path)
    } else {
      next.delete(path)
    }
    return next
  }

  return (
    <div className="nipa-changes-layout">
      <aside className="nipa-changes-sidebar">
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          <div
            style={{
              padding: '6px 10px',
              background: 'var(--bgColor-muted)',
              borderBottom: '1px solid var(--borderColor-muted)',
              fontSize: 12,
              fontWeight: 600,
            }}
          >
            Files
          </div>
          <div style={{ maxHeight: '70vh', overflowY: 'auto' }}>
            {visible.length === 0 && (
              <Text as="p" style={{ padding: 10, margin: 0, fontSize: 12, color: 'var(--fgColor-muted)' }}>
                No matching files.
              </Text>
            )}
            {visible.map((file) => (
              <a
                key={file.path}
                href={`#file-${file.path}`}
                title={file.path}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '4px 10px',
                  color: 'inherit',
                  textDecoration: 'none',
                  borderBottom: '1px solid var(--borderColor-muted)',
                  opacity: viewed.has(file.path) ? 0.6 : 1,
                }}
              >
                <FileStatusIcon status={file.status} />
                <span
                  style={{
                    flex: 1,
                    minWidth: 0,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    fontSize: 12,
                  }}
                >
                  {file.path}
                </span>
                {!file.binary && (
                  <>
                    <span style={{ color: 'var(--fgColor-success)', fontSize: 11 }}>+{file.additions}</span>
                    <span style={{ color: 'var(--fgColor-danger)', fontSize: 11 }}>-{file.deletions}</span>
                  </>
                )}
              </a>
            ))}
          </div>
        </div>
      </aside>

      <div className="nipa-changes-main">
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            flexWrap: 'wrap',
            paddingBottom: 8,
            borderBottom: '1px solid var(--borderColor-muted)',
          }}
        >
          <Text style={{ fontWeight: 600 }}>
            {visible.length} file{visible.length === 1 ? '' : 's'} changed
          </Text>
          <DiffStat additions={additions} deletions={deletions} />
          <div style={{ width: 220 }}>
            <TextInput
              block
              leadingVisual={SearchIcon}
              placeholder="Filter files…"
              aria-label="Filter files"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
            />
          </div>
          <Stack direction="horizontal" gap="condensed" style={{ marginLeft: 'auto' }}>
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
        </div>

        {visible.length === 0 ? (
          <EmptyState>{query ? 'No files match the filter.' : 'No file changes.'}</EmptyState>
        ) : (
          visible.map((file) => (
            <DiffFile
              key={file.path}
              file={file}
              threads={threads}
              mode={mode}
              canComment={canComment}
              viewed={viewed.has(file.path)}
              collapsed={collapsed.has(file.path)}
              onToggleViewed={(next) => setViewed((current) => toggleValue(current, file.path, next))}
              onToggleCollapsed={() =>
                setCollapsed((current) => toggleValue(current, file.path, !current.has(file.path)))
              }
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
          ))
        )}
      </div>
    </div>
  )
}
