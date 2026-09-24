import { Button, Stack } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { useMemo, useState } from 'react'
import { Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { getTree } from '../api/endpoints'
import type { TreeEntryResponse } from '../api/models'
import { BranchSelector } from '../components/repo/BranchSelector'
import { CommitBar } from '../components/repo/CommitBar'
import { GoToFileDialog } from '../components/repo/GoToFileDialog'
import { Readme } from '../components/repo/Readme'
import { RepoBreadcrumb } from '../components/repo/RepoBreadcrumb'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { RepoTree } from '../components/repo/RepoTree'
import { treeUrl } from '../components/repo/repoPaths'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { useAsync } from '../hooks'

function findReadme(entries: TreeEntryResponse[]): TreeEntryResponse | undefined {
  const candidates = entries.filter(
    (entry) => entry.type === 'file' && /^readme(\.[a-z]+)?$/i.test(entry.name),
  )
  if (candidates.length === 0) return undefined
  return (
    candidates.find((entry) => entry.name.toLowerCase() === 'readme.md') ??
    candidates.find((entry) => /\.(md|markdown)$/i.test(entry.name)) ??
    candidates[0]
  )
}

export default function RepoPage() {
  const { org = '', project = '' } = useParams()
  const params = useParams()
  const routeRev = params.rev ?? ''
  const splat = params['*'] ?? ''
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const [findOpen, setFindOpen] = useState(false)
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)

  const legacyRev = searchParams.get('rev') ?? ''
  const path = splat || searchParams.get('path') || ''
  const rev = routeRev || legacyRev

  const { data: tree, error, loading } = useAsync(
    () => getTree(org, project, rev, path, { history: true }),
    [org, project, rev, path],
  )

  const readme = useMemo(() => (tree ? findReadme(tree.entries) : undefined), [tree])
  const linkRev = rev || defaultBranch
  const currentBranch = branches?.find((branch) => (rev ? branch.name === rev : branch.is_default))
  const emptyRepo = Boolean(currentBranch && !currentBranch.commit_id)

  if (legacyRev) {
    return <Navigate replace to={treeUrl(org, project, legacyRev, path)} />
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="code"
      rev={linkRev}
      canAdmin={canAdmin}
      canWrite={canWrite}
    >
      <Stack direction="horizontal" gap="normal" align="center" justify="space-between">
        <Stack direction="horizontal" gap="normal" align="center" style={{ minWidth: 0 }}>
          <BranchSelector
            branches={branches}
            rev={linkRev}
            onSelect={(nextRev) => navigate(treeUrl(org, project, nextRev, path))}
          />
          {path && <RepoBreadcrumb org={org} project={project} rev={linkRev} path={path} />}
        </Stack>
        <Button leadingVisual={SearchIcon} onClick={() => setFindOpen(true)}>
          Go to file
        </Button>
      </Stack>

      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && tree && tree.entries.length === 0 && (
        <EmptyState>{emptyRepo ? 'This branch has no commits yet.' : 'This directory is empty.'}</EmptyState>
      )}
      {!loading && tree && tree.entries.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          {tree.latest_commit && (
            <CommitBar org={org} project={project} rev={linkRev} path={path} commit={tree.latest_commit} />
          )}
          <RepoTree org={org} project={project} rev={linkRev} entries={tree.entries} />
        </div>
      )}
      {readme && <Readme org={org} project={project} rev={linkRev} entry={readme} />}

      <GoToFileDialog
        org={org}
        project={project}
        rev={linkRev}
        path={path}
        open={findOpen}
        onClose={() => setFindOpen(false)}
      />
    </RepoPageShell>
  )
}
