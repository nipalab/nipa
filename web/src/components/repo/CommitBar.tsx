import { Link as PrimerLink, Text } from '@primer/react'
import { Link } from 'react-router-dom'
import type { CommitResponse } from '../../api/models'
import { absoluteTime, commitTitle, relativeTime } from './format'
import { commitsUrl } from './repoPaths'

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
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '10px 16px',
        background: 'var(--bgColor-muted)',
        borderBottom: '1px solid var(--borderColor-default)',
      }}
    >
      <Text style={{ color: 'var(--fgColor-muted)', flexShrink: 0 }}>
        <strong style={{ color: 'var(--fgColor-default)' }}>{author}</strong> committed{' '}
        <span title={absoluteTime(commit.created_at)}>{relativeTime(commit.created_at)}</span>
      </Text>
      <span
        style={{
          flex: 1,
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {commitTitle(commit.message)}
      </span>
      <PrimerLink as={Link} to={commitsUrl(org, project, rev, path)} style={{ flexShrink: 0 }}>
        History
      </PrimerLink>
    </div>
  )
}
