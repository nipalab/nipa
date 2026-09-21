import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { createProject, deleteProject, listOrgs, listProjects } from '../api/endpoints'
import { useAuth } from '../auth'
import { EmptyState, ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'

export default function OrgProjectsPage() {
  const { org = '' } = useParams()
  const { me } = useAuth()
  const navigate = useNavigate()
  const { data: orgs } = useAsync(listOrgs, [])
  const { data: projects, error, loading, reload } = useAsync(() => listProjects(org), [org])
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [description, setDescription] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

  const role = orgs?.find((item) => item.slug === org)?.role
  const canManage = Boolean(me?.is_admin || me?.is_super_admin || role === 'owner')

  async function handleCreate(event: React.FormEvent) {
    event.preventDefault()
    setActionError(null)
    try {
      const project = await createProject(org, name, description, slug)
      setName('')
      setSlug('')
      setDescription('')
      reload()
      navigate(`/${org}/${project.slug}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDelete(project: string) {
    setActionError(null)
    try {
      await deleteProject(org, project)
      reload()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Page title={org} subtitle="Repositories in this organization">
      <ErrorBanner error={actionError ?? error} />
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
            <Button variant="danger" size="small" onClick={() => handleDelete(project.slug)}>
              Delete
            </Button>
          )}
        </div>
      ))}

      {canManage && (
        <form
          onSubmit={handleCreate}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <Stack direction="vertical" gap="normal">
            <strong>New repository</strong>
            <FormControl required>
              <FormControl.Label>Name</FormControl.Label>
              <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
            </FormControl>
            <FormControl>
              <FormControl.Label>Slug (optional)</FormControl.Label>
              <TextInput block value={slug} onChange={(event) => setSlug(event.target.value)} />
            </FormControl>
            <FormControl>
              <FormControl.Label>Description</FormControl.Label>
              <TextInput block value={description} onChange={(event) => setDescription(event.target.value)} />
            </FormControl>
            <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
              Create repository
            </Button>
          </Stack>
        </form>
      )}
    </Page>
  )
}
