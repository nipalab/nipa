import { useState } from 'react'
import { Button, FormControl, Textarea } from '@primer/react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError } from '../api/client'
import { createProject } from '../api/endpoints'
import { FormCard } from '../components/SettingsCard'
import { SlugFields, useSlugFields } from '../components/SlugFields'
import { ErrorBanner, NoPermission, Page } from '../components/ui'
import { errorMessage } from '../hooks'
import { useOrgRole } from '../orgHooks'
import { RESERVED_PROJECT_SLUGS } from '../slug'

export default function NewProjectPage() {
  const { org = '' } = useParams()
  const navigate = useNavigate()
  const { canManage, loading: roleLoading } = useOrgRole(org)
  const slugFields = useSlugFields(RESERVED_PROJECT_SLUGS)
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const crumbs = [{ label: org, to: `/${org}` }, { label: 'New repository' }]
  const title = `New repository in ${org}`
  const canSubmit = slugFields.valid && !saving

  if (roleLoading || !canManage) {
    return (
      <Page title={title} breadcrumbs={crumbs}>
        <NoPermission loading={roleLoading}>Only organization owners can create repositories.</NoPermission>
      </Page>
    )
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setActionError(null)
    try {
      const project = await createProject(org, slugFields.name.trim(), description, slugFields.slug)
      navigate(`/${org}/${project.slug}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        slugFields.markTaken()
      } else {
        setActionError(errorMessage(err))
      }
      setSaving(false)
    }
  }

  return (
    <Page title={title} subtitle="Create a repository in this organization" breadcrumbs={crumbs}>
      <ErrorBanner error={actionError} />
      <FormCard onSubmit={handleSubmit}>
        <SlugFields state={slugFields} autoFocus />
        <FormControl>
          <FormControl.Label>Description</FormControl.Label>
          <Textarea block rows={3} value={description} onChange={(event) => setDescription(event.target.value)} />
        </FormControl>
        <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving} style={{ alignSelf: 'flex-start' }}>
          Create repository
        </Button>
      </FormCard>
    </Page>
  )
}
