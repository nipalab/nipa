import { useState } from 'react'
import { Button, FormControl, TextInput } from '@primer/react'
import { useNavigate, useParams } from 'react-router-dom'
import { createBranch } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { FormCard } from '../components/SettingsCard'
import { ErrorBanner, NoPermission } from '../components/ui'
import { errorMessage } from '../hooks'

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
  const { permissionsLoading, branchesLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const nameError = branchNameError(name)
  const canSubmit = nameError === null && !saving
  const shell = {
    org,
    project,
    active: 'branches' as const,
    rev: defaultBranch,
    canAdmin,
    canWrite,
    heading: 'New branch',
    crumbs: [{ label: 'Branches', to: `/${org}/${project}/branches` }, { label: 'New branch' }],
  }

  if (branchesLoading || permissionsLoading || !canWrite) {
    return (
      <RepoPageShell {...shell}>
        <NoPermission loading={branchesLoading || permissionsLoading}>
          You need project write permission to create branches.
        </NoPermission>
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
      setActionError(errorMessage(err))
      setSaving(false)
    }
  }

  return (
    <RepoPageShell {...shell}>
      <ErrorBanner error={actionError} />
      <FormCard onSubmit={handleSubmit}>
        <FormControl required>
          <FormControl.Label>Name</FormControl.Label>
          <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
          {name !== '' && nameError && <FormControl.Validation variant="error">{nameError}</FormControl.Validation>}
        </FormControl>
        <FormControl>
          <FormControl.Label>From (optional)</FormControl.Label>
          <TextInput block value={from} onChange={(event) => setFrom(event.target.value)} />
          <FormControl.Caption>Branch name or commit id. Forks from the default branch when empty.</FormControl.Caption>
        </FormControl>
        <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving} style={{ alignSelf: 'flex-start' }}>
          Create branch
        </Button>
      </FormCard>
    </RepoPageShell>
  )
}
