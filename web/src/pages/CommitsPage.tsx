import { Button, IconButton, Link as PrimerLink, Text } from '@primer/react'
import { ArrowLeftIcon, CodeIcon, FileCodeIcon, GitCommitIcon } from '@primer/octicons-react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { getCommitDiff, listCommits } from '../api/endpoints'
import type { CommitResponse } from '../api/models'
import { ActorAvatar } from '../components/ActorAvatar'
import { CopyButton } from '../components/CopyButton'
import { LoadMore } from '../components/LoadMore'
import { BranchSelector } from '../components/repo/BranchSelector'
import { DiffView } from '../components/repo/DiffView'
import { absoluteTime, commitTitle, relativeTime, shortSha } from '../components/repo/format'
import { commitUrl, commitsUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'
import { sliceCursorPage, useAsync, usePaged } from '../hooks'

const PAGE_SIZE = 50

function authorOf(commit: CommitResponse): string {
  return commit.author_name || commit.author_email || 'unknown'
}

function commitBody(message: string): string {
  const lines = message.split('\n')
  return lines.slice(1).join('\n').trim()
}

export function dayKey(iso: string): string {
  const date = new Date(iso)
  return Number.isNaN(date.getTime())
    ? 'Unknown date'
    : date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

export function groupByDay(commits: CommitResponse[]): { day: string; commits: CommitResponse[] }[] {
  const groups: { day: string; commits: CommitResponse[] }[] = []
  for (const commit of commits) {
    const day = dayKey(commit.created_at)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.commits.push(commit)
    else groups.push({ day, commits: [commit] })
  }
  return groups
}

function CommitDetail({ org, project, id, branch }: { org: string; project: string; id: string; branch: string }) {
  const { data: entries, error: commitError } = useAsync(
    () => listCommits(org, project, branch, '', { start: id, limit: 1 }),
    [org, project, id, branch],
  )
  const { data: diff, error: diffError, loading: diffLoading } = useAsync(() => getCommitDiff(org, project, id), [org, project, id])
  const commit = entries?.[0]
  const body = commit ? commitBody(commit.message) : ''
  const parents = commit ? [commit.parent_1_id, commit.parent_2_id].filter((parent): parent is string => Boolean(parent)) : []
  const files = diff?.files ?? []
  const additions = files.reduce((total, file) => total + file.additions, 0)
  const deletions = files.reduce((total, file) => total + file.deletions, 0)

  return (
    <>
      <PrimerLink as={Link} to={commitsUrl(org, project, branch)} className="nipa-back-link">
        <ArrowLeftIcon /> Commits
      </PrimerLink>
      <ErrorBanner error={commitError ?? diffError} />
      <div className="nipa-list-box">
        <div className="nipa-commit-header">
          <div style={{ minWidth: 0, flex: 1 }}>
            <Text as="h2" className="nipa-commit-title">
              {commit ? commitTitle(commit.message) : <Mono>{shortSha(id, 10)}</Mono>}
            </Text>
            {body && <pre className="nipa-commit-body">{body}</pre>}
          </div>
          <Button as={Link} to={treeUrl(org, project, id)} leadingVisual={FileCodeIcon}>
            Browse files
          </Button>
        </div>
        <div className="nipa-commit-meta">
          {commit && (
            <span className="nipa-commit-author">
              <ActorAvatar actor={{ user_id: commit.author_email ?? authorOf(commit), name: authorOf(commit) }} size={20} />
              <strong>{authorOf(commit)}</strong> committed{' '}
              <span title={absoluteTime(commit.created_at)}>{relativeTime(commit.created_at)}</span>
            </span>
          )}
          <span className="nipa-commit-ids">
            {parents.length > 0 && (
              <span>
                {parents.length === 1 ? '1 parent' : `${parents.length} parents`}{' '}
                {parents.map((parent, index) => (
                  <span key={parent}>
                    {index > 0 && ' + '}
                    <PrimerLink as={Link} to={commitUrl(org, project, parent, branch)} className="nipa-mono-link">
                      {shortSha(parent, 7)}
                    </PrimerLink>
                  </span>
                ))}
              </span>
            )}
            <span>
              commit <Mono>{id}</Mono>
              <CopyButton value={id} label="Copy full commit ID" />
            </span>
          </span>
        </div>
      </div>
      {diffLoading && <Loading />}
      {diff && (
        <>
          <Text as="p" className="nipa-diff-summary">
            Showing <strong>{files.length} changed {files.length === 1 ? 'file' : 'files'}</strong> with{' '}
            <span style={{ color: 'var(--fgColor-success)' }}>{additions} additions</span> and{' '}
            <span style={{ color: 'var(--fgColor-danger)' }}>{deletions} deletions</span>.
          </Text>
          <DiffView files={files} />
        </>
      )}
    </>
  )
}

export default function CommitsPage() {
  const { org = '', project = '', commit } = useParams()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const branch = params.get('branch') ?? ''
  const path = params.get('path') ?? ''
  const { branches, canAdmin, defaultBranch, branchesLoading, emptyProject } = useRepoChrome(org, project)
  const skipCommits = branchesLoading || emptyProject || Boolean(commit)
  const {
    items: commits,
    error,
    loading,
    hasMore,
    loadingMore,
    loadMore,
  } = usePaged<CommitResponse, string>(
    async (start) =>
      skipCommits
        ? { items: [] }
        : sliceCursorPage(
            await listCommits(org, project, branch, path, { start, limit: PAGE_SIZE + 1 }),
            PAGE_SIZE,
            (rows) => rows[PAGE_SIZE].id,
          ),
    [org, project, branch, path, skipCommits],
  )

  const rev = branch || defaultBranch

  return (
    <RepoPageShell org={org} project={project} active="commits" rev={rev} wide={Boolean(commit)} canAdmin={canAdmin}>
      {commit ? (
        <CommitDetail org={org} project={project} id={commit} branch={branch} />
      ) : (
        <>
          <div className="nipa-code-toolbar">
            <div className="nipa-code-toolbar-left">
              <BranchSelector branches={branches} rev={rev} onSelect={(next) => navigate(commitsUrl(org, project, next, path))} />
              {path && (
                <Text style={{ color: 'var(--fgColor-muted)' }}>
                  History for <Mono>{path}</Mono>{' '}
                  <PrimerLink as={Link} to={commitsUrl(org, project, branch)}>
                    (show all)
                  </PrimerLink>
                </Text>
              )}
            </div>
          </div>
          {!emptyProject && <ErrorBanner error={error} />}
          {loading && <Loading />}
          {emptyProject && <EmptyState>This repository is empty. Push your first commit to get started.</EmptyState>}
          {!loading && !emptyProject && commits.length === 0 && <EmptyState>No commits yet.</EmptyState>}
          {groupByDay(commits).map((group) => (
            <section key={group.day} className="nipa-commit-group" aria-label={`Commits on ${group.day}`}>
              <h3 className="nipa-commit-day">
                <GitCommitIcon /> Commits on {group.day}
              </h3>
              <div className="nipa-list-box">
                {group.commits.map((entry) => (
                  <div key={entry.id} className="nipa-list-row" style={{ alignItems: 'center' }}>
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <PrimerLink
                        as={Link}
                        to={commitUrl(org, project, entry.id, branch)}
                        className="nipa-list-row-title"
                        style={{ fontSize: 14 }}
                        title={entry.message}
                      >
                        {commitTitle(entry.message)}
                      </PrimerLink>
                      <div className="nipa-list-row-meta nipa-commit-row-meta">
                        <ActorAvatar actor={{ user_id: entry.author_email ?? authorOf(entry), name: authorOf(entry) }} size={16} />
                        <strong>{authorOf(entry)}</strong> committed{' '}
                        <span title={absoluteTime(entry.created_at)}>{relativeTime(entry.created_at)}</span>
                      </div>
                    </div>
                    <span className="nipa-sha-group">
                      <PrimerLink as={Link} to={commitUrl(org, project, entry.id, branch)} className="nipa-mono-link">
                        {shortSha(entry.id, 7)}
                      </PrimerLink>
                      <CopyButton value={entry.id} label={`Copy commit ID ${shortSha(entry.id, 7)}`} />
                    </span>
                    <IconButton
                      as={Link}
                      to={treeUrl(org, project, entry.id)}
                      icon={CodeIcon}
                      variant="invisible"
                      size="small"
                      aria-label={`Browse the repository at ${shortSha(entry.id, 7)}`}
                    />
                  </div>
                ))}
              </div>
            </section>
          ))}
          <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
        </>
      )}
    </RepoPageShell>
  )
}
