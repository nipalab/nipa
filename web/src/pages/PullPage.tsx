import { useState } from 'react'
import { Button, Stack, Text } from '@primer/react'
import { useParams } from 'react-router-dom'
import {
  closeMergeRequest,
  getMergeRequest,
  getMergeRequestDiff,
  mergeMergeRequest,
  reopenMergeRequest,
} from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading, Mono, StatusLabel } from '../components/ui'
import { useAsync } from '../hooks'

export default function PullPage() {
  const { org = '', project = '', id = '' } = useParams()
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: request, error, loading, reload } = useAsync(
    () => getMergeRequest(org, project, id),
    [org, project, id],
  )
  const { data: diff, error: diffError, loading: diffLoading } = useAsync(
    () => getMergeRequestDiff(org, project, id),
    [org, project, id],
  )
  const [actionError, setActionError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  const mergeable = request?.mergeability?.status === 'mergeable'
  const canToggle = request?.status === 'open' || request?.status === 'closed'

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="pulls"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading={request?.title ?? 'Merge request'}
    >
      <Text style={{ color: 'var(--fgColor-muted)' }}>
        {request?.source_branch ?? ''} → {request?.target_branch ?? ''}
      </Text>
      <ErrorBanner error={actionError ?? error ?? diffError} />
      {loading && <Loading />}
      {request && (
        <Stack direction="vertical" gap="normal">
          <Text>
            Status: <StatusLabel status={request.status} />{' '}
            {request.mergeability && (
              <>
                · mergeability: <StatusLabel status={request.mergeability.status} />
              </>
            )}
          </Text>
          {request.description && <Text>{request.description}</Text>}
          <Mono>{request.id}</Mono>
          <Stack direction="horizontal" gap="normal">
            {request.status === 'open' && (
              <Button
                variant="primary"
                disabled={!mergeable}
                onClick={() => run(() => mergeMergeRequest(org, project, id))}
              >
                Merge
              </Button>
            )}
            {canToggle && (
              <Button
                onClick={() =>
                  run(() =>
                    request.status === 'open'
                      ? closeMergeRequest(org, project, id)
                      : reopenMergeRequest(org, project, id),
                  )
                }
              >
                {request.status === 'open' ? 'Close' : 'Reopen'}
              </Button>
            )}
          </Stack>
          {request.status === 'open' && !mergeable && (
            <Text style={{ color: 'var(--fgColor-attention)' }}>
              The source branch must contain the target before it can merge (update the branch first).
            </Text>
          )}
        </Stack>
      )}

      {diffLoading && <Loading />}
      {diff?.files.map((file) => (
        <div key={file.path} style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6 }}>
          <div style={{ padding: '6px 12px', borderBottom: '1px solid var(--borderColor-muted)' }}>
            <Mono>{file.path}</Mono> <StatusLabel status={file.status} />{' '}
            <Text style={{ color: 'var(--fgColor-success)' }}>+{file.additions}</Text>{' '}
            <Text style={{ color: 'var(--fgColor-danger)' }}>-{file.deletions}</Text>
          </div>
          {!file.binary && file.patch && (
            <pre style={{ margin: 0, padding: 12, overflowX: 'auto', fontSize: 12, lineHeight: 1.4 }}>
              {file.patch.join('\n')}
            </pre>
          )}
        </div>
      ))}
    </RepoPageShell>
  )
}
