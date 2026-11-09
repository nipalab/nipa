import { useState } from 'react'
import { Button, FormControl, Stack, TextInput } from '@primer/react'
import { GitBranchIcon } from '@primer/octicons-react'
import { useNavigate, useParams } from 'react-router-dom'
import { createBranch } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { SourcePicker } from '../components/repo/SourcePicker'
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
  const { branches, permissionsLoading, branchesLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
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
          <FormControl.Label>New branch name</FormControl.Label>
          <TextInput
            block
            autoFocus
            leadingVisual={GitBranchIcon}
            placeholder="e.g. feature-new-level"
            value={name}
            validationStatus={name !== '' && nameError ? 'error' : undefined}
            onChange={(event) => setName(event.target.value)}
          />
          {name !== '' && nameError ? (
            <FormControl.Validation variant="error">{nameError}</FormControl.Validation>
          ) : (
            <FormControl.Caption>No spaces or slashes.</FormControl.Caption>
          )}
        </FormControl>
        <SourcePicker label="Source" branches={branches ?? []} defaultBranch={defaultBranch} onChange={setFrom} />
        <Stack direction="horizontal" gap="condensed">
          <Button type="submit" variant="primary" disabled={!canSubmit} loading={saving}>
            Create branch
          </Button>
          <Button onClick={() => navigate(`/${org}/${project}/branches`)} disabled={saving}>
            Cancel
          </Button>
        </Stack>
      </FormCard>
    </RepoPageShell>
  )
}
