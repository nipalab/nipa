import { Link as PrimerLink, Stack, Text } from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { getCommitDiff, listCommits } from '../api/endpoints'
import { EmptyState, ErrorBanner, Loading, Mono, Page, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function CommitsPage() {
  const { org = '', project = '', commit } = useParams()
  const [params] = useSearchParams()
  const branch = params.get('branch') ?? ''
  const { data: commits, error, loading } = useAsync(
    () => listCommits(org, project, branch),
    [org, project, branch],
  )
  const { data: diff, error: diffError, loading: diffLoading } = useAsync(
    () => (commit ? getCommitDiff(org, project, commit) : Promise.resolve(null)),
    [org, project, commit],
  )

  return (
    <Page
      title="Commits"
      subtitle={`${org}/${project}${branch ? ` · ${branch}` : ''}`}
      actions={<PrimerLink as={Link} to={`/${org}/${project}`}>back to files</PrimerLink>}
    >
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && commits && commits.length === 0 && <EmptyState>No commits yet.</EmptyState>}
      {commits && commits.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {commits.map((entry) => (
              <tr key={entry.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={{ padding: '6px 4px' }}>
                  <PrimerLink as={Link} to={`/${org}/${project}/commits/${entry.id}${branch ? `?branch=${branch}` : ''}`}>
                    {entry.message}
                  </PrimerLink>
                </td>
                <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                  {entry.author_name ?? entry.author_email ?? ''}
                </td>
                <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                  <Mono>{entry.id.slice(0, 10)}</Mono>
                </td>
                <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                  {new Date(entry.created_at).toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {commit && (
        <Stack direction="vertical" gap="normal">
          <Text as="h3">Diff of <Mono>{commit}</Mono></Text>
          <ErrorBanner error={diffError} />
          {diffLoading && <Loading />}
          {diff?.files.map((file) => (
            <div key={file.path} style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6 }}>
              <div style={{ padding: '6px 12px', borderBottom: '1px solid var(--borderColor-muted)' }}>
                <Mono>{file.path}</Mono> <StatusLabel status={file.status} />{' '}
                <Text style={{ color: 'var(--fgColor-success)' }}>+{file.additions}</Text>{' '}
                <Text style={{ color: 'var(--fgColor-danger)' }}>-{file.deletions}</Text>
                {file.binary && <Text style={{ color: 'var(--fgColor-muted)' }}> binary</Text>}
              </div>
              {!file.binary && file.patch && (
                <pre style={{ margin: 0, padding: 12, overflowX: 'auto', fontSize: 12, lineHeight: 1.4 }}>
                  {file.patch.join('\n')}
                </pre>
              )}
            </div>
          ))}
        </Stack>
      )}
    </Page>
  )
}
