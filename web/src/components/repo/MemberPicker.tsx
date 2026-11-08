import { IconButton, Select } from '@primer/react'
import { XIcon } from '@primer/octicons-react'
import type { OrgMemberResponse } from '../../api/models'
import { ActorAvatar } from '../ActorAvatar'

export function memberLabel(member: OrgMemberResponse): string {
  return member.name || member.email
}

export function MemberSelect({
  label,
  placeholder,
  members,
  value,
  onChange,
  disabled,
}: {
  label: string
  placeholder: string
  members: OrgMemberResponse[]
  value: string
  onChange: (userId: string) => void
  disabled?: boolean
}) {
  return (
    <Select aria-label={label} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
      <option value="">{placeholder}</option>
      {members.map((member) => (
        <option key={member.user_id} value={member.user_id}>
          {memberLabel(member)}
        </option>
      ))}
    </Select>
  )
}

export function MemberMultiPicker({
  label,
  placeholder,
  members,
  selected,
  onChange,
  disabled,
}: {
  label: string
  placeholder: string
  members: OrgMemberResponse[]
  selected: string[]
  onChange: (userIds: string[]) => void
  disabled?: boolean
}) {
  const chosen = selected
    .map((id) => members.find((member) => member.user_id === id))
    .filter((member): member is OrgMemberResponse => Boolean(member))
  const available = members.filter((member) => !selected.includes(member.user_id))
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {chosen.map((member) => (
        <div key={member.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
          <ActorAvatar actor={member} size={20} />
          <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>{memberLabel(member)}</span>
          <IconButton
            size="small"
            aria-label={`remove ${memberLabel(member)}`}
            disabled={disabled}
            onClick={() => onChange(selected.filter((id) => id !== member.user_id))}
            icon={XIcon}
          />
        </div>
      ))}
      {available.length > 0 && (
        <MemberSelect
          label={label}
          placeholder={placeholder}
          members={available}
          value=""
          disabled={disabled}
          onChange={(userId) => userId && onChange([...selected, userId])}
        />
      )}
    </div>
  )
}
