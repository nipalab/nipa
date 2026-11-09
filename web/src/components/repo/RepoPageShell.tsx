import { Link as PrimerLink, Stack } from '@primer/react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Page, SectionHeading, type Crumb } from '../ui'
import { RepoNav, type RepoTab } from './RepoNav'

export function RepoPageShell({
  org,
  project,
  active,
  rev = '',
  canAdmin = false,
  heading,
  actions,
  crumbs,
  wide = false,
  children,
}: {
  org: string
  project: string
  active: RepoTab
  rev?: string
  canAdmin?: boolean
  heading?: ReactNode
  actions?: ReactNode
  crumbs?: Crumb[]
  wide?: boolean
  children: ReactNode
}) {
  return (
    <Page
      breadcrumbs={crumbs}
      wide={wide}
      title={
        <>
          <PrimerLink as={Link} to={`/${org}`} style={{ fontWeight: 400 }}>
            {org}
          </PrimerLink>
          <span style={{ color: 'var(--fgColor-muted)', fontWeight: 400 }}> / </span>
          <PrimerLink as={Link} to={`/${org}/${project}`} style={{ fontWeight: 600 }}>
            {project}
          </PrimerLink>
        </>
      }
      actions={
        actions && (
          <Stack direction="horizontal" gap="normal" align="center">
            {actions}
          </Stack>
        )
      }
    >
      <RepoNav org={org} project={project} active={active} rev={rev} canAdmin={canAdmin} />
      {heading && <SectionHeading>{heading}</SectionHeading>}
      {children}
    </Page>
  )
}
