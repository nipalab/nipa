import { Text } from '@primer/react'
import { getProjectStorage } from '../../api/endpoints'
import { useAsync } from '../../hooks'
import { SettingsCard, SettingsRow } from '../SettingsCard'
import { ErrorBanner, Loading } from '../ui'
import { useByoStorage } from './storage'

export function ProjectStorageCard({ org, project }: { org: string; project: string }) {
  const { enabled } = useByoStorage()
  if (!enabled) return null
  return <ProjectStorageInfo org={org} project={project} />
}

function ProjectStorageInfo({ org, project }: { org: string; project: string }) {
  const { data, error, loading } = useAsync(() => getProjectStorage(org, project), [org, project])
  return (
    <SettingsCard title="Storage" description="Chosen when the repository was created; it cannot be changed.">
      <ErrorBanner error={error} />
      {loading && !data ? (
        <Loading variant="skeleton" lines={1} />
      ) : (
        data && (
          <SettingsRow label="Content stored on">
            <Text style={{ fontWeight: 600 }}>{data.server ? data.server.name : 'Nipa storage'}</Text>
            <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
              {data.external
                ? 'An S3-compatible server owned by the organization’s billing owner. It does not count toward the storage quota.'
                : 'Managed by Nipa and counted toward the organization’s storage quota.'}
            </div>
          </SettingsRow>
        )
      )}
    </SettingsCard>
  )
}
