import { ActionList, ActionMenu, TextInput } from '@primer/react'
import { GitBranchIcon, SearchIcon } from '@primer/octicons-react'
import { useMemo, useState } from 'react'
import type { BranchResponse } from '../../api/models'

export function BranchSelector({
  branches,
  rev,
  onSelect,
  loading = false,
}: {
  branches: BranchResponse[] | null
  rev: string
  onSelect: (rev: string) => void
  loading?: boolean
}) {
  const [query, setQuery] = useState('')
  const current = rev || branches?.find((branch) => branch.is_default)?.name || 'default branch'
  const filtered = useMemo(() => {
    const list = branches ?? []
    const needle = query.trim().toLowerCase()
    if (!needle) return list
    return list.filter((branch) => branch.name.toLowerCase().includes(needle))
  }, [branches, query])

  return (
    <ActionMenu
      onOpenChange={(open) => {
        if (!open) setQuery('')
      }}
    >
      <ActionMenu.Button leadingVisual={GitBranchIcon} disabled={loading} block>
        {current}
      </ActionMenu.Button>
      <ActionMenu.Overlay width="medium">
        <div style={{ padding: 8 }}>
          <TextInput
            block
            autoFocus
            aria-label="Find a branch"
            leadingVisual={SearchIcon}
            placeholder="Find a branch"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <ActionList>
          {filtered.length === 0 && <ActionList.Item disabled>No branches found</ActionList.Item>}
          {filtered.map((branch) => (
            <ActionList.Item
              key={branch.id}
              selected={branch.name === current}
              onSelect={() => onSelect(branch.name)}
            >
              <ActionList.LeadingVisual>
                <GitBranchIcon />
              </ActionList.LeadingVisual>
              {branch.name}
              {branch.is_default && <ActionList.TrailingVisual>default</ActionList.TrailingVisual>}
            </ActionList.Item>
          ))}
        </ActionList>
      </ActionMenu.Overlay>
    </ActionMenu>
  )
}
