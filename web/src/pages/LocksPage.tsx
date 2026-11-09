import { useState } from 'react'
import { Button, FormControl, TextInput } from '@primer/react'
import { useParams } from 'react-router-dom'
import { listFileLocks, lockFile, unlockFile } from '../api/endpoints'
import type { FileLockResponse } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { DataTable, type Column } from '../components/DataTable'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { FormCard } from '../components/SettingsCard'
import { Timestamp } from '../components/Timestamp'
import { ErrorBanner, Mono, SectionHeading } from '../components/ui'
import { useAction, useAsync } from '../hooks'

export default function LocksPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: locks, error, loading, reload } = useAsync(() => listFileLocks(org, project), [org, project])
  const { run, error: actionError, clearError } = useAction(reload)
  const [path, setPath] = useState('')
  const [branch, setBranch] = useState('')

  async function handleLock(event: React.FormEvent) {
    event.preventDefault()
    await run(async () => {
      await lockFile(org, project, path, branch)
      setPath('')
    })
  }

  async function handleUnlock(lock: FileLockResponse) {
    const ok = await confirm({
      title: 'Release lock?',
      content: `${lock.path} will become editable by anyone with write access.`,
      confirmLabel: 'Unlock',
    })
    if (ok) await run(() => unlockFile(org, project, lock.path, lock.global ? '' : (lock.branch ?? '')))
  }

  const columns: Column<FileLockResponse>[] = [
    { id: 'path', header: 'Path', rowHeader: true, renderCell: (lock) => <Mono>{lock.path}</Mono> },
    { id: 'scope', header: 'Scope', renderCell: (lock) => (lock.global ? 'mainline' : `branch ${lock.branch}`) },
    {
      id: 'holder',
      header: 'Holder',
      renderCell: (lock) => (
        <>
          {lock.held_by_name || lock.held_by}
          {lock.merge_request_number != null && ` · MR #${lock.merge_request_number}`}
        </>
      ),
    },
    { id: 'acquired', header: 'Acquired', renderCell: (lock) => <Timestamp value={lock.acquired_at} /> },
    { id: 'expires', header: 'Expires', renderCell: (lock) => <Timestamp value={lock.expires_at} fallback="never" /> },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (lock) => (
        <Button size="small" onClick={() => void handleUnlock(lock)}>
          Unlock
        </Button>
      ),
    },
  ]

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="locks"
      rev={defaultBranch}
      canAdmin={canAdmin}
      heading="File locks"
    >
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <DataTable
        label="File locks"
        loading={loading}
        data={locks ?? []}
        columns={columns}
        empty="No locks. Tracked binary files can only change while you hold a lock on them."
      />

      {canWrite && (
        <FormCard onSubmit={handleLock}>
          <SectionHeading>Lock a binary file or directory</SectionHeading>
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
        </FormCard>
      )}
    </RepoPageShell>
  )
}
