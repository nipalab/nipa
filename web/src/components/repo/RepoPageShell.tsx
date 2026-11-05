import { Stack, Text } from '@primer/react'
import type { ReactNode } from 'react'
import { Page, SectionHeading, type Crumb } from '../ui'
import { RepoNav, type RepoTab } from './RepoNav'

export function RepoPageShell({
  org,
  project,
  active,
  rev = '',
  canAdmin = false,
  canWrite = false,
  heading,
  actions,
  crumbs,
  children,
}: {
  org: string
  project: string
  active: RepoTab
  rev?: string
  canAdmin?: boolean
  canWrite?: boolean
  heading?: ReactNode
  actions?: ReactNode
  crumbs?: Crumb[]
  children: ReactNode
}) {
  return (
    <Page
      breadcrumbs={crumbs}
      title={
        <>
          <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}>{org} /</span> {project}
        </>
      }
      actions={
        <Stack direction="horizontal" gap="normal" align="center">
          {actions}
          <Text style={{ color: 'var(--fgColor-muted)' }}>{canWrite ? 'write access' : 'read-only'}</Text>
        </Stack>
      }
    >
      <RepoNav org={org} project={project} active={active} rev={rev} canAdmin={canAdmin} />
      {heading && <SectionHeading>{heading}</SectionHeading>}
      {children}
    </Page>
  )
}
