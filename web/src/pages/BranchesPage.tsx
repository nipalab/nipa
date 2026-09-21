import { useState } from 'react'
import { Button, FormControl, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useParams } from 'react-router-dom'
import {
  createBranch,
  deleteBranch,
  getMyProjectPermissions,
  listBranches,
  renameBranch,
  setBranchProtection,
  setDefaultBranch,
} from '../api/endpoints'
import { useAuth } from '../auth'
import { EmptyState, ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'
import { PERMISSION_ADMIN, PERMISSION_WRITE } from '../api/models'

export default function BranchesPage() {
  const { org = '', project = '' } = useParams()
  const { me } = useAuth()
  const { data: branches, error, loading, reload } = useAsync(() => listBranches(org, project), [org, project])
  const { data: permissions } = useAsync(() => getMyProjectPermissions(org, project), [org, project])
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [actionError, setActionError] = useState<string | null>(null)

  const canWrite = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_WRITE) !== 0,
  )
  const canAdmin = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_ADMIN) !== 0,
  )

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
      reload()
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
    <Page
      title="Branches"
      subtitle={`${org}/${project}`}
      actions={<PrimerLink as={Link} to={`/${org}/${project}`}>back to files</PrimerLink>}
    >
      <ErrorBanner error={actionError ?? error} />
      {loading && <Loading />}
      {!loading && branches && branches.length === 0 && <EmptyState>No branches.</EmptyState>}
      {branches && branches.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {branches.map((branch) => (
              <tr key={branch.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={{ padding: '6px 4px' }}>
                  <PrimerLink as={Link} to={`/${org}/${project}?rev=${encodeURIComponent(branch.name)}`}>
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
    </Page>
  )
}
