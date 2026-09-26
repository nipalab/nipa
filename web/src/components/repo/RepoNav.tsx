import { UnderlineNav } from '@primer/react'
import {
  CodeIcon,
  GearIcon,
  GitBranchIcon,
  GitPullRequestIcon,
  HistoryIcon,
  LockIcon,
} from '@primer/octicons-react'
import { Link } from 'react-router-dom'
import { commitsUrl, repoUrl, treeUrl } from './repoPaths'

export type RepoTab = 'code' | 'commits' | 'branches' | 'pulls' | 'locks' | 'settings'

function itemProps(to: string, current: boolean) {
  return { as: Link, to, 'aria-current': current ? ('page' as const) : undefined }
}

export function RepoNav({
  org,
  project,
  active,
  rev = '',
  canAdmin = false,
}: {
  org: string
  project: string
  active: RepoTab
  rev?: string
  canAdmin?: boolean
}) {
  return (
    <UnderlineNav aria-label="Repository">
      <UnderlineNav.Item
        {...itemProps(treeUrl(org, project, rev), active === 'code')}
        leadingVisual={<CodeIcon />}
      >
        Code
      </UnderlineNav.Item>
      <UnderlineNav.Item
        {...itemProps(commitsUrl(org, project, rev), active === 'commits')}
        leadingVisual={<HistoryIcon />}
      >
        Commits
      </UnderlineNav.Item>
      <UnderlineNav.Item
        {...itemProps(`${repoUrl(org, project)}/branches`, active === 'branches')}
        leadingVisual={<GitBranchIcon />}
      >
        Branches
      </UnderlineNav.Item>
      <UnderlineNav.Item
        {...itemProps(`${repoUrl(org, project)}/pulls`, active === 'pulls')}
        leadingVisual={<GitPullRequestIcon />}
      >
        Merge requests
      </UnderlineNav.Item>
      <UnderlineNav.Item
        {...itemProps(`${repoUrl(org, project)}/locks`, active === 'locks')}
        leadingVisual={<LockIcon />}
      >
        Locks
      </UnderlineNav.Item>
      {canAdmin && (
        <UnderlineNav.Item
          {...itemProps(`${repoUrl(org, project)}/settings`, active === 'settings')}
          leadingVisual={<GearIcon />}
        >
          Settings
        </UnderlineNav.Item>
      )}
    </UnderlineNav>
  )
}
