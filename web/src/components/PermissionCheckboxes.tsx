import { Stack, Text } from '@primer/react'
import { PERMISSION_ADMIN, PERMISSION_LOCK, PERMISSION_READ, PERMISSION_WRITE } from '../api/models'

const PERMISSION_OPTIONS = [
  { bit: PERMISSION_READ, label: 'Read', description: 'View and download files' },
  { bit: PERMISSION_WRITE, label: 'Write', description: 'Push changes and create branches' },
  { bit: PERMISSION_LOCK, label: 'Lock', description: 'Lock and unlock binary files' },
  { bit: PERMISSION_ADMIN, label: 'Admin', description: 'Manage access rules and settings' },
]

export function PermissionCheckboxes({
  value,
  onChange,
}: {
  value: number
  onChange: (mask: number) => void
}) {
  function toggle(bit: number) {
    if (value & bit) {
      onChange(value & ~bit)
    } else {
      onChange(value | bit)
    }
  }

  return (
    <Stack direction="vertical" gap="condensed">
      {PERMISSION_OPTIONS.map((option) => (
        <label
          key={option.bit}
          style={{ display: 'flex', gap: 8, alignItems: 'flex-start', cursor: 'pointer' }}
        >
          <input
            type="checkbox"
            checked={(value & option.bit) !== 0}
            onChange={() => toggle(option.bit)}
            style={{ marginTop: 2 }}
          />
          <span>
            <span style={{ fontWeight: 600 }}>{option.label}</span>
            <Text as="span" style={{ color: 'var(--fgColor-muted)' }}>
              {' '}
              — {option.description}
            </Text>
          </span>
        </label>
      ))}
    </Stack>
  )
}
