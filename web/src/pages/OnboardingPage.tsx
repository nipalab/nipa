import { useState } from 'react'
import { Button, FormControl, Heading, Stack, TextInput } from '@primer/react'
import { RepoIcon } from '@primer/octicons-react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ApiError } from '../api/client'
import { createOrg, listOrgs } from '../api/endpoints'
import { ErrorBanner, Loading } from '../components/ui'
import { useAsync } from '../hooks'
import { SLUG_PATTERN, slugify } from '../slug'

// OnboardingPage is the shared first-run step after registration confirmation
// and social signup: name the first workspace. Users who already belong to an
// organization skip it.
export default function OnboardingPage() {
  const navigate = useNavigate()
  const { data: orgs, error: loadError, loading } = useAsync(listOrgs, [])
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [slugError, setSlugError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const effectiveSlug = slugEdited ? slug : slugify(name)
  const slugValid = effectiveSlug !== '' && SLUG_PATTERN.test(effectiveSlug)

  if (loading) {
    return (
      <div style={{ maxWidth: 420, margin: '48px auto 0', padding: '0 16px' }}>
        <Loading />
      </div>
    )
  }
  if (loadError) {
    return (
      <div style={{ maxWidth: 420, margin: '48px auto 0', padding: '0 16px' }}>
        <ErrorBanner error={loadError} />
      </div>
    )
  }
  if (orgs && orgs.length > 0) {
    return <Navigate to="/" replace />
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim() || !slugValid || busy) return
    setBusy(true)
    setError(null)
    setSlugError(null)
    try {
      const org = await createOrg(name.trim(), effectiveSlug)
      navigate(`/${org.slug}`, { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSlugError('This slug is already taken. Choose a different one.')
      } else {
        setError(err instanceof Error ? err.message : String(err))
      }
      setBusy(false)
    }
  }

  return (
    <div style={{ maxWidth: 420, margin: '48px auto 0', padding: '0 16px' }}>
      <Stack direction="vertical" gap="spacious" align="center">
        <Heading as="h1">
          <RepoIcon size={24} verticalAlign="middle" /> Welcome to Nipa
        </Heading>

        <form
          onSubmit={handleSubmit}
          style={{
            width: '100%',
            border: '1px solid var(--borderColor-default)',
            borderRadius: 6,
            padding: 24,
          }}
        >
          <Stack direction="vertical" gap="normal">
            <Heading as="h3" style={{ margin: 0, fontSize: 16 }}>
              Name your workspace
            </Heading>
            <FormControl required>
              <FormControl.Label>Organization name</FormControl.Label>
              <TextInput
                block
                value={name}
                onChange={(e) => {
                  setName(e.target.value)
                  setSlugError(null)
                }}
                placeholder="e.g. Acme Game Studio"
                autoFocus
              />
              <FormControl.Caption>
                You become its owner and can invite members and create projects afterwards.
              </FormControl.Caption>
            </FormControl>

            <FormControl required>
              <FormControl.Label>Slug</FormControl.Label>
              <TextInput
                block
                value={effectiveSlug}
                onChange={(e) => {
                  const value = e.target.value
                  setSlugError(null)
                  if (value.trim() === '') {
                    setSlugEdited(false)
                    setSlug('')
                    return
                  }
                  setSlugEdited(true)
                  setSlug(value)
                }}
                placeholder="acme-game-studio"
              />
              <FormControl.Caption>
                Your workspace URL: /{effectiveSlug || '…'}
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

            <ErrorBanner error={error} />

            <Button
              type="submit"
              variant="primary"
              block
              disabled={busy || !name.trim() || !slugValid}
            >
              {busy ? 'Creating…' : 'Create workspace'}
            </Button>
          </Stack>
        </form>
      </Stack>
    </div>
  )
}
