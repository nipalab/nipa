import { Label, Link as PrimerLink, Stack } from '@primer/react'
import { Link } from 'react-router-dom'
import {
  endOrgMaintenance,
  endProjectMaintenance,
  getOrgMaintenance,
  getProjectMaintenance,
  setOrgMaintenance,
  setProjectMaintenance,
} from '../api/endpoints'
import type { MaintenanceWindow } from '../api/models'
import { useAsync } from '../hooks'
import { formatLocal, modeLabel } from '../maintenance'
import { MaintenanceForm } from './MaintenanceForm'
import { MaintenanceWindowBanner } from './MaintenanceBanner'
import { SettingsCard, SettingsRow } from './SettingsCard'
import { EmptyState, ErrorBanner, Loading } from './ui'

export const NOTIFY_SCOPED = 'Email organization owners and project admins'

export function MaintenanceWindowList({
  title,
  windows,
  empty,
}: {
  title: string
  windows: MaintenanceWindow[]
  empty: string
}) {
  return (
    <SettingsCard title={title}>
      {windows.length === 0 && <EmptyState bordered={false}>{empty}</EmptyState>}
      {windows.map((window) => {
        const target = window.project || window.org || ''
        const end = formatLocal(window.ends_at)
        return (
          <SettingsRow
            key={target}
            label={
              <PrimerLink as={Link} to={`/${target}/settings?tab=maintenance`}>
                {target}
              </PrimerLink>
            }
            subtext={window.message || undefined}
          >
            <span style={{ display: 'inline-flex', gap: 'var(--base-size-8)', alignItems: 'center', flexWrap: 'wrap' }}>
              <Label variant={window.active ? 'attention' : 'accent'}>{window.active ? 'Active' : 'Scheduled'}</Label>
              <span>{modeLabel(window.mode)}</span>
              <span style={{ color: 'var(--fgColor-muted)' }}>
                {window.active ? 'since' : 'from'} {formatLocal(window.starts_at) || 'now'}
                {end ? `, estimated end ${end}` : ''}
              </span>
            </span>
          </SettingsRow>
        )
      })}
    </SettingsCard>
  )
}

export function OrgMaintenanceTab({ org }: { org: string }) {
  const { data, error, loading, reload } = useAsync(() => getOrgMaintenance(org), [org])
  if (loading && !data) return <Loading />
  if (!data) return <ErrorBanner error={error} title="Could not load maintenance" />
  return (
    <Stack direction="vertical" gap="normal">
      {data.server && <MaintenanceWindowBanner item={{ scope: 'server', target: '', window: data.server }} />}
      <MaintenanceForm
        scope="org"
        target={org}
        window={data.org}
        notifyLabel={NOTIFY_SCOPED}
        onSave={(input) => setOrgMaintenance(org, input)}
        onEnd={() => endOrgMaintenance(org)}
        onChanged={reload}
      />
      {data.projects && (
        <MaintenanceWindowList
          title="Project windows"
          windows={data.projects}
          empty="No project in this organization has maintenance scheduled."
        />
      )}
    </Stack>
  )
}

export function ProjectMaintenanceTab({ org, project }: { org: string; project: string }) {
  const { data, error, loading, reload } = useAsync(() => getProjectMaintenance(org, project), [org, project])
  if (loading && !data) return <Loading />
  if (!data) return <ErrorBanner error={error} title="Could not load maintenance" />
  return (
    <Stack direction="vertical" gap="normal">
      {data.server && <MaintenanceWindowBanner item={{ scope: 'server', target: '', window: data.server }} />}
      {data.org && <MaintenanceWindowBanner item={{ scope: 'org', target: org, window: data.org }} />}
      <MaintenanceForm
        scope="project"
        target={`${org}/${project}`}
        window={data.project}
        notifyLabel={NOTIFY_SCOPED}
        onSave={(input) => setProjectMaintenance(org, project, input)}
        onEnd={() => endProjectMaintenance(org, project)}
        onChanged={reload}
      />
    </Stack>
  )
}
