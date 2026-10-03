import { useState } from 'react'
import { Button, Link as PrimerLink, Stack } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listOrgs, listProjects } from '../api/endpoints'
import type { ProjectResponse } from '../api/models'
import { useAuth } from '../auth'
import { DeleteProjectDialog } from '../components/DeleteProjectDialog'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function OrgProjectsPage() {
  const { org = '' } = useParams()
  const { me } = useAuth()
  const navigate = useNavigate()
  const { data: orgs } = useAsync(listOrgs, [])
  const { data: projects, error, loading, reload } = useAsync(() => listProjects(org), [org])
  const [deleting, setDeleting] = useState<ProjectResponse | null>(null)

  const role = orgs?.find((item) => item.slug === org)?.role
  const canManage = Boolean(me?.is_admin || me?.is_super_admin || role === 'owner')

  return (
    <Page
      title={org}
      subtitle="Repositories in this organization"
      actions={
        canManage && (
          <Stack direction="horizontal" gap="normal" align="center">
            <PrimerLink as={Link} to={`/${org}/settings`}>
              Settings
            </PrimerLink>
            <Button variant="primary" onClick={() => navigate(`/${org}/new`)}>
              New repository
            </Button>
          </Stack>
        )
      }
    >
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && projects && projects.length === 0 && <EmptyState>No repositories you can read yet.</EmptyState>}
      {projects?.map((project) => (
        <div
          key={project.id}
          style={{
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            padding: 16,
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            gap: 16,
          }}
        >
          <div>
            <PrimerLink as={Link} to={`/${org}/${project.slug}`} style={{ fontWeight: 600 }}>
              {project.name}
            </PrimerLink>
            <div style={{ color: 'var(--fgColor-muted)', fontSize: 13 }}>
              {project.slug}
              {project.description ? ` · ${project.description}` : ''}
            </div>
          </div>
          {canManage && (
            <Button variant="danger" size="small" onClick={() => setDeleting(project)}>
              Delete
            </Button>
          )}
        </div>
      ))}

      <DeleteProjectDialog
        org={org}
        project={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          setDeleting(null)
          reload()
        }}
      />
    </Page>
  )
}
