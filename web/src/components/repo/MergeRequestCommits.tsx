import { useState } from 'react'
import { Button, IconButton, Link as PrimerLink, Stack, Text } from '@primer/react'
import { CheckIcon, CodeIcon, CopyIcon } from '@primer/octicons-react'
import { Link } from 'react-router-dom'
import type { CommitResponse } from '../../api/models'
import { useClipboard } from '../../hooks'
import { Timestamp } from '../Timestamp'
import { EmptyState, Mono } from '../ui'
import { ActorAvatar, type ActorLike } from '../ActorAvatar'
import { commitTitle, shortSha } from './format'

function commitAuthor(commit: CommitResponse): ActorLike {
  return {
    user_id: commit.author_email || commit.id,
    name: commit.author_name || commit.author_email || 'unknown',
  }
}

export function MergeRequestCommits({
  org,
  project,
  commits,
}: {
  org: string
  project: string
  commits: CommitResponse[]
}) {
  const { copied, copy } = useClipboard(1500)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  if (commits.length === 0) {
    return <EmptyState>No commits on this request.</EmptyState>
  }

  // The API returns the first-parent chain newest-first; GitHub lists the
  // commits of a pull request oldest-first.
  const ordered = [...commits].reverse()

  async function copyId(id: string) {
    if (await copy(id)) setCopiedId(id)
  }

  return (
    <Stack direction="vertical" gap="condensed">
      <Text style={{ color: 'var(--fgColor-muted)', fontSize: 13 }}>
        {commits.length} commit{commits.length === 1 ? '' : 's'}
      </Text>
      <div style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, overflow: 'hidden' }}>
        {ordered.map((commit, index) => {
          const author = commitAuthor(commit)
          const last = index === ordered.length - 1
          return (
            <div
              key={commit.id}
              style={{
                display: 'flex',
                gap: 12,
                padding: '12px 16px',
                borderBottom: last ? undefined : '1px solid var(--borderColor-muted)',
              }}
            >
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                <ActorAvatar actor={author} size={28} />
                {!last && (
                  <span
                    style={{
                      flex: 1,
                      width: 2,
                      minHeight: 12,
                      background: 'var(--borderColor-muted)',
                      marginTop: 4,
                    }}
                  />
                )}
              </div>
              <div
                style={{
                  flex: 1,
                  minWidth: 0,
                  display: 'flex',
                  gap: 12,
                  alignItems: 'flex-start',
                  justifyContent: 'space-between',
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <PrimerLink
                    as={Link}
                    to={`/${org}/${project}/commits/${commit.id}`}
                    style={{ fontWeight: 600, color: 'var(--fgColor-default)' }}
                  >
                    {commitTitle(commit.message)}
                  </PrimerLink>
                  <div style={{ color: 'var(--fgColor-muted)', fontSize: 12, marginTop: 2 }}>
                    <strong>{author.name}</strong> authored on <Timestamp value={commit.created_at} mode="absolute" />
                  </div>
                </div>
                <Stack direction="horizontal" gap="condensed" align="center">
                  <Button
                    size="small"
                    aria-label={`Copy commit ${shortSha(commit.id)}`}
                    onClick={() => copyId(commit.id)}
                  >
                    {copied && copiedId === commit.id ? <CheckIcon /> : <CopyIcon />}{' '}
                    <Mono>{shortSha(commit.id)}</Mono>
                  </Button>
                  <IconButton
                    size="small"
                    as={Link}
                    to={`/${org}/${project}/tree/${commit.id}`}
                    icon={CodeIcon}
                    aria-label="Browse repository at this point"
                  />
                </Stack>
              </div>
            </div>
          )
        })}
      </div>
    </Stack>
  )
}
