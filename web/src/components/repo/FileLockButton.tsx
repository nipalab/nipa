import { useEffect } from 'react'
import { Button, Label } from '@primer/react'
import { LockIcon, UnlockIcon } from '@primer/octicons-react'
import { listFileLocks, lockFile, unlockFile } from '../../api/endpoints'
import type { FileLockResponse } from '../../api/models'
import { useAction, useAsync } from '../../hooks'
import { useConfirmAction } from '../confirm'

function coveringLock(locks: FileLockResponse[], path: string): FileLockResponse | undefined {
  return locks.find((lock) => lock.path === path) ?? locks.find((lock) => path.startsWith(`${lock.path.replace(/\/$/, '')}/`))
}

export function FileLockButton({
  org,
  project,
  path,
  me,
  canWrite,
  onError,
}: {
  org: string
  project: string
  path: string
  me?: string
  canWrite: boolean
  onError: (message: string | null) => void
}) {
  const confirm = useConfirmAction()
  const { data: locks, error, reload } = useAsync(() => listFileLocks(org, project), [org, project])
  const { run, busy, error: actionError } = useAction(reload)

  const lock = locks ? coveringLock(locks, path) : undefined
  const shownError = actionError ?? error
  useEffect(() => {
    onError(shownError)
  }, [shownError])

  if (!locks) return null

  if (!lock) {
    if (!canWrite) return null
    return (
      <Button size="small" leadingVisual={LockIcon} disabled={busy} onClick={() => void run(() => lockFile(org, project, path, ''))}>
        Lock
      </Button>
    )
  }

  const mine = me !== undefined && lock.held_by === me
  if (!mine || !canWrite) {
    return (
      <Label variant="attention">
        Locked by {lock.held_by_name || lock.held_by}
        {lock.path !== path ? ` (${lock.path})` : ''}
      </Label>
    )
  }

  async function release() {
    if (!lock) return
    const ok = await confirm({
      title: 'Release lock?',
      content: `${lock.path} will become editable by anyone with write access.`,
      confirmLabel: 'Unlock',
    })
    if (ok) await run(() => unlockFile(org, project, lock.path, lock.global ? '' : (lock.branch ?? '')))
  }

  return (
    <Button size="small" leadingVisual={UnlockIcon} disabled={busy} onClick={() => void release()}>
      Unlock
    </Button>
  )
}
