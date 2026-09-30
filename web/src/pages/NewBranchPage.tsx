import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { createBranch } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading } from '../components/ui'

function branchNameError(name: string): string | null {
  const trimmed = name.trim()
  if (!trimmed) return 'branch name must not be empty'
  if (trimmed.includes('/')) return 'branch names cannot contain "/"'
  if (trimmed === '.' || trimmed === '..') return `invalid branch name "${trimmed}"`
  for (const char of trimmed) {
    const code = char.codePointAt(0) ?? 0
    if (code <= 0x20 || code === 0x7f) return `invalid branch name "${trimmed}"`
  }
  return null
}

export default function NewBranchPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const { branchesLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const nameError = branchNameError(name)
  const canSubmit = nameError === null && !saving
  const backLink = (
    <PrimerLink as={Link} to={`/${org}/${project}/branches`}>
      back to branches
    </PrimerLink>
  )

  if (branchesLoading) {
    return (
      <RepoPageShell
        org={org}
        project={project}
        active="branches"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="New branch"
      >
        <Loading />
      </RepoPageShell>
    )
  }

  if (!canWrite) {
    return (
      <RepoPageShell
        org={org}
        project={project}
        active="branches"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="New branch"
      >
        <Text>You need project write permission to create branches.</Text>
        {backLink}
      </RepoPageShell>
    )
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    setSaving(true)
    setActionError(null)
    try {
      await createBranch(org, project, name.trim(), from.trim())
      navigate(`/${org}/${project}/branches`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
      setSaving(false)
    }
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="branches"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="New branch"
      actions={backLink}
    >
      <ErrorBanner error={actionError} />
      <form
        onSubmit={handleSubmit}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <FormControl required>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
            {name !== '' && nameError && (
              <FormControl.Validation variant="error">{nameError}</FormControl.Validation>
            )}
          </FormControl>
          <FormControl>
            <FormControl.Label>From (optional)</FormControl.Label>
            <TextInput block value={from} onChange={(event) => setFrom(event.target.value)} />
            <FormControl.Caption>
              Branch name or commit id. Forks from the default branch when empty.
            </FormControl.Caption>
          </FormControl>
          <Button
            type="submit"
            variant="primary"
            disabled={!canSubmit}
            loading={saving}
            style={{ alignSelf: 'flex-start' }}
          >
            Create branch
          </Button>
        </Stack>
      </form>
    </RepoPageShell>
  )
}
