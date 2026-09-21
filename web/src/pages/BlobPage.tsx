import { Link as PrimerLink, Stack, Text } from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { getBlob } from '../api/endpoints'
import { ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function BlobPage() {
  const { org = '', project = '' } = useParams()
  const [params] = useSearchParams()
  const rev = params.get('rev') ?? ''
  const path = params.get('path') ?? ''
  const { data: content, error, loading } = useAsync(
    () => getBlob(org, project, rev, path),
    [org, project, rev, path],
  )

  const binary = content !== null && (content.includes('\u0000') || content.includes('\uFFFD'))
  const treeQuery = new URLSearchParams()
  if (rev) treeQuery.set('rev', rev)
  const parent = path.split('/').slice(0, -1).join('/')
  if (parent) treeQuery.set('path', parent)

  return (
    <Page
      title={path || 'file'}
      subtitle={`${org}/${project}`}
      actions={
        <Stack direction="horizontal" gap="normal">
          <PrimerLink as={Link} to={`/${org}/${project}?${treeQuery.toString()}`}>
            back to tree
          </PrimerLink>
          <PrimerLink as={Link} to={`/${org}/${project}/commits${rev ? `?branch=${rev}` : ''}`}>
            history
          </PrimerLink>
        </Stack>
      }
    >
      <Text style={{ color: 'var(--fgColor-muted)' }}>
        revision: <Mono>{rev || 'default branch'}</Mono>
      </Text>
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {content !== null && binary && (
        <Text>Binary file ({content.length} bytes) — preview is not available.</Text>
      )}
      {content !== null && !binary && (
        <pre
          style={{
            background: 'var(--bgColor-muted)',
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            padding: 12,
            overflowX: 'auto',
            fontSize: 13,
            lineHeight: 1.5,
          }}
        >
          {content}
        </pre>
      )}
    </Page>
  )
}
