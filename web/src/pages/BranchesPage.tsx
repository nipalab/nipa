import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useParams } from 'react-router-dom'
import {
  createBranch,
  deleteBranch,
  renameBranch,
  setBranchProtection,
  setDefaultBranch,
} from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { treeUrl } from '../components/repo/repoPaths'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'

export default function BranchesPage() {
  const { org = '', project = '' } = useParams()
  const {
    branches,
    branchesError,
    branchesLoading,
    reloadBranches,
    canWrite,
    canAdmin,
    defaultBranch,
  } = useRepoChrome(org, project)
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reloadBranches()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleCreate(event: React.FormEvent) {
    event.preventDefault()
    await run(async () => {
      await createBranch(org, project, name, from)
      setName('')
      setFrom('')
    })
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="branches"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Branches"
    >
      <ErrorBanner error={actionError ?? branchesError} />
      {branchesLoading && <Loading />}
      {!branchesLoading && branches && branches.length === 0 && <EmptyState>No branches.</EmptyState>}
      {branches && branches.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {branches.map((branch) => (
              <tr key={branch.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={{ padding: '6px 4px' }}>
                  <PrimerLink as={Link} to={treeUrl(org, project, branch.name)}>
                    {branch.name}
                  </PrimerLink>
                  {branch.is_default && <span style={{ color: 'var(--fgColor-accent)' }}> · default</span>}
                  {branch.is_protected && <span style={{ color: 'var(--fgColor-attention)' }}> · protected</span>}
                </td>
                <td style={{ padding: '6px 4px', color: 'var(--fgColor-muted)' }}>
                  <Mono>{branch.commit_id?.slice(0, 10) ?? 'empty'}</Mono>
                </td>
                <td style={{ padding: '6px 4px', textAlign: 'right' }}>
                  {canAdmin && (
                    <Stack direction="horizontal" gap="condensed" justify="end">
                      {!branch.is_default && (
                        <Button size="small" onClick={() => run(() => setDefaultBranch(org, project, branch.name))}>
                          Make default
                        </Button>
                      )}
                      <Button
                        size="small"
                        onClick={() =>
                          run(() => setBranchProtection(org, project, branch.name, !branch.is_protected))
                        }
                      >
                        {branch.is_protected ? 'Unprotect' : 'Protect'}
                      </Button>
                      <Button
                        size="small"
                        onClick={() => {
                          const next = window.prompt('New branch name', branch.name)
                          if (next && next !== branch.name) {
                            run(() => renameBranch(org, project, branch.name, next))
                          }
                        }}
                      >
                        Rename
                      </Button>
                      {!branch.is_default && (
                        <Button
                          size="small"
                          variant="danger"
                          onClick={() => {
                            if (window.confirm(`Delete branch ${branch.name}?`)) {
                              run(() => deleteBranch(org, project, branch.name))
                            }
                          }}
                        >
                          Delete
                        </Button>
                      )}
                    </Stack>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {canWrite && (
        <form
          onSubmit={handleCreate}
          style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
        >
          <Stack direction="vertical" gap="normal">
            <strong>New branch</strong>
            <FormControl required>
              <FormControl.Label>Name</FormControl.Label>
              <TextInput block value={name} onChange={(event) => setName(event.target.value)} />
            </FormControl>
            <FormControl>
              <FormControl.Label>From (branch or commit, default branch when empty)</FormControl.Label>
              <TextInput block value={from} onChange={(event) => setFrom(event.target.value)} />
            </FormControl>
            <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
              Create branch
            </Button>
          </Stack>
        </form>
      )}
    </RepoPageShell>
  )
}
