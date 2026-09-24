import { Heading, Text } from '@primer/react'
import type { ReactNode } from 'react'
import { Page } from '../ui'
import { RepoNav, type RepoTab } from './RepoNav'

export function RepoPageShell({
  org,
  project,
  active,
  rev = '',
  canAdmin = false,
  canWrite = false,
  heading,
  children,
}: {
  org: string
  project: string
  active: RepoTab
  rev?: string
  canAdmin?: boolean
  canWrite?: boolean
  heading?: ReactNode
  children: ReactNode
}) {
  return (
    <Page
      title={
        <>
          <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}>{org} /</span> {project}
        </>
      }
      actions={<Text style={{ color: 'var(--fgColor-muted)' }}>{canWrite ? 'write access' : 'read-only'}</Text>}
    >
      <RepoNav org={org} project={project} active={active} rev={rev} canAdmin={canAdmin} />
      {heading && (
        <Heading as="h3" style={{ margin: 0 }}>
          {heading}
        </Heading>
      )}
      {children}
    </Page>
  )
}
