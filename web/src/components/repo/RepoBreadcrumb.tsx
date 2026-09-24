import { Link as PrimerLink } from '@primer/react'
import { Link } from 'react-router-dom'
import { repoUrl, treeUrl } from './repoPaths'

interface Crumb {
  label: string
  to?: string
}

export function RepoBreadcrumb({
  org,
  project,
  rev,
  path = '',
  leafIsFile = false,
}: {
  org: string
  project: string
  rev: string
  path?: string
  leafIsFile?: boolean
}) {
  const segments = path.split('/').filter(Boolean)
  const crumbs: Crumb[] = [
    { label: org, to: `/${encodeURIComponent(org)}` },
    { label: project, to: treeUrl(org, project, rev) },
  ]
  segments.forEach((segment, index) => {
    const isLeaf = index === segments.length - 1
    const subPath = segments.slice(0, index + 1).join('/')
    crumbs.push({
      label: segment,
      to: isLeaf && leafIsFile ? undefined : treeUrl(org, project, rev, subPath),
    })
  })

  return (
    <nav
      aria-label="Repository path"
      style={{
        display: 'flex',
        alignItems: 'center',
        flexWrap: 'wrap',
        gap: 6,
        fontSize: 14,
        minWidth: 0,
      }}
    >
      {crumbs.map((crumb, index) => (
        <span key={`${crumb.label}-${index}`} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          {index > 0 && <span style={{ color: 'var(--fgColor-muted)' }}>/</span>}
          {crumb.to ? (
            <PrimerLink as={Link} to={crumb.to} style={{ whiteSpace: 'nowrap' }}>
              {crumb.label}
            </PrimerLink>
          ) : (
            <span
              style={{
                fontWeight: 600,
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                maxWidth: 320,
              }}
            >
              {crumb.label}
            </span>
          )}
        </span>
      ))}
      {path && (
        <span style={{ color: 'var(--fgColor-muted)', marginLeft: 2 }}>
          <PrimerLink as={Link} to={repoUrl(org, project)}>
            (root)
          </PrimerLink>
        </span>
      )}
    </nav>
  )
}
