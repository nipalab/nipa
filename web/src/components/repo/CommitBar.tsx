import { Link as PrimerLink, Text } from '@primer/react'
import { HistoryIcon } from '@primer/octicons-react'
import { Link } from 'react-router-dom'
import type { CommitResponse } from '../../api/models'
import { ActorAvatar } from '../ActorAvatar'
import { absoluteTime, commitTitle, relativeTime, shortSha } from './format'
import { commitUrl, commitsUrl } from './repoPaths'

export function CommitBar({
  org,
  project,
  rev,
  path = '',
  commit,
}: {
  org: string
  project: string
  rev: string
  path?: string
  commit: CommitResponse
}) {
  const author = commit.author_name || commit.author_email || 'unknown'
  return (
    <div className="nipa-commit-bar">
      <ActorAvatar actor={{ user_id: commit.author_email ?? author, name: author }} size={24} />
      <strong style={{ flexShrink: 0 }}>{author}</strong>
      <PrimerLink
        as={Link}
        to={commitUrl(org, project, commit.id, rev)}
        title={commit.message}
        className="nipa-commit-bar-message"
      >
        {commitTitle(commit.message)}
      </PrimerLink>
      <Text className="nipa-commit-bar-meta">
        <PrimerLink as={Link} to={commitUrl(org, project, commit.id, rev)} className="nipa-mono-link">
          {shortSha(commit.id, 7)}
        </PrimerLink>
        {' · '}
        <span title={absoluteTime(commit.created_at)}>{relativeTime(commit.created_at)}</span>
      </Text>
      <PrimerLink as={Link} to={commitsUrl(org, project, rev, path)} className="nipa-commit-bar-history">
        <HistoryIcon /> History
      </PrimerLink>
    </div>
  )
}
