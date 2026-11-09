import { useState } from 'react'
import { Button, FormControl, Label, Link as PrimerLink, Stack, TextInput } from '@primer/react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { deleteBranch, listBranches, renameBranch, setBranchProtection, setDefaultBranch } from '../api/endpoints'
import type { BranchResponse } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { DataTable, type Column } from '../components/DataTable'
import { FormDialog } from '../components/FormDialog'
import { LoadMore } from '../components/LoadMore'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { treeUrl } from '../components/repo/repoPaths'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { ErrorBanner, Mono } from '../components/ui'
import { sliceCursorPage, useAction, usePaged } from '../hooks'

const PAGE_SIZE = 50

function RenameBranchDialog({
  org,
  project,
  branch,
  onClose,
  onRenamed,
}: {
  org: string
  project: string
  branch: BranchResponse
  onClose: () => void
  onRenamed: () => void
}) {
  const [name, setName] = useState(branch.name)
  const next = name.trim()
  return (
    <FormDialog
      title={`Rename ${branch.name}`}
      submitLabel="Rename"
      submitDisabled={next === '' || next === branch.name}
      onClose={onClose}
      onSubmit={async () => {
        await renameBranch(org, project, branch.name, next)
        onRenamed()
      }}
    >
      <FormControl required>
        <FormControl.Label>New branch name</FormControl.Label>
        <TextInput block autoFocus value={name} onChange={(event) => setName(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

export default function BranchesPage() {
  const { org = '', project = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirmAction()
  const { reloadBranches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const {
    items: branches,
    loading: branchesLoading,
    error: branchesError,
    hasMore,
    loadingMore,
    loadMore,
    reload,
  } = usePaged<BranchResponse, BranchResponse>(
    async (after) =>
      sliceCursorPage(await listBranches(org, project, { limit: PAGE_SIZE + 1, after }), PAGE_SIZE, (rows) => rows[PAGE_SIZE - 1]),
    [org, project],
  )
  const reloadAll = () => {
    reload()
    reloadBranches()
  }
  const { run, error: actionError, clearError } = useAction(reloadAll)
  const [renaming, setRenaming] = useState<BranchResponse | null>(null)

  async function toggleProtection(branch: BranchResponse) {
    if (branch.is_protected) {
      const ok = await confirm({
        title: `Unprotect ${branch.name}?`,
        content: 'Anyone with write access will be able to push to and delete this branch.',
        confirmLabel: 'Unprotect',
      })
      if (!ok) return
    }
    await run(() => setBranchProtection(org, project, branch.name, !branch.is_protected))
  }

  async function remove(branch: BranchResponse) {
    const ok = await confirm({
      title: `Delete branch ${branch.name}`,
      content: `This deletes the branch ${branch.name}. Commits reachable only from this branch become inaccessible.`,
      confirmLabel: 'Delete branch',
    })
    if (ok) await run(() => deleteBranch(org, project, branch.name))
  }

  const columns: Column<BranchResponse>[] = [
    {
      id: 'name',
      header: 'Branch',
      rowHeader: true,
      renderCell: (branch) => (
        <Stack direction="horizontal" gap="condensed" align="center">
          <PrimerLink as={Link} to={treeUrl(org, project, branch.name)}>
            {branch.name}
          </PrimerLink>
          {branch.is_default && <Label variant="accent">default</Label>}
          {branch.is_protected && <Label variant="attention">protected</Label>}
        </Stack>
      ),
    },
    {
      id: 'commit',
      header: 'Head',
      renderCell: (branch) => <Mono>{branch.commit_id?.slice(0, 10) ?? 'empty'}</Mono>,
    },
    ...(canAdmin
      ? [
          {
            id: 'actions',
            header: 'Actions',
            align: 'end' as const,
            renderCell: (branch: BranchResponse) => (
              <Stack direction="horizontal" gap="condensed" justify="end">
                {!branch.is_default && (
                  <Button size="small" onClick={() => void run(() => setDefaultBranch(org, project, branch.name))}>
                    Make default
                  </Button>
                )}
                <Button size="small" onClick={() => void toggleProtection(branch)}>
                  {branch.is_protected ? 'Unprotect' : 'Protect'}
                </Button>
                <Button size="small" onClick={() => setRenaming(branch)}>
                  Rename
                </Button>
                {!branch.is_default && (branches.length > 1 || hasMore) && (
                  <Button size="small" variant="danger" onClick={() => void remove(branch)}>
                    Delete
                  </Button>
                )}
              </Stack>
            ),
          },
        ]
      : []),
  ]

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="branches"
      rev={defaultBranch}
      canAdmin={canAdmin}
      heading="Branches"
      actions={
        <>
          <Button onClick={() => navigate(`/${org}/${project}/compare`)}>Compare</Button>
          {canWrite && (
            <Button variant="primary" onClick={() => navigate(`/${org}/${project}/branches/new`)}>
              Add branch
            </Button>
          )}
        </>
      }
    >
      <ErrorBanner error={actionError ?? branchesError} onDismiss={actionError ? clearError : undefined} />
      <DataTable
        label="Branches"
        loading={branchesLoading}
        data={branches}
        columns={columns}
        empty="No branches."
      />
      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
      {renaming && (
        <RenameBranchDialog
          org={org}
          project={project}
          branch={renaming}
          onClose={() => setRenaming(null)}
          onRenamed={reloadAll}
        />
      )}
    </RepoPageShell>
  )
}
