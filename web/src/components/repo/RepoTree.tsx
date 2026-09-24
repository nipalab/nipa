import { ActionList, ActionMenu, IconButton, Link as PrimerLink } from '@primer/react'
import { KebabHorizontalIcon } from '@primer/octicons-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import type { TreeEntryResponse } from '../../api/models'
import { fileIcon } from './fileIcon'
import { absoluteTime, commitTitle, relativeTime } from './format'
import { blobUrl, commitUrl, commitsUrl, treeUrl } from './repoPaths'

function TreeRow({
  org,
  project,
  rev,
  entry,
}: {
  org: string
  project: string
  rev: string
  entry: TreeEntryResponse
}) {
  const navigate = useNavigate()
  const [hover, setHover] = useState(false)
  const { Icon, color } = fileIcon(entry.name, entry.type)
  const last = entry.last_commit
  const target =
    entry.type === 'tree' ? treeUrl(org, project, rev, entry.path) : blobUrl(org, project, rev, entry.path)

  return (
    <tr
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{ borderTop: '1px solid var(--borderColor-muted)' }}
    >
      <td style={{ padding: '6px 8px 6px 16px', width: '34%' }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, minWidth: 0, maxWidth: '100%' }}>
          <span style={{ color, flexShrink: 0, display: 'inline-flex' }}>
            <Icon size={16} />
          </span>
          <PrimerLink
            as={Link}
            to={target}
            style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          >
            {entry.name}
          </PrimerLink>
        </span>
      </td>
      <td style={{ padding: '6px 8px', width: '50%', maxWidth: 0, color: 'var(--fgColor-muted)' }}>
        {last && (
          <PrimerLink
            as={Link}
            to={commitUrl(org, project, last.id, rev)}
            title={commitTitle(last.message)}
            style={{
              display: 'block',
              color: 'var(--fgColor-muted)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {commitTitle(last.message)}
          </PrimerLink>
        )}
      </td>
      <td style={{ padding: '6px 16px 6px 8px', width: 140, textAlign: 'right', whiteSpace: 'nowrap', color: 'var(--fgColor-muted)' }}>
        {last && <span title={absoluteTime(last.created_at)}>{relativeTime(last.created_at)}</span>}
      </td>
      <td style={{ width: 44, padding: '0 8px' }}>
        <div style={{ opacity: hover ? 1 : 0 }}>
          <ActionMenu>
            <ActionMenu.Anchor>
              <IconButton
                icon={KebabHorizontalIcon}
                aria-label={`Actions for ${entry.name}`}
                variant="invisible"
              />
            </ActionMenu.Anchor>
            <ActionMenu.Overlay align="end">
              <ActionList>
                {entry.type === 'file' && (
                  <ActionList.Item onSelect={() => navigate(blobUrl(org, project, rev, entry.path))}>
                    View file
                  </ActionList.Item>
                )}
                <ActionList.Item onSelect={() => navigate(commitsUrl(org, project, rev, entry.path))}>
                  History
                </ActionList.Item>
              </ActionList>
            </ActionMenu.Overlay>
          </ActionMenu>
        </div>
      </td>
    </tr>
  )
}

export function RepoTree({
  org,
  project,
  rev,
  entries,
}: {
  org: string
  project: string
  rev: string
  entries: TreeEntryResponse[]
}) {
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse', tableLayout: 'fixed', fontSize: 14 }}>
      <thead>
        <tr style={{ background: 'var(--bgColor-muted)', color: 'var(--fgColor-muted)', fontSize: 12 }}>
          <th style={{ textAlign: 'left', fontWeight: 500, padding: '8px 8px 8px 16px' }}>Name</th>
          <th style={{ textAlign: 'left', fontWeight: 500, padding: '8px 8px' }}>Last commit</th>
          <th style={{ textAlign: 'right', fontWeight: 500, padding: '8px 16px 8px 8px' }}>Last modified</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {entries.map((entry) => (
          <TreeRow key={entry.path} org={org} project={project} rev={rev} entry={entry} />
        ))}
      </tbody>
    </table>
  )
}
