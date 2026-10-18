import { useState } from 'react'
import { Button, Dialog, FormControl, Stack, TextInput } from '@primer/react'
import { ApiError } from '../api/client'
import { createOrg } from '../api/endpoints'
import { SLUG_PATTERN, slugify } from '../slug'
import { ErrorBanner } from './ui'

export function CreateOrgDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (slug: string) => void
}) {
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [slugError, setSlugError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const effectiveSlug = slugEdited ? slug : slugify(name)
  const slugValid = effectiveSlug !== '' && SLUG_PATTERN.test(effectiveSlug)
  const canSubmit = name.trim() !== '' && slugValid && !loading

  async function handleCreate() {
    if (!canSubmit) return
    setLoading(true)
    setError(null)
    setSlugError(null)
    try {
      const org = await createOrg(name.trim(), effectiveSlug)
      onCreated(org.slug)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSlugError('This slug is already taken. Choose a different one.')
      } else {
        setError(err instanceof Error ? err.message : String(err))
      }
      setLoading(false)
    }
  }

  return (
    <Dialog title="New organization" onClose={onClose} width="large">
      <Dialog.Body>
        <Stack direction="vertical" gap="normal">
          <ErrorBanner error={error} />
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput
              block
              autoFocus
              value={name}
              onChange={(event) => {
                setName(event.target.value)
                setSlugError(null)
              }}
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
        </Stack>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button variant="primary" disabled={!canSubmit} loading={loading} onClick={() => void handleCreate()}>
          Create organization
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
