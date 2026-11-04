import { useState } from 'react'
import { FormControl, TextInput } from '@primer/react'
import { SLUG_PATTERN, slugify } from '../slug'

export const SLUG_TAKEN_MESSAGE = 'This slug is already taken. Choose a different one.'

export function slugMessage(slug: string, reserved: ReadonlySet<string>): string | null {
  if (slug === '') return 'Slug is required.'
  if (!SLUG_PATTERN.test(slug)) {
    return 'Slug must start with a letter or digit and contain only lowercase letters, digits and dashes.'
  }
  if (reserved.has(slug)) return 'This slug is reserved. Choose a different one.'
  return null
}

export function useSlugFields(reserved: ReadonlySet<string>) {
  const [name, setNameState] = useState('')
  const [rawSlug, setRawSlug] = useState('')
  const [edited, setEdited] = useState(false)
  const [taken, setTaken] = useState(false)

  const slug = edited ? rawSlug : slugify(name)
  const message = slugMessage(slug, reserved)

  return {
    name,
    slug,
    message,
    valid: message === null && name.trim() !== '',
    taken,
    setName(value: string) {
      setNameState(value)
      setTaken(false)
    },
    setSlug(value: string) {
      setEdited(true)
      setRawSlug(value)
      setTaken(false)
    },
    markTaken() {
      setTaken(true)
    },
  }
}

export type SlugFieldsState = ReturnType<typeof useSlugFields>

export function SlugFields({
  state,
  nameLabel = 'Name',
  autoFocus = false,
}: {
  state: SlugFieldsState
  nameLabel?: string
  autoFocus?: boolean
}) {
  return (
    <>
      <FormControl required>
        <FormControl.Label>{nameLabel}</FormControl.Label>
        <TextInput block autoFocus={autoFocus} value={state.name} onChange={(event) => state.setName(event.target.value)} />
      </FormControl>
      <FormControl required>
        <FormControl.Label>Slug</FormControl.Label>
        <TextInput block value={state.slug} onChange={(event) => state.setSlug(event.target.value)} />
        <FormControl.Caption>
          Used in URLs. Lowercase letters, digits and dashes; defaults to the name.
        </FormControl.Caption>
        {state.message && <FormControl.Validation variant="error">{state.message}</FormControl.Validation>}
        {state.taken && <FormControl.Validation variant="error">{SLUG_TAKEN_MESSAGE}</FormControl.Validation>}
      </FormControl>
    </>
  )
}
