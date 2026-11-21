import { useState } from 'react'
import { ActionList, ActionMenu, Button, Link, Stack } from '@primer/react'
import { listIssueTrackerJobs, retryIssueTrackerJob } from '../api/endpoints'
import type { IssueTrackerJobResponse } from '../api/models'
import { useAction, useAsync } from '../hooks'
import { DataTable, type Column } from './DataTable'
import { SettingsCard } from './SettingsCard'
import { StateBadge } from './StateBadge'
import { Timestamp } from './Timestamp'
import { ErrorBanner } from './ui'

const STATE_OPTIONS = [
  { value: '', label: 'All states' },
  { value: 'failed', label: 'Failed' },
  { value: 'pending', label: 'Pending' },
  { value: 'delivered', label: 'Delivered' },
]

const ACTION_LABELS: Record<IssueTrackerJobResponse['action'], string> = {
  link: 'Link',
  unlink: 'Unlink',
  comment: 'Comment',
  transition: 'Transition',
}

const ellipsis = {
  display: 'inline-block',
  maxWidth: 260,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'bottom',
} as const

function jobDetail(job: IssueTrackerJobResponse) {
  if (job.action === 'transition') return job.status ? `To ${job.status}` : '—'
  if (job.action === 'unlink') return 'Remove the merge request link'
  if (job.action === 'link' && job.url) {
    return (
      <Link href={job.url} target="_blank" rel="noreferrer" style={ellipsis} title={job.title}>
        {job.title || job.url}
      </Link>
    )
  }
  return job.title || '—'
}

export function IssueTrackerJobs({ org, project }: { org: string; project: string }) {
  const [state, setState] = useState('')
  const { data, error, loading, reload } = useAsync(
    () => listIssueTrackerJobs(org, project, state),
    [org, project, state],
  )
  const { run, error: actionError, clearError } = useAction(reload)
  const stateLabel = STATE_OPTIONS.find((option) => option.value === state)?.label ?? 'All states'
  const [retrying, setRetrying] = useState<string | null>(null)

  async function retry(job: IssueTrackerJobResponse) {
    setRetrying(job.id)
    await run(() => retryIssueTrackerJob(org, project, job.id))
    setRetrying(null)
  }

  const columns: Column<IssueTrackerJobResponse>[] = [
    { id: 'issue', header: 'Issue', rowHeader: true, renderCell: (job) => job.issue_key },
    { id: 'action', header: 'Action', renderCell: (job) => ACTION_LABELS[job.action] ?? job.action },
    { id: 'detail', header: 'Detail', renderCell: jobDetail },
    { id: 'state', header: 'State', renderCell: (job) => <StateBadge status={job.state} /> },
    { id: 'attempts', header: 'Attempts', renderCell: (job) => job.attempts },
    {
      id: 'error',
      header: 'Last error',
      renderCell: (job) => (
        <span style={ellipsis} title={job.last_error}>
          {job.last_error || '—'}
        </span>
      ),
    },
    { id: 'created', header: 'Created', renderCell: (job) => <Timestamp value={job.created_at} /> },
    {
      id: 'actions',
      header: '',
      align: 'end',
      renderCell: (job) =>
        job.state === 'pending' ? null : (
          <Button size="small" disabled={retrying === job.id} onClick={() => void retry(job)}>
            Retry
          </Button>
        ),
    },
  ]

  return (
    <Stack direction="vertical" gap="normal">
      <SettingsCard
        title="Jobs"
        description="Remote links (and their removal), merge comments and transitions sent to the issue tracker, newest first. Rate limits and outages retry automatically; retrying queues a delivered or failed job again."
        actions={
          <ActionMenu>
            <ActionMenu.Button aria-label="Filter by job state">
              <span style={{ color: 'var(--fgColor-muted)' }}>State:</span> {stateLabel}
            </ActionMenu.Button>
            <ActionMenu.Overlay width="small" align="end">
              <ActionList selectionVariant="single" aria-label="Job state">
                {STATE_OPTIONS.map((option) => (
                  <ActionList.Item
                    key={option.value}
                    selected={option.value === state}
                    onSelect={() => setState(option.value)}
                  >
                    {option.label}
                  </ActionList.Item>
                ))}
              </ActionList>
            </ActionMenu.Overlay>
          </ActionMenu>
        }
      >
        <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      </SettingsCard>
      <DataTable
        label="Issue tracker jobs"
        loading={loading}
        data={data?.jobs ?? []}
        columns={columns}
        empty="No issue tracker jobs."
      />
    </Stack>
  )
}
