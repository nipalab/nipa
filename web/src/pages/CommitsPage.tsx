import { Link as PrimerLink } from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { getCommitDiff, listCommits } from '../api/endpoints'
import type { CommitResponse } from '../api/models'
import { DataTable, type Column } from '../components/DataTable'
import { DiffView } from '../components/repo/DiffView'
import { parentPath, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { Timestamp } from '../components/Timestamp'
import { EmptyState, ErrorBanner, Loading, Mono, SectionHeading } from '../components/ui'
import { useAsync } from '../hooks'

export default function CommitsPage() {
  const { org = '', project = '', commit } = useParams()
  const [params] = useSearchParams()
  const branch = params.get('branch') ?? ''
  const path = params.get('path') ?? ''
  const { canWrite, canAdmin, defaultBranch, branchesLoading, emptyProject } = useRepoChrome(org, project)
  const skipCommits = branchesLoading || emptyProject
  const { data: commits, error, loading } = useAsync(
    () => (skipCommits ? Promise.resolve(null) : listCommits(org, project, branch, path)),
    [org, project, branch, path, skipCommits],
  )
  const { data: diff, error: diffError, loading: diffLoading } = useAsync(
    () => (commit ? getCommitDiff(org, project, commit) : Promise.resolve(null)),
    [org, project, commit],
  )

  const rev = branch || defaultBranch
  const backTarget = treeUrl(org, project, rev, path ? parentPath(path) : '')
  const commitQuery = new URLSearchParams()
  if (branch) commitQuery.set('branch', branch)
  if (path) commitQuery.set('path', path)
  const commitQueryString = commitQuery.toString()

  const columns: Column<CommitResponse>[] = [
    {
      id: 'message',
      header: 'Commit',
      rowHeader: true,
      renderCell: (entry) => (
        <PrimerLink
          as={Link}
          to={`/${org}/${project}/commits/${entry.id}${commitQueryString ? `?${commitQueryString}` : ''}`}
        >
          {entry.message}
        </PrimerLink>
      ),
    },
    { id: 'author', header: 'Author', renderCell: (entry) => entry.author_name ?? entry.author_email ?? '' },
    { id: 'id', header: 'ID', renderCell: (entry) => <Mono>{entry.id.slice(0, 10)}</Mono> },
    { id: 'created', header: 'Date', renderCell: (entry) => <Timestamp value={entry.created_at} /> },
  ]

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="commits"
      rev={rev}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading={`Commits${branch ? ` · ${branch}` : ''}${path ? ` · ${path}` : ''}`}
    >
      {path && (
        <PrimerLink as={Link} to={backTarget}>
          Back to files
        </PrimerLink>
      )}
      {!emptyProject && <ErrorBanner error={error} />}
      {loading && <Loading />}
      {emptyProject && <EmptyState>This repository is empty. Push your first commit to get started.</EmptyState>}
      {!loading && commits && (
        <DataTable label="Commits" data={commits} columns={columns} empty="No commits yet." />
      )}

      {commit && (
        <>
          <SectionHeading>
            Diff of <Mono>{commit}</Mono>
          </SectionHeading>
          <ErrorBanner error={diffError} />
          {diffLoading && <Loading />}
          {diff && <DiffView files={diff.files} />}
        </>
      )}
    </RepoPageShell>
  )
}
