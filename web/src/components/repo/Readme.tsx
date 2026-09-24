import { BookIcon } from '@primer/octicons-react'
import { Spinner, Text } from '@primer/react'
import { Suspense, lazy } from 'react'
import { fetchBlob } from '../../api/endpoints'
import type { TreeEntryResponse } from '../../api/models'
import { useAsync } from '../../hooks'

const Markdown = lazy(() => import('./Markdown').then((module) => ({ default: module.Markdown })))

export function Readme({
  org,
  project,
  rev,
  entry,
}: {
  org: string
  project: string
  rev: string
  entry: TreeEntryResponse
}) {
  const { data: content, error, loading } = useAsync(
    () => fetchBlob(org, project, rev, entry.path).then((file) => file.blob.text()),
    [org, project, rev, entry.path],
  )
  const isMarkdown = /\.(md|markdown)$/i.test(entry.name)

  return (
    <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: '10px 16px',
          background: 'var(--bgColor-muted)',
          borderBottom: '1px solid var(--borderColor-default)',
        }}
      >
        <span style={{ color: 'var(--fgColor-muted)', display: 'inline-flex' }}>
          <BookIcon size={16} />
        </span>
        <strong>{entry.name}</strong>
      </div>
      <div style={{ padding: '16px 24px 24px' }}>
        {loading && <Spinner />}
        {error && <Text style={{ color: 'var(--fgColor-danger)' }}>{error}</Text>}
        {content !== null && !error && (
          isMarkdown ? (
            <Suspense fallback={<Spinner />}>
              <Markdown>{content}</Markdown>
            </Suspense>
          ) : (
            <pre style={{ margin: 0, overflowX: 'auto', fontSize: 13, lineHeight: 1.5 }}>{content}</pre>
          )
        )}
      </div>
    </div>
  )
}
