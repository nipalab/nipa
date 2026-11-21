import { useCallback, useEffect, useRef, useState, type FocusEvent } from 'react'
import { Button, SelectPanel } from '@primer/react'
import { IssueOpenedIcon } from '@primer/octicons-react'
import { resolveIssues, searchIssues } from '../../api/endpoints'
import type { IssueResponse } from '../../api/models'

const SEARCH_DELAY_MS = 250
const SEARCH_LIMIT = 20
const MIN_QUERY = 2

// useIssueTrackerEnabled tells whether the project reads an issue tracker: an
// empty resolve answers 200 without calling the tracker, 404 without settings
// and 503 when the integration is off.
export function useIssueTrackerEnabled(org: string, project: string): boolean {
  const [enabled, setEnabled] = useState(false)
  useEffect(() => {
    let active = true
    setEnabled(false)
    resolveIssues(org, project, [])
      .then(() => active && setEnabled(true))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [org, project])
  return enabled
}

// insertIssueKey puts key between start and end, separated from neighbouring
// words by a space, and returns the new text.
export function insertIssueKey(text: string, key: string, start: number, end = start): string {
  const before = text.slice(0, start)
  const after = text.slice(end)
  const lead = before !== '' && !/[\s([]$/.test(before) ? ' ' : ''
  const trail = after !== '' && !/^[\s).,;:!?\]]/.test(after) ? ' ' : ''
  return `${before}${lead}${key}${trail}${after}`
}

// useIssueInsertion remembers the caret of a text field when it loses focus
// (for example to the issue picker) and inserts picked keys there, or at the
// end when the field was never focused.
export function useIssueInsertion(value: string, onChange: (next: string) => void) {
  const caret = useRef<{ start: number; end: number } | null>(null)
  const onBlur = useCallback((event: FocusEvent<HTMLTextAreaElement | HTMLInputElement>) => {
    const { selectionStart, selectionEnd } = event.currentTarget
    caret.current = { start: selectionStart ?? 0, end: selectionEnd ?? selectionStart ?? 0 }
  }, [])
  const insert = useCallback(
    (key: string) => {
      const at = caret.current ?? { start: value.length, end: value.length }
      const next = insertIssueKey(value, key, Math.min(at.start, value.length), Math.min(at.end, value.length))
      caret.current = null
      onChange(next)
    },
    [value, onChange],
  )
  return { onBlur, insert }
}

type SearchState = { status: 'idle' | 'loading' | 'done' | 'unavailable'; issues: IssueResponse[] }

function panelMessage(state: SearchState) {
  switch (state.status) {
    case 'idle':
      return { variant: 'empty' as const, title: 'Search issues', body: 'Type a key or words from the issue title.' }
    case 'unavailable':
      return {
        variant: 'warning' as const,
        title: 'The issue tracker is not answering',
        body: 'You can still type the issue key yourself.',
      }
    default:
      return state.issues.length === 0 ? { variant: 'empty' as const, title: 'No matching issues', body: '' } : undefined
  }
}

// IssuePicker searches the project's issue tracker and hands the chosen key
// to onPick. It disappears when the tracker cannot search.
export function IssuePicker({
  org,
  project,
  onPick,
  disabled,
}: {
  org: string
  project: string
  onPick: (key: string) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const [unsupported, setUnsupported] = useState(false)
  const [state, setState] = useState<SearchState>({ status: 'idle', issues: [] })

  useEffect(() => {
    const query = filter.trim()
    if (query.length < MIN_QUERY) {
      setState({ status: 'idle', issues: [] })
      return
    }
    let active = true
    setState((prev) => ({ status: 'loading', issues: prev.issues }))
    const timer = setTimeout(() => {
      searchIssues(org, project, query, SEARCH_LIMIT)
        .then((res) => {
          if (!active) return
          if (!res.supported) {
            setUnsupported(true)
            setOpen(false)
            return
          }
          setState(res.unavailable ? { status: 'unavailable', issues: [] } : { status: 'done', issues: res.issues })
        })
        .catch(() => {
          if (active) setState({ status: 'unavailable', issues: [] })
        })
    }, SEARCH_DELAY_MS)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [org, project, filter])

  if (unsupported) return null

  const items = state.issues.map((issue) => ({
    id: issue.key,
    text: issue.title ? `${issue.key} ${issue.title}` : issue.key,
    description: [issue.status, issue.type].filter(Boolean).join(' · ') || undefined,
    descriptionVariant: 'block' as const,
    leadingVisual: IssueOpenedIcon,
  }))

  return (
    <SelectPanel
      title="Reference an issue"
      placeholder="Search issues"
      placeholderText="Search issues"
      open={open}
      onOpenChange={(next: boolean) => {
        setOpen(next)
        if (!next) setFilter('')
      }}
      filterValue={filter}
      onFilterChange={setFilter}
      loading={state.status === 'loading' && state.issues.length === 0}
      items={items}
      message={panelMessage(state)}
      selected={undefined}
      onSelectedChange={(selected: { id?: string | number } | undefined) => {
        if (selected?.id !== undefined) onPick(String(selected.id))
        setOpen(false)
        setFilter('')
      }}
      renderAnchor={({ children: _children, ...anchorProps }) => (
        <Button {...anchorProps} size="small" leadingVisual={IssueOpenedIcon} disabled={disabled}>
          Reference issue
        </Button>
      )}
      overlayProps={{ width: 'medium', maxHeight: 'medium' }}
    />
  )
}
