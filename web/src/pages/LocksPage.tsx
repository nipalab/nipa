import { useState } from 'react'
import { Button, FormControl, Stack, TextInput } from '@primer/react'
import { useParams } from 'react-router-dom'
import { listFileLocks, lockFile, unlockFile } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'
import { useAsync } from '../hooks'

export default function LocksPage() {
  const { org = '', project = '' } = useParams()
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: locks, error, loading, reload } = useAsync(
    () => listFileLocks(org, project),
    [org, project],
  )
  const [path, setPath] = useState('')
  const [branch, setBranch] = useState('')
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

  async function handleLock(event: React.FormEvent) {
    event.preventDefault()
    await run(async () => {
      await lockFile(org, project, path, branch)
      setPath('')
    })
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="locks"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="File locks"
    >
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}
      {!loading && locks && locks.length === 0 && (
        <EmptyState>
          No locks. Tracked binary files can only change while you hold a lock on them.
        </EmptyState>
      )}
      {locks && locks.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ color: 'var(--fgColor-muted)', textAlign: 'left' }}>
              <th style={{ padding: '6px 4px' }}>Path</th>
              <th style={{ padding: '6px 4px' }}>Scope</th>
              <th style={{ padding: '6px 4px' }}>Holder</th>
              <th style={{ padding: '6px 4px' }}>Acquired</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {locks.map((lock) => (
              <tr key={lock.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={{ padding: '6px 4px' }}>
                  <Mono>{lock.path}</Mono>
                </td>
                <td style={{ padding: '6px 4px' }}>
                  {lock.global ? 'mainline' : `branch ${lock.branch}`}
                </td>
                <td style={{ padding: '6px 4px' }}>
                  {lock.held_by_name || lock.held_by}
                  {lock.merge_request_number != null && ` · MR #${lock.merge_request_number}`}
                </td>
                <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                  {new Date(lock.acquired_at).toLocaleString()}
                </td>
                <td style={{ padding: '6px 4px', textAlign: 'right' }}>
                  <Button
                    size="small"
                    onClick={() => run(() => unlockFile(org, project, lock.path, lock.global ? '' : (lock.branch ?? '')))}
                  >
                    Unlock
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {canWrite && (
        <form
          onSubmit={handleLock}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <Stack direction="vertical" gap="normal">
            <strong>Lock a binary file or directory</strong>
            <FormControl required>
              <FormControl.Label>Path</FormControl.Label>
              <TextInput
                block
                placeholder="assets/textures/orc.png or assets/textures"
                value={path}
                onChange={(event) => setPath(event.target.value)}
              />
            </FormControl>
            <FormControl>
              <FormControl.Label>Branch scope (empty uses the current branch)</FormControl.Label>
              <TextInput block value={branch} onChange={(event) => setBranch(event.target.value)} />
            </FormControl>
            <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
              Lock
            </Button>
          </Stack>
        </form>
      )}
    </RepoPageShell>
  )
}
