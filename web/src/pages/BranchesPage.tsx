import { useState } from 'react'
import { ActionList, ActionMenu, Button, FormControl, IconButton, Label, Link as PrimerLink, Text, TextInput } from '@primer/react'
import {
  GitBranchIcon,
  GitCompareIcon,
  HistoryIcon,
  KebabHorizontalIcon,
  PencilIcon,
  SearchIcon,
  ShieldIcon,
  ShieldLockIcon,
  StarIcon,
  TrashIcon,
} from '@primer/octicons-react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { deleteBranch, listBranches, renameBranch, setBranchProtection, setDefaultBranch } from '../api/endpoints'
import type { BranchResponse } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { CopyButton } from '../components/CopyButton'
import { FormDialog } from '../components/FormDialog'
import { LoadMore } from '../components/LoadMore'
import { absoluteTime, relativeTime, shortSha } from '../components/repo/format'
import { commitUrl, commitsUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
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
      title="Rename branch"
      submitLabel="Rename branch"
      submitDisabled={next === '' || next === branch.name}
      onClose={onClose}
      onSubmit={async () => {
        await renameBranch(org, project, branch.name, next)
        onRenamed()
      }}
    >
      <FormControl required>
        <FormControl.Label>Rename this branch to:</FormControl.Label>
        <TextInput block autoFocus leadingVisual={GitBranchIcon} value={name} onChange={(event) => setName(event.target.value)} />
        <FormControl.Caption>
          Clones of <strong>{branch.name}</strong> need to switch to the new name before they update or push again.
        </FormControl.Caption>
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
  const [query, setQuery] = useState('')

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

  async function makeDefault(branch: BranchResponse) {
    const ok = await confirm({
      title: `Make ${branch.name} the default branch?`,
      content: 'New clones, merge requests and the code view start from the default branch.',
      confirmLabel: 'Make default',
    })
    if (ok) await run(() => setDefaultBranch(org, project, branch.name))
  }

  async function remove(branch: BranchResponse) {
    const ok = await confirm({
      title: `Delete branch ${branch.name}?`,
      content: `Commits reachable only from ${branch.name} can no longer be reached by name.`,
      confirmLabel: 'Delete branch',
    })
    if (ok) await run(() => deleteBranch(org, project, branch.name))
  }

  const needle = query.trim().toLowerCase()
  const matches = branches.filter((branch) => !needle || branch.name.toLowerCase().includes(needle))
  const defaults = matches.filter((branch) => branch.is_default)
  const others = matches.filter((branch) => !branch.is_default)
  const canDelete = branches.length > 1 || hasMore

  function row(branch: BranchResponse) {
    return (
      <div key={branch.id} className="nipa-list-row" style={{ alignItems: 'center' }}>
        <span className="nipa-list-row-icon" style={{ color: 'var(--fgColor-muted)' }}>
          <GitBranchIcon />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
            <PrimerLink as={Link} to={treeUrl(org, project, branch.name)} className="nipa-branch-name">
              {branch.name}
            </PrimerLink>
            <CopyButton value={branch.name} label={`Copy branch name ${branch.name}`} />
            {branch.is_default && <Label variant="accent">Default</Label>}
            {branch.is_protected && (
              <Label variant="attention">
                <ShieldLockIcon size={12} /> Protected
              </Label>
            )}
          </div>
          <div className="nipa-list-row-meta">
            {branch.commit_id ? (
              <>
                Updated <span title={absoluteTime(branch.updated_at)}>{relativeTime(branch.updated_at)}</span>
                {' · head '}
                <PrimerLink as={Link} to={commitUrl(org, project, branch.commit_id, branch.name)} className="nipa-mono-link">
                  {shortSha(branch.commit_id, 7)}
                </PrimerLink>
              </>
            ) : (
              'No commits yet'
            )}
          </div>
        </div>
        <IconButton
          as={Link}
          to={commitsUrl(org, project, branch.name)}
          icon={HistoryIcon}
          variant="invisible"
          aria-label={`Commits on ${branch.name}`}
        />
        {canAdmin && !branch.is_default && canDelete && (
          <IconButton
            icon={TrashIcon}
            variant="invisible"
            aria-label={`Delete branch ${branch.name}`}
            onClick={() => void remove(branch)}
          />
        )}
        {canAdmin && (
          <ActionMenu>
            <ActionMenu.Anchor>
              <IconButton icon={KebabHorizontalIcon} variant="invisible" aria-label={`More actions for ${branch.name}`} />
            </ActionMenu.Anchor>
            <ActionMenu.Overlay width="small" align="end">
              <ActionList>
                {!branch.is_default && (
                  <ActionList.Item onSelect={() => void makeDefault(branch)}>
                    <ActionList.LeadingVisual>
                      <StarIcon />
                    </ActionList.LeadingVisual>
                    Make default
                  </ActionList.Item>
                )}
                <ActionList.Item onSelect={() => void toggleProtection(branch)}>
                  <ActionList.LeadingVisual>
                    <ShieldIcon />
                  </ActionList.LeadingVisual>
                  {branch.is_protected ? 'Unprotect' : 'Protect'}
                </ActionList.Item>
                <ActionList.Item onSelect={() => setRenaming(branch)}>
                  <ActionList.LeadingVisual>
                    <PencilIcon />
                  </ActionList.LeadingVisual>
                  Rename
                </ActionList.Item>
              </ActionList>
            </ActionMenu.Overlay>
          </ActionMenu>
        )}
      </div>
    )
  }

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
          <Button leadingVisual={GitCompareIcon} onClick={() => navigate(`/${org}/${project}/compare`)}>
            Compare
          </Button>
          {canWrite && (
            <Button variant="primary" leadingVisual={GitBranchIcon} onClick={() => navigate(`/${org}/${project}/branches/new`)}>
              New branch
            </Button>
          )}
        </>
      }
    >
      <ErrorBanner error={actionError ?? branchesError} onDismiss={actionError ? clearError : undefined} />
      <TextInput
        block
        leadingVisual={SearchIcon}
        aria-label="Search branches"
        placeholder="Search branches…"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      {branchesLoading && <Loading />}
      {!branchesLoading && branches.length === 0 && <EmptyState>No branches yet. Push a commit to create one.</EmptyState>}
      {!branchesLoading && branches.length > 0 && matches.length === 0 && (
        <EmptyState>No branches match “{query.trim()}”.</EmptyState>
      )}
      {defaults.length > 0 && (
        <section className="nipa-list-box" aria-label="Default branch">
          <div className="nipa-list-header">
            <Text style={{ fontWeight: 600 }}>Default</Text>
          </div>
          {defaults.map(row)}
        </section>
      )}
      {others.length > 0 && (
        <section className="nipa-list-box" aria-label="Other branches">
          <div className="nipa-list-header">
            <Text style={{ fontWeight: 600 }}>
              {needle ? 'Matching branches' : 'Other branches'} <span className="nipa-count">{others.length}{hasMore ? '+' : ''}</span>
            </Text>
          </div>
          {others.map(row)}
        </section>
      )}
      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
      {renaming && (
        <RenameBranchDialog org={org} project={project} branch={renaming} onClose={() => setRenaming(null)} onRenamed={reloadAll} />
      )}
    </RepoPageShell>
  )
}
