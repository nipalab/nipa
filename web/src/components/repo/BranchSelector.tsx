import { ActionList, ActionMenu, Button, TextInput } from '@primer/react'
import { GitBranchIcon, SearchIcon, TagIcon } from '@primer/octicons-react'
import { useMemo, useState } from 'react'
import type { BranchResponse, TagResponse } from '../../api/models'

export function BranchSelector({
  branches,
  rev,
  onSelect,
  loading = false,
  placeholder,
  tags,
  onSelectTag,
  onOpen,
}: {
  branches: BranchResponse[] | null
  rev: string
  onSelect: (rev: string) => void
  loading?: boolean
  placeholder?: string
  tags?: TagResponse[] | null
  onSelectTag?: (tag: TagResponse) => void
  onOpen?: () => void
}) {
  const [query, setQuery] = useState('')
  const [mode, setMode] = useState<'branches' | 'tags'>('branches')
  const tagsEnabled = tags !== undefined && onSelectTag !== undefined
  const currentTag = tagsEnabled ? tags?.find((tag) => tag.commit_id === rev) : undefined
  const current =
    currentTag?.name || rev || placeholder || branches?.find((branch) => branch.is_default)?.name || 'default branch'
  const needle = query.trim().toLowerCase()
  const filteredBranches = useMemo(() => {
    const list = branches ?? []
    return needle ? list.filter((branch) => branch.name.toLowerCase().includes(needle)) : list
  }, [branches, needle])
  const filteredTags = useMemo(() => {
    const list = tags ?? []
    return needle ? list.filter((tag) => tag.name.toLowerCase().includes(needle)) : list
  }, [tags, needle])
  const showingTags = tagsEnabled && mode === 'tags'

  return (
    <ActionMenu
      onOpenChange={(open) => {
        if (open) onOpen?.()
        else setQuery('')
      }}
    >
      <ActionMenu.Button leadingVisual={currentTag ? TagIcon : GitBranchIcon} disabled={loading} block>
        {current}
      </ActionMenu.Button>
      <ActionMenu.Overlay width="medium">
        <div style={{ padding: 8, display: 'flex', flexDirection: 'column', gap: 8 }}>
          {tagsEnabled && (
            <div style={{ display: 'flex', gap: 4 }}>
              <Button size="small" variant={mode === 'branches' ? 'primary' : 'invisible'} onClick={() => setMode('branches')}>
                Branches
              </Button>
              <Button size="small" variant={mode === 'tags' ? 'primary' : 'invisible'} onClick={() => setMode('tags')}>
                Tags
              </Button>
            </div>
          )}
          <TextInput
            block
            autoFocus
            aria-label={showingTags ? 'Find a tag' : 'Find a branch'}
            leadingVisual={SearchIcon}
            placeholder={showingTags ? 'Find a tag' : 'Find a branch'}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <ActionList>
          {!showingTags && filteredBranches.length === 0 && <ActionList.Item disabled>No branches found</ActionList.Item>}
          {!showingTags &&
            filteredBranches.map((branch) => (
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
          {showingTags && tags === null && <ActionList.Item disabled>Loading tags…</ActionList.Item>}
          {showingTags && tags !== null && filteredTags.length === 0 && <ActionList.Item disabled>No tags found</ActionList.Item>}
          {showingTags &&
            filteredTags.map((tag) => (
              <ActionList.Item key={tag.id} selected={tag.commit_id === rev} onSelect={() => onSelectTag?.(tag)}>
                <ActionList.LeadingVisual>
                  <TagIcon />
                </ActionList.LeadingVisual>
                {tag.name}
              </ActionList.Item>
            ))}
        </ActionList>
      </ActionMenu.Overlay>
    </ActionMenu>
  )
}
