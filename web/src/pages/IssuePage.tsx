import { useState } from 'react'
import { Button, Label, Link as PrimerLink, Text } from '@primer/react'
import { GitBranchIcon, GitCommitIcon, GitPullRequestIcon, IssueOpenedIcon, LinkExternalIcon } from '@primer/octicons-react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../api/client'
import { listIssueLinks } from '../api/endpoints'
import type { IssueLinkResponse } from '../api/models'
import { LoadMore } from '../components/LoadMore'
import { StateBadge } from '../components/StateBadge'
import { Timestamp } from '../components/Timestamp'
import { IssueStatusLabel, useIssues } from '../components/repo/IssueChips'
import { shortSha } from '../components/repo/format'
import { commitUrl, repoUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { usePaged } from '../hooks'

type Unavailable = 'not_configured' | 'off' | null

function LinkRow({ org, project, link }: { org: string; project: string; link: IssueLinkResponse }) {
  const closes = link.relation === 'closes' && <Label variant="done">Closes</Label>
  if (link.merge_request) {
    const mr = link.merge_request
    return (
      <div className="nipa-list-row" style={{ alignItems: 'center' }}>
        <span className="nipa-list-row-icon">
          <GitPullRequestIcon />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <PrimerLink as={Link} to={`${repoUrl(org, project)}/merges/${mr.number}`} className="nipa-list-row-title">
            {mr.title}
          </PrimerLink>
          <div className="nipa-list-row-meta">
            Merge request #{mr.number} · linked <Timestamp value={link.created_at} />
          </div>
        </div>
        {closes}
        <StateBadge status={mr.state} />
      </div>
    )
  }
  if (link.commit) {
    const commit = link.commit
    return (
      <div className="nipa-list-row" style={{ alignItems: 'center' }}>
        <span className="nipa-list-row-icon">
          <GitCommitIcon />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <PrimerLink as={Link} to={commitUrl(org, project, commit.id)} className="nipa-list-row-title">
            {commit.title || shortSha(commit.id, 7)}
          </PrimerLink>
          <div className="nipa-list-row-meta">
            Commit <span className="nipa-mono-link">{shortSha(commit.id, 7)}</span> · committed{' '}
            <Timestamp value={commit.created_at} />
          </div>
        </div>
        {closes}
      </div>
    )
  }
  if (link.branch) {
    return (
      <div className="nipa-list-row" style={{ alignItems: 'center' }}>
        <span className="nipa-list-row-icon">
          <GitBranchIcon />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <PrimerLink as={Link} to={treeUrl(org, project, link.branch.name)} className="nipa-branch-name">
            {link.branch.name}
          </PrimerLink>
          <div className="nipa-list-row-meta">
            Branch · linked <Timestamp value={link.created_at} />
          </div>
        </div>
      </div>
    )
  }
  return null
}

// IssuePage lists the merge requests, commits and branches linked to one
// issue of the project's tracker, newest first.
export default function IssuePage() {
  const { org = '', project = '', key = '' } = useParams()
  const { canAdmin, defaultBranch } = useRepoChrome(org, project)
  const [unavailable, setUnavailable] = useState<Unavailable>(null)
  const [trackerUrl, setTrackerUrl] = useState('')
  const issues = useIssues(org, project, unavailable ? [] : [key])
  const issue = issues.get(key)
  const { items, loading, error, hasMore, loadingMore, loadMore } = usePaged<IssueLinkResponse, string>(
    async (after) => {
      try {
        const page = await listIssueLinks(org, project, key, after)
        setUnavailable(null)
        setTrackerUrl(page.url)
        return { items: page.links, next: page.next_cursor || undefined }
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) setUnavailable('not_configured')
        else if (err instanceof ApiError && err.status === 503) setUnavailable('off')
        throw err
      }
    },
    [org, project, key],
  )

  return (
    <RepoPageShell
      org={org}
      project={project}
      rev={defaultBranch}
      canAdmin={canAdmin}
      crumbs={[{ label: 'Issues' }, { label: key }]}
      heading={
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <IssueOpenedIcon />
          <span className="nipa-issue-key">{key}</span>
          {issue?.state === 'ok' && issue.title && <span>{issue.title}</span>}
          <IssueStatusLabel issue={issue} />
        </span>
      }
      actions={
        trackerUrl && (
          <Button as="a" href={trackerUrl} target="_blank" rel="noopener noreferrer" leadingVisual={LinkExternalIcon}>
            Open in issue tracker
          </Button>
        )
      }
    >
      {issue?.state === 'ok' && (issue.type || issue.assignee) && (
        <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
          {[issue.type, issue.assignee && `Assignee: ${issue.assignee}`].filter(Boolean).join(' · ')}
        </Text>
      )}
      {unavailable === 'not_configured' && (
        <EmptyState icon={IssueOpenedIcon} description="A project admin can connect one under Settings → Issue tracker.">
          This project does not use an issue tracker
        </EmptyState>
      )}
      {unavailable === 'off' && (
        <EmptyState icon={IssueOpenedIcon} description="The server has no integrations encryption key.">
          The issue tracker integration is off
        </EmptyState>
      )}
      {!unavailable && <ErrorBanner error={error} />}
      {loading && <Loading />}
      {!loading && !error && items.length === 0 && (
        <EmptyState icon={IssueOpenedIcon} description="Mention the key in a commit message, a branch name or a merge request.">
          Nothing is linked to {key} yet
        </EmptyState>
      )}
      {items.length > 0 && (
        <section className="nipa-list-box" aria-label={`Work linked to ${key}`}>
          <div className="nipa-list-header">
            <Text style={{ fontWeight: 600 }}>
              Linked work <span className="nipa-count">{items.length}{hasMore ? '+' : ''}</span>
            </Text>
          </div>
          {items.map((link) => (
            <LinkRow key={link.id} org={org} project={project} link={link} />
          ))}
        </section>
      )}
      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
    </RepoPageShell>
  )
}
