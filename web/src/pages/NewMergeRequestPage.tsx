import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, Text, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { createMergeRequest } from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Loading } from '../components/ui'

export default function NewMergeRequestPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const { branches, branchesLoading, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [saving, setSaving] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const canSubmit = title.trim() !== '' && source !== '' && target !== '' && !saving
  const backLink = (
    <PrimerLink as={Link} to={`/${org}/${project}/merges`}>
      back to merge requests
    </PrimerLink>
  )

  if (branchesLoading) {
    return (
      <RepoPageShell
        org={org}
        project={project}
        active="merges"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="New merge request"
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
        active="merges"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="New merge request"
      >
        <Text>You need project write permission to create merge requests.</Text>
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
      const request = await createMergeRequest(org, project, title.trim(), description, source, target)
      navigate(`/${org}/${project}/merges/${request.number}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
      setSaving(false)
    }
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="merges"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="New merge request"
      actions={backLink}
    >
      <ErrorBanner error={actionError} />
      <form
        onSubmit={handleSubmit}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <FormControl required>
            <FormControl.Label>Title</FormControl.Label>
            <TextInput block autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Description</FormControl.Label>
            <TextInput block value={description} onChange={(event) => setDescription(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Source branch</FormControl.Label>
            <select
              value={source}
              onChange={(event) => setSource(event.target.value)}
              style={{ padding: 6 }}
            >
              <option value="">select a branch</option>
              {branches?.map((branch) => (
                <option key={branch.id} value={branch.name}>
                  {branch.name}
                </option>
              ))}
            </select>
          </FormControl>
          <FormControl required>
            <FormControl.Label>Target branch</FormControl.Label>
            <select
              value={target}
              onChange={(event) => setTarget(event.target.value)}
              style={{ padding: 6 }}
            >
              <option value="">select a branch</option>
              {branches?.map((branch) => (
                <option key={branch.id} value={branch.name}>
                  {branch.name}
                  {branch.is_default ? ' (default)' : ''}
                </option>
              ))}
            </select>
          </FormControl>
          <Button
            type="submit"
            variant="primary"
            disabled={!canSubmit}
            loading={saving}
            style={{ alignSelf: 'flex-start' }}
          >
            Create merge request
          </Button>
        </Stack>
      </form>
    </RepoPageShell>
  )
}
