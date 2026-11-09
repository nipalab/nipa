import { Checkbox, CheckboxGroup, FormControl } from '@primer/react'
import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'

const PERMISSION_OPTIONS = [
  { bit: PERMISSION_READ, label: 'Read', description: 'View, clone and download files.' },
  { bit: PERMISSION_WRITE, label: 'Write', description: 'Push changes, create branches and open merge requests.' },
  { bit: PERMISSION_LOCK, label: 'Lock', description: 'Lock and unlock binary files.' },
  { bit: PERMISSION_ADMIN, label: 'Admin', description: 'Manage access rules, branch protection and settings.' },
]

export function PermissionCheckboxes({
  value,
  onChange,
  label = 'Permissions',
}: {
  value: number
  onChange: (mask: number) => void
  label?: string
}) {
  function toggle(bit: number) {
    onChange(value & bit ? value & ~bit : value | bit)
  }

  return (
    <CheckboxGroup required>
      <CheckboxGroup.Label>{label}</CheckboxGroup.Label>
      {PERMISSION_OPTIONS.map((option) => (
        <FormControl key={option.bit}>
          <Checkbox value={String(option.bit)} checked={(value & option.bit) !== 0} onChange={() => toggle(option.bit)} />
          <FormControl.Label>{option.label}</FormControl.Label>
          <FormControl.Caption>{option.description}</FormControl.Caption>
        </FormControl>
      ))}
    </CheckboxGroup>
  )
}
