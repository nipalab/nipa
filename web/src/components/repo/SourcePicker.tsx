import { useState } from 'react'
import { Button, FormControl, TextInput } from '@primer/react'
import { GitCommitIcon } from '@primer/octicons-react'
import type { BranchResponse } from '../../api/models'
import { BranchPicker } from './MemberPicker'

// SourcePicker chooses where a new branch or tag starts: a branch from the
// list, or a specific commit id typed in.
export function SourcePicker({
  label,
  branches,
  defaultBranch,
  onChange,
}: {
  label: string
  branches: BranchResponse[]
  defaultBranch: string
  onChange: (from: string) => void
}) {
  const [branch, setBranch] = useState('')
  const [commitMode, setCommitMode] = useState(false)
  const [commit, setCommit] = useState('')
  const chosen = branch || defaultBranch

  return (
    <FormControl>
      <FormControl.Label>{label}</FormControl.Label>
      {commitMode ? (
        <TextInput
          block
          autoFocus
          leadingVisual={GitCommitIcon}
          placeholder="Commit ID"
          aria-label="Commit ID"
          value={commit}
          onChange={(event) => {
            setCommit(event.target.value)
            onChange(event.target.value.trim())
          }}
        />
      ) : (
        <BranchPicker
          label="Choose a source branch"
          branches={branches}
          value={chosen}
          onChange={(name) => {
            setBranch(name)
            onChange(name)
          }}
        />
      )}
      <FormControl.Caption>
        <Button
          variant="link"
          onClick={() => {
            const next = !commitMode
            setCommitMode(next)
            onChange(next ? commit.trim() : branch)
          }}
        >
          {commitMode ? 'Pick a branch instead' : 'Use a specific commit instead'}
        </Button>
      </FormControl.Caption>
    </FormControl>
  )
}
