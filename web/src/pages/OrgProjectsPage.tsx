import { useState } from 'react'
import { Button, Link as PrimerLink } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listProjects } from '../api/endpoints'
import type { ProjectResponse } from '../api/models'
import { DeleteProjectDialog } from '../components/DeleteProjectDialog'
import { OrgNav } from '../components/OrgNav'
import { Row, RowList } from '../components/RowList'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'
import { useOrgRole } from '../orgHooks'

export default function OrgProjectsPage() {
  const { org = '' } = useParams()
  const navigate = useNavigate()
  const { canManage } = useOrgRole(org)
  const { data: projects, error, loading, reload } = useAsync(() => listProjects(org), [org])
  const [deleting, setDeleting] = useState<ProjectResponse | null>(null)

  return (
    <Page
      title={org}
      subtitle="Repositories in this organization"
      actions={
        canManage && (
          <Button variant="primary" onClick={() => navigate(`/${org}/new`)}>
            New repository
          </Button>
        )
      }
    >
      <OrgNav org={org} active="projects" canManage={canManage} />
      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && projects && projects.length === 0 && <EmptyState>No repositories you can read yet.</EmptyState>}
      {projects && projects.length > 0 && (
        <RowList>
          {projects.map((project, index) => (
            <Row
              key={project.id}
              first={index === 0}
              title={
                <PrimerLink as={Link} to={`/${org}/${project.slug}`}>
                  {project.name}
                </PrimerLink>
              }
              meta={`${project.slug}${project.description ? ` · ${project.description}` : ''}`}
              actions={
                canManage && (
                  <Button variant="danger" size="small" onClick={() => setDeleting(project)}>
                    Delete
                  </Button>
                )
              }
            />
          ))}
        </RowList>
      )}

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
