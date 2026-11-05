import { useEffect, useState } from 'react'
import { Banner, Button, FormControl, Textarea, TextInput } from '@primer/react'
import { useNavigate } from 'react-router-dom'
import { listProjects, updateProject } from '../../api/endpoints'
import type { ProjectResponse } from '../../api/models'
import { errorMessage, useAsync } from '../../hooks'
import { useOrgRole } from '../../orgHooks'
import { DeleteProjectDialog } from '../DeleteProjectDialog'
import { SettingsCard, SettingsRow } from '../SettingsCard'
import { ErrorBanner, Loading, Mono } from '../ui'

export function ProjectGeneralSettings({ org, project }: { org: string; project: string }) {
  const navigate = useNavigate()
  const { canManage } = useOrgRole(org)
  const { data: projects, error, loading, reload } = useAsync(() => listProjects(org), [org])
  const current = projects?.find((entry) => entry.slug === project) ?? null
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<ProjectResponse | null>(null)

  useEffect(() => {
    if (current) {
      setName(current.name)
      setDescription(current.description ?? '')
    }
  }, [current?.id, current?.name, current?.description])

  const dirty = current !== null && (name.trim() !== current.name || description !== (current.description ?? ''))

  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (!current || !dirty || name.trim() === '' || saving) return
    setSaving(true)
    setSaveError(null)
    setNotice(null)
    try {
      await updateProject(org, project, name.trim(), description)
      setNotice('Repository updated.')
      reload()
    } catch (err) {
      setSaveError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <Loading variant="skeleton" />
  if (!current) return <ErrorBanner error={error ?? 'Repository not found.'} />

  return (
    <>
      <form onSubmit={save}>
        <SettingsCard
          title="General"
          description="The display name and description shown in repository lists."
          footer={
            <Button type="submit" variant="primary" loading={saving} disabled={!dirty || name.trim() === ''}>
              Save changes
            </Button>
          }
        >
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <ErrorBanner error={saveError} title="Could not save" onDismiss={() => setSaveError(null)} />
            {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
          </div>
          <SettingsRow label="Name">
            <FormControl required>
              <FormControl.Label visuallyHidden>Name</FormControl.Label>
              <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
            </FormControl>
          </SettingsRow>
          <SettingsRow label="Slug" subtext="The URL name cannot be changed.">
            <Mono>{current.slug}</Mono>
          </SettingsRow>
          <SettingsRow label="Description">
            <FormControl>
              <FormControl.Label visuallyHidden>Description</FormControl.Label>
              <Textarea block rows={3} value={description} onChange={(event) => setDescription(event.target.value)} />
            </FormControl>
          </SettingsRow>
        </SettingsCard>
      </form>

      {canManage && (
        <SettingsCard title="Danger zone">
          <SettingsRow
            label="Delete this repository"
            subtext="The repository becomes inaccessible and its slug cannot be reused."
          >
            <Button variant="danger" onClick={() => setDeleting(current)}>
              Delete repository
            </Button>
          </SettingsRow>
        </SettingsCard>
      )}

      <DeleteProjectDialog
        org={org}
        project={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => navigate(`/${org}`)}
      />
    </>
  )
}
