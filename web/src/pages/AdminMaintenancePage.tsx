import { endServerMaintenance, getAdminMaintenance, setServerMaintenance } from '../api/endpoints'
import { useAuth } from '../auth'
import { MaintenanceForm } from '../components/MaintenanceForm'
import { MaintenanceWindowList } from '../components/MaintenanceSettings'
import { ErrorBanner, Loading, NoPermission, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function AdminMaintenancePage() {
  const { me } = useAuth()
  const allowed = Boolean(me?.is_super_admin)
  const { data, error, loading, reload } = useAsync(
    () => (allowed ? getAdminMaintenance() : Promise.resolve(null)),
    [allowed],
  )

  return (
    <Page
      title="Maintenance"
      subtitle="Put the server into maintenance and review the organization and project windows"
      breadcrumbs={[{ label: 'Site administration' }, { label: 'Maintenance' }]}
    >
      {!allowed ? (
        <NoPermission>Only super admins can manage server maintenance.</NoPermission>
      ) : loading && !data ? (
        <Loading />
      ) : !data ? (
        <ErrorBanner error={error} title="Could not load maintenance" />
      ) : (
        <>
          <MaintenanceForm
            scope="server"
            target=""
            window={data.server}
            source={data.source}
            notifyLabel="Email organization owners"
            onSave={setServerMaintenance}
            onEnd={endServerMaintenance}
            onChanged={reload}
          />
          <MaintenanceWindowList
            title="Organization windows"
            windows={data.orgs ?? []}
            empty="No organization has maintenance scheduled."
          />
          <MaintenanceWindowList
            title="Project windows"
            windows={data.projects ?? []}
            empty="No project has maintenance scheduled."
          />
        </>
      )}
    </Page>
  )
}
