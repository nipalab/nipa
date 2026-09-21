import { Link as PrimerLink, Stack, Text } from '@primer/react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { getMyProjectPermissions, getTree, listBranches } from '../api/endpoints'
import { useAuth } from '../auth'
import { EmptyState, ErrorBanner, Loading, Mono, Page } from '../components/ui'
import { useAsync } from '../hooks'
import { PERMISSION_ADMIN, PERMISSION_WRITE, type TreeEntryResponse } from '../api/models'

function parentPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.join('/')
}

export default function RepoPage() {
  const { org = '', project = '' } = useParams()
  const { me } = useAuth()
  const [params, setParams] = useSearchParams()
  const rev = params.get('rev') ?? ''
  const path = params.get('path') ?? ''

  const { data: branches } = useAsync(() => listBranches(org, project), [org, project])
  const { data: permissions } = useAsync(() => getMyProjectPermissions(org, project), [org, project])
  const { data: tree, error, loading } = useAsync(() => getTree(org, project, rev, path), [org, project, rev, path])

  const canWrite = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_WRITE) !== 0,
  )
  const canAdmin = Boolean(
    me?.is_admin || me?.is_super_admin || ((permissions?.project_permission ?? 0) & PERMISSION_ADMIN) !== 0,
  )

  const segments = path.split('/').filter(Boolean)
  const treeQuery = (nextPath: string) => {
    const next = new URLSearchParams()
    if (rev) next.set('rev', rev)
    if (nextPath) next.set('path', nextPath)
    const query = next.toString()
    return query ? `?${query}` : ''
  }

  return (
    <Page
      title={`${org}/${project}`}
      subtitle="Repository browser"
      actions={
        <Stack direction="horizontal" gap="normal">
          <PrimerLink as={Link} to={`/${org}/${project}/commits${rev ? `?branch=${rev}` : ''}`}>
            Commits
          </PrimerLink>
          <PrimerLink as={Link} to={`/${org}/${project}/branches`}>Branches</PrimerLink>
          <PrimerLink as={Link} to={`/${org}/${project}/pulls`}>Merge requests</PrimerLink>
          {canAdmin && <PrimerLink as={Link} to={`/${org}/${project}/settings`}>Settings</PrimerLink>}
        </Stack>
      }
    >
      <Stack direction="horizontal" gap="normal" align="center">
        <Text style={{ color: 'var(--fgColor-muted)' }}>Revision</Text>
        <select
          value={rev}
          onChange={(event) => {
            const next = new URLSearchParams()
            if (event.target.value) next.set('rev', event.target.value)
            if (path) next.set('path', path)
            setParams(next)
          }}
          style={{ padding: 4 }}
        >
          <option value="">default branch</option>
          {branches?.map((branch) => (
            <option key={branch.id} value={branch.name}>
              {branch.name}
              {branch.is_default ? ' (default)' : ''}
            </option>
          ))}
        </select>
        <Text style={{ color: 'var(--fgColor-muted)' }}>
          {canWrite ? 'write access' : 'read-only'}
        </Text>
      </Stack>

      <div style={{ fontSize: 14 }}>
        <PrimerLink as={Link} to={`/${org}/${project}${treeQuery('')}`}>
          root
        </PrimerLink>
        {segments.map((segment, index) => {
          const nextPath = segments.slice(0, index + 1).join('/')
          return (
            <span key={nextPath}>
              {' / '}
              <PrimerLink as={Link} to={`/${org}/${project}${treeQuery(nextPath)}`}>
                {segment}
              </PrimerLink>
            </span>
          )
        })}
        {path && (
          <>
            {' · '}
            <PrimerLink as={Link} to={`/${org}/${project}${treeQuery(parentPath(path))}`}>up</PrimerLink>
          </>
        )}
      </div>

      <ErrorBanner error={error} />
      {loading && <Loading />}
      {!loading && tree && tree.entries.length === 0 && <EmptyState>This directory is empty.</EmptyState>}
      {tree && tree.entries.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {tree.entries.map((entry: TreeEntryResponse) => (
              <tr key={entry.path} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
                <td style={{ padding: '6px 4px' }}>
                  <PrimerLink
                    as={Link}
                    to={
                      entry.type === 'tree'
                        ? `/${org}/${project}${treeQuery(entry.path)}`
                        : `/${org}/${project}/blob${treeQuery(entry.path)}`
                    }
                  >
                    {entry.type === 'tree' ? `${entry.name}/` : entry.name}
                  </PrimerLink>
                </td>
                <td style={{ padding: '6px 4px', textAlign: 'right', color: 'var(--fgColor-muted)' }}>
                  {entry.type === 'file' ? `${entry.size_bytes ?? 0} B` : ''}
                </td>
                <td style={{ padding: '6px 4px', textAlign: 'right' }}>
                  {entry.hash ? <Mono>{entry.hash.slice(0, 8)}</Mono> : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Page>
  )
}
