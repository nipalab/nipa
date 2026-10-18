import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, Textarea, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ApiError } from '../api/client'
import { createProject, listOrgs } from '../api/endpoints'
import { useAuth } from '../auth'
import { ErrorBanner, Loading, Page } from '../components/ui'
import { useAsync } from '../hooks'
import { SLUG_PATTERN, slugify } from '../slug'

export default function NewProjectPage() {
  const { org = '' } = useParams()
  const { me } = useAuth()
  const navigate = useNavigate()
  const { data: orgs, loading: orgsLoading } = useAsync(listOrgs, [])
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [slugError, setSlugError] = useState<string | null>(null)
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const role = orgs?.find((item) => item.slug === org)?.role
  const canManage = Boolean(me?.is_admin || me?.is_super_admin || role === 'owner')
  const effectiveSlug = slugEdited ? slug : slugify(name)
  const slugValid = effectiveSlug !== '' && SLUG_PATTERN.test(effectiveSlug)
  const canSubmit = name.trim() !== '' && slugValid && !saving

  if (orgsLoading) {
    return (
      <Page title={`New repository in ${org}`}>
        <Loading />
      </Page>
    )
  }

  if (!canManage) {
    return (
      <Page title={`New repository in ${org}`} subtitle="Create a repository in this organization">
        <Text>Only organization owners can create repositories.</Text>
        <PrimerLink as={Link} to={`/${org}`}>
          back to repositories
        </PrimerLink>
      </Page>
    )
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setActionError(null)
    setSlugError(null)
    try {
      const project = await createProject(org, name.trim(), description, effectiveSlug)
      navigate(`/${org}/${project.slug}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSlugError('This slug is already taken. Choose a different one.')
      } else {
        setActionError(err instanceof Error ? err.message : String(err))
      }
      setSaving(false)
    }
  }

  return (
    <Page
      title={`New repository in ${org}`}
      subtitle="Create a repository in this organization"
      actions={
        <PrimerLink as={Link} to={`/${org}`}>
          back to repositories
        </PrimerLink>
      }
    >
      <ErrorBanner error={actionError} />
      <form
        onSubmit={handleSubmit}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput
              block
              value={name}
              onChange={(event) => {
                setName(event.target.value)
                setSlugError(null)
              }}
              autoFocus
            />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Slug</FormControl.Label>
            <TextInput
              block
              value={effectiveSlug}
              onChange={(event) => {
                setSlugEdited(true)
                setSlug(event.target.value)
                setSlugError(null)
              }}
            />
            <FormControl.Caption>
              Used in URLs. Lowercase letters, digits and dashes; defaults to the name.
            </FormControl.Caption>
            {!slugValid && (
              <FormControl.Validation variant="error">
                {effectiveSlug === ''
                  ? 'Slug is required.'
                  : 'Slug must start with a letter or digit and contain only lowercase letters, digits and dashes.'}
              </FormControl.Validation>
            )}
            {slugError && <FormControl.Validation variant="error">{slugError}</FormControl.Validation>}
          </FormControl>
          <FormControl>
            <FormControl.Label>Description</FormControl.Label>
            <Textarea block rows={3} value={description} onChange={(event) => setDescription(event.target.value)} />
          </FormControl>
          <Button
            type="submit"
            variant="primary"
            disabled={!canSubmit}
            loading={saving}
            style={{ alignSelf: 'flex-start' }}
          >
            Create repository
          </Button>
        </Stack>
      </form>
    </Page>
  )
}
