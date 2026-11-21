import { useEffect, useMemo, useState } from 'react'
import { Label } from '@primer/react'
import { IssueOpenedIcon, LinkExternalIcon } from '@primer/octicons-react'
import { Link } from 'react-router-dom'
import { resolveIssues } from '../../api/endpoints'
import type { IssueKeyRef, IssueResponse } from '../../api/models'
import { issueUrl } from './repoPaths'

const RESOLVE_BATCH = 50

export type IssueMap = ReadonlyMap<string, IssueResponse>

type LabelVariant = 'default' | 'accent' | 'done' | 'secondary'

function statusVariant(issue?: IssueResponse): LabelVariant {
  switch (issue?.status_category) {
    case 'done':
      return 'done'
    case 'in_progress':
      return 'accent'
    default:
      return 'secondary'
  }
}

export function collectIssueKeys(items: { issue_keys?: IssueKeyRef[] }[]): string[] {
  const keys = new Set<string>()
  for (const item of items) for (const ref of item.issue_keys ?? []) keys.add(ref.key)
  return [...keys].sort()
}

// useIssues resolves issue statuses for a page in batched calls, asking only
// for keys it has not resolved yet. Failures leave the keys unresolved.
export function useIssues(org: string, project: string, keys: string[]): IssueMap {
  const [issues, setIssues] = useState<Map<string, IssueResponse>>(() => new Map())
  const [asked] = useState(() => new Set<string>())
  const wanted = keys.join(',')

  useEffect(() => {
    asked.clear()
    setIssues(new Map())
  }, [org, project, asked])

  useEffect(() => {
    const missing = wanted.split(',').filter((key) => key && !asked.has(key))
    if (missing.length === 0) return
    missing.forEach((key) => asked.add(key))
    let active = true
    const pending = new Set(missing)
    for (let i = 0; i < missing.length; i += RESOLVE_BATCH) {
      const batch = missing.slice(i, i + RESOLVE_BATCH)
      resolveIssues(org, project, batch)
        .then((res) => {
          if (!active) return
          batch.forEach((key) => pending.delete(key))
          setIssues((prev) => {
            const next = new Map(prev)
            for (const issue of res.issues) next.set(issue.key, issue)
            return next
          })
        })
        .catch(() => {
          if (active) batch.forEach((key) => pending.delete(key))
        })
    }
    return () => {
      active = false
      pending.forEach((key) => asked.delete(key))
    }
  }, [org, project, wanted, asked])

  return issues
}

export function issueSummary(issue?: IssueResponse): string {
  if (issue?.state !== 'ok') return ''
  return [issue.title, issue.status, issue.assignee && `Assignee: ${issue.assignee}`].filter(Boolean).join(' · ')
}

export function IssueStatusLabel({ issue }: { issue?: IssueResponse }) {
  if (issue?.state === 'ok' && issue.status) {
    return (
      <Label size="small" variant={statusVariant(issue)}>
        {issue.status}
      </Label>
    )
  }
  if (issue?.state === 'not_found') {
    return (
      <Label size="small" variant="attention">
        Unknown
      </Label>
    )
  }
  return null
}

// IssueChip links the key to the issue's page in Nipa (the work linked to it)
// and offers the tracker's own page next to it.
export function IssueChip({
  org,
  project,
  issueRef,
  issue,
}: {
  org: string
  project: string
  issueRef: IssueKeyRef
  issue?: IssueResponse
}) {
  const stop = (event: React.MouseEvent) => event.stopPropagation()
  return (
    <span className="nipa-issue-chip">
      <Link
        className="nipa-issue-chip-main"
        to={issueUrl(org, project, issueRef.key)}
        title={issueSummary(issue) || `Work linked to ${issueRef.key}`}
        onClick={stop}
      >
        <IssueOpenedIcon size={12} />
        <span className="nipa-issue-key">{issueRef.key}</span>
        <IssueStatusLabel issue={issue} />
      </Link>
      <a
        className="nipa-issue-chip-external"
        href={issueRef.url}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={`Open ${issueRef.key} in the issue tracker`}
        title={`Open ${issueRef.key} in the issue tracker`}
        onClick={stop}
      >
        <LinkExternalIcon size={12} />
      </a>
    </span>
  )
}

export function IssueChips({
  org,
  project,
  refs,
  issues,
}: {
  org: string
  project: string
  refs?: IssueKeyRef[]
  issues: IssueMap
}) {
  const sorted = useMemo(() => [...(refs ?? [])].sort((a, b) => a.key.localeCompare(b.key)), [refs])
  if (sorted.length === 0) return null
  return (
    <span className="nipa-issue-chips" aria-label="Linked issues">
      {sorted.map((ref) => (
        <IssueChip key={ref.key} org={org} project={project} issueRef={ref} issue={issues.get(ref.key)} />
      ))}
    </span>
  )
}
