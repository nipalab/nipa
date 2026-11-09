import { Button, Link as PrimerLink, Text } from '@primer/react'
import { GitBranchIcon, SearchIcon, TagIcon } from '@primer/octicons-react'
import { useMemo, useState } from 'react'
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { getTree } from '../api/endpoints'
import type { TreeEntryResponse } from '../api/models'
import { BranchSelector } from '../components/repo/BranchSelector'
import { CloneButton, cloneUrl } from '../components/repo/CloneButton'
import { CommitBar } from '../components/repo/CommitBar'
import { GoToFileDialog } from '../components/repo/GoToFileDialog'
import { Readme } from '../components/repo/Readme'
import { RepoBreadcrumb } from '../components/repo/RepoBreadcrumb'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { RepoTree } from '../components/repo/RepoTree'
import { repoUrl, treeUrl } from '../components/repo/repoPaths'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { useTags } from '../components/repo/useTags'
import { CopyButton } from '../components/CopyButton'
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

// EmptyRepository is GitHub's quick setup box for a project without commits.
function EmptyRepository({ org, project }: { org: string; project: string }) {
  const url = cloneUrl(org, project)
  const steps = [`nipa clone ${url} ${project}`, `cd ${project}`, 'nipa add .', 'nipa push -m "Initial commit"']
  return (
    <div className="nipa-list-box">
      <div className="nipa-list-header">
        <strong>Quick setup</strong>
      </div>
      <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
        <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
          This repository is empty. Clone it, add your files and push the first commit:
        </Text>
        <div className="nipa-clone-field nipa-setup-steps">
          <pre>{steps.join('\n')}</pre>
          <CopyButton value={steps.join('\n')} label="Copy setup commands" />
        </div>
      </div>
    </div>
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
  const { branches, branchesLoading, emptyProject, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { tags, load: loadTags } = useTags(org, project)

  const legacyRev = searchParams.get('rev') ?? ''
  const path = splat || searchParams.get('path') || ''
  const rev = routeRev || legacyRev
  const skipTree = branchesLoading || emptyProject

  const { data: tree, error, loading } = useAsync(
    () => (skipTree ? Promise.resolve(null) : getTree(org, project, rev, path, { history: true })),
    [org, project, rev, path, skipTree],
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
    >
      <div className="nipa-code-toolbar">
        <div className="nipa-code-toolbar-left">
          <BranchSelector
            branches={branches}
            rev={linkRev}
            onSelect={(nextRev) => navigate(treeUrl(org, project, nextRev, path))}
            tags={tags}
            onOpen={loadTags}
            onSelectTag={(tag) => navigate(treeUrl(org, project, tag.commit_id, path))}
          />
          {path ? (
            <RepoBreadcrumb org={org} project={project} rev={linkRev} path={path} />
          ) : (
            <>
              <PrimerLink as={Link} to={`${repoUrl(org, project)}/branches`} className="nipa-code-count">
                <GitBranchIcon /> <strong>{branches?.length ?? 0}</strong> {branches?.length === 1 ? 'branch' : 'branches'}
              </PrimerLink>
              <PrimerLink as={Link} to={`${repoUrl(org, project)}/tags`} className="nipa-code-count">
                <TagIcon /> Tags
              </PrimerLink>
            </>
          )}
        </div>
        <div className="nipa-code-toolbar-right">
          <Button leadingVisual={SearchIcon} onClick={() => setFindOpen(true)}>
            Go to file
          </Button>
          {!emptyProject && <CloneButton org={org} project={project} />}
        </div>
      </div>

      {!emptyProject && <ErrorBanner error={error} />}
      {loading && <Loading />}
      {emptyProject && <EmptyRepository org={org} project={project} />}
      {!loading && tree && tree.entries.length === 0 && (
        <EmptyState>{emptyRepo ? 'This branch has no commits yet.' : 'This directory is empty.'}</EmptyState>
      )}
      {!loading && tree && tree.entries.length > 0 && (
        <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
          {tree.latest_commit && (
            <CommitBar org={org} project={project} rev={linkRev} path={path} commit={tree.latest_commit} />
          )}
          <RepoTree org={org} project={project} rev={linkRev} path={path} entries={tree.entries} />
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
