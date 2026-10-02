import { useState } from 'react'
import { Button, Dialog, FormControl, Stack, TextInput } from '@primer/react'
import { createOrg } from '../api/endpoints'
import { ErrorBanner } from './ui'

const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]*$/

function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

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
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const effectiveSlug = slugEdited ? slug : slugify(name)
  const slugValid = effectiveSlug === '' || SLUG_PATTERN.test(effectiveSlug)
  const canSubmit = name.trim() !== '' && slugValid && !loading

  async function handleCreate() {
    if (!canSubmit) return
    setLoading(true)
    setError(null)
    try {
      const org = await createOrg(name.trim(), slugEdited ? slug.trim() : '')
      onCreated(org.slug)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
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
            <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Slug (optional)</FormControl.Label>
            <TextInput
              block
              value={effectiveSlug}
              onChange={(event) => {
                setSlugEdited(true)
                setSlug(event.target.value)
              }}
            />
            <FormControl.Caption>
              Used in URLs. Lowercase letters, digits and dashes; defaults to the name.
            </FormControl.Caption>
            {!slugValid && (
              <FormControl.Validation variant="error">
                Slug must start with a letter or digit and contain only lowercase letters, digits and dashes.
              </FormControl.Validation>
            )}
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
