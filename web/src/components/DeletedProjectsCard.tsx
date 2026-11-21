import { Button, Text } from '@primer/react'
import { listDeletedProjects, restoreProject } from '../api/endpoints'
import type { DeletedProjectResponse } from '../api/models'
import { useAction, useAsync } from '../hooks'
import { useConfirmAction } from './confirm'
import { DataTable, type Column } from './DataTable'
import { absoluteTime, relativeTime } from './repo/format'
import { SettingsCard } from './SettingsCard'
import { ErrorBanner, Mono } from './ui'

// DeletedProjectsCard lists the organization's deleted projects that can
// still be restored. onRestored lets the page refresh what a restore changes
// (the storage usage).
export function DeletedProjectsCard({ org, onRestored }: { org: string; onRestored?: () => void }) {
  const { data, error: loadError, loading, reload } = useAsync(() => listDeletedProjects(org), [org])
  const { run, busy, error, clearError } = useAction(() => {
    reload()
    onRestored?.()
  })
  const confirm = useConfirmAction()

  async function restore(project: DeletedProjectResponse) {
    const ok = await confirm({
      title: `Restore ${project.name}?`,
      content: (
        <Text as="p" style={{ margin: 0 }}>
          The repository comes back at <Mono>{project.slug}</Mono> with its full history. Its stored content counts
          toward the storage quota again; the restore fails if it no longer fits.
        </Text>
      ),
      confirmLabel: 'Restore',
      danger: false,
    })
    if (ok) await run(() => restoreProject(org, project.id))
  }

  const columns: Column<DeletedProjectResponse>[] = [
    {
      id: 'project',
      header: 'Project',
      rowHeader: true,
      renderCell: (project) => (
        <>
          {project.name} <Mono>{project.slug}</Mono>
        </>
      ),
    },
    {
      id: 'deleted',
      header: 'Deleted',
      renderCell: (project) => <span title={absoluteTime(project.deleted_at)}>{relativeTime(project.deleted_at)}</span>,
    },
    {
      id: 'purge',
      header: 'Purged',
      renderCell: (project) =>
        project.purge_after ? (
          absoluteTime(project.purge_after)
        ) : (
          <Text style={{ color: 'var(--fgColor-muted)' }}>Kept until restored</Text>
        ),
    },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (project) => (
        <Button size="small" disabled={busy} onClick={() => void restore(project)}>
          Restore
        </Button>
      ),
    },
  ]

  return (
    <SettingsCard
      title="Deleted projects"
      description="Deleted repositories stay restorable until they are purged. Purging removes their history for good and frees their slug."
    >
      <ErrorBanner error={loadError ?? error} onDismiss={clearError} />
      <DataTable
        data={data ?? []}
        columns={columns}
        label="Deleted projects"
        loading={loading && !data}
        empty="No deleted projects."
      />
    </SettingsCard>
  )
}
