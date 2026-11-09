import { useMemo, useState, type ElementType, type ReactNode } from 'react'
import { Button, IconButton, SelectPanel } from '@primer/react'
import { GearIcon, GitBranchIcon, TriangleDownIcon, XIcon } from '@primer/octicons-react'
import type { BranchResponse, OrgMemberResponse } from '../../api/models'
import { ActorAvatar, type ActorLike } from '../ActorAvatar'

export function memberLabel(member: OrgMemberResponse): string {
  return member.name || member.email
}

export interface PickerOption {
  id: string
  text: string
  description?: string
  avatar?: ActorLike
  icon?: ElementType
}

type PanelItem = {
  id: string
  text: string
  description?: string
  descriptionVariant?: 'inline' | 'block'
  leadingVisual?: ElementType
}

function avatarVisual(actor: ActorLike): ElementType {
  return function Avatar() {
    return <ActorAvatar actor={actor} size={20} />
  }
}

export function memberOptions(members: OrgMemberResponse[]): PickerOption[] {
  return members.map((member) => ({
    id: member.user_id,
    text: memberLabel(member),
    description: member.name && member.email ? member.email : undefined,
    avatar: { user_id: member.user_id, name: member.name || member.email, photo_url: member.photo_url },
  }))
}

export function branchOptions(branches: BranchResponse[]): PickerOption[] {
  return branches.map((branch) => ({
    id: branch.name,
    text: branch.name,
    description: branch.is_default ? 'default' : undefined,
    icon: GitBranchIcon,
  }))
}

type PickerPanelProps = {
  title: string
  placeholder: string
  options: PickerOption[]
  emptyText: string
  renderAnchor: (props: React.HTMLAttributes<HTMLElement>) => ReactNode
} & ({ multiple: true; value: string[]; onChange: (ids: string[]) => void } | { multiple?: false; value: string; onChange: (id: string) => void })

// PickerPanel is a searchable Primer SelectPanel over a list of options, the
// GitHub-style picker behind reviewers, assignees, filters and branches.
function PickerPanel(props: PickerPanelProps) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const allItems = useMemo<PanelItem[]>(
    () =>
      props.options.map((option) => ({
        id: option.id,
        text: option.text,
        description: option.description,
        descriptionVariant: 'block',
        leadingVisual: option.avatar ? avatarVisual(option.avatar) : option.icon,
      })),
    [props.options],
  )
  const query = filter.trim().toLowerCase()
  const items = query
    ? allItems.filter((item) => item.text.toLowerCase().includes(query) || (item.description ?? '').toLowerCase().includes(query))
    : allItems
  const byId = (id: string) => allItems.find((item) => item.id === id)
  const message = items.length === 0 ? { variant: 'empty' as const, title: props.emptyText, body: '' } : undefined
  const common = {
    title: props.title,
    placeholder: props.placeholder,
    placeholderText: props.placeholder,
    open,
    onOpenChange: (next: boolean) => {
      setOpen(next)
      if (!next) setFilter('')
    },
    filterValue: filter,
    onFilterChange: setFilter,
    items,
    message,
    renderAnchor: ({ children: _children, ...anchorProps }: React.HTMLAttributes<HTMLElement> & { children?: ReactNode }) =>
      props.renderAnchor(anchorProps) as React.ReactElement,
    overlayProps: { width: 'medium' as const, maxHeight: 'medium' as const },
  }

  if (props.multiple) {
    const { value, onChange } = props
    return (
      <SelectPanel
        {...common}
        selected={value.map(byId).filter((item): item is PanelItem => Boolean(item))}
        onSelectedChange={(selected: { id?: string | number }[]) => onChange(selected.map((item) => String(item.id)))}
      />
    )
  }
  const { value, onChange } = props
  return (
    <SelectPanel
      {...common}
      selected={byId(value)}
      onSelectedChange={(selected: { id?: string | number } | undefined) => {
        onChange(selected?.id === undefined ? '' : String(selected.id))
        setOpen(false)
        setFilter('')
      }}
    />
  )
}

// MemberPanel picks several members. With a title-row gear anchor it mirrors
// GitHub's sidebar "Reviewers ⚙"; otherwise it renders a plain button.
export function MemberPanel({
  title,
  anchorLabel,
  members,
  selected,
  onChange,
  disabled,
  anchor = 'gear',
}: {
  title: string
  anchorLabel: string
  members: OrgMemberResponse[]
  selected: string[]
  onChange: (userIds: string[]) => void
  disabled?: boolean
  anchor?: 'gear' | 'button'
}) {
  return (
    <PickerPanel
      multiple
      title={title}
      placeholder="Type or choose a name"
      emptyText="No matching members"
      options={memberOptions(members)}
      value={selected}
      onChange={onChange}
      renderAnchor={({ 'aria-labelledby': _labelledBy, ...props }) =>
        anchor === 'gear' ? (
          <IconButton {...props} icon={GearIcon} size="small" variant="invisible" aria-label={anchorLabel} disabled={disabled} />
        ) : (
          <Button {...props} size="small" trailingVisual={TriangleDownIcon} disabled={disabled}>
            {anchorLabel}
          </Button>
        )
      }
    />
  )
}

// FilterButton is a GitHub-style list filter ("Author ▾"); picking the chosen
// option again clears it.
export function FilterButton({
  label,
  title,
  options,
  value,
  onChange,
}: {
  label: string
  title: string
  options: PickerOption[]
  value: string
  onChange: (id: string) => void
}) {
  const current = options.find((option) => option.id === value)
  return (
    <PickerPanel
      title={title}
      placeholder={`Filter ${label.toLowerCase()}`}
      emptyText="No matches"
      options={options}
      value={value}
      onChange={onChange}
      renderAnchor={(props) => (
        <Button {...props} variant="invisible" trailingVisual={TriangleDownIcon} aria-label={`Filter by ${label.toLowerCase()}`}>
          {current ? (
            <>
              {label}: <strong>{current.text}</strong>
            </>
          ) : (
            label
          )}
        </Button>
      )}
    />
  )
}

// BranchPicker chooses one branch for a form field.
export function BranchPicker({
  label,
  branches,
  value,
  onChange,
}: {
  label: string
  branches: BranchResponse[]
  value: string
  onChange: (name: string) => void
}) {
  return (
    <PickerPanel
      title={label}
      placeholder="Find a branch"
      emptyText="No matching branches"
      options={branchOptions(branches)}
      value={value}
      onChange={onChange}
      renderAnchor={(props) => (
        <Button {...props} leadingVisual={GitBranchIcon} trailingVisual={TriangleDownIcon} aria-label={label}>
          {value || 'Choose a branch'}
        </Button>
      )}
    />
  )
}

export function MemberMultiPicker({
  label,
  members,
  selected,
  onChange,
  disabled,
}: {
  label: string
  members: OrgMemberResponse[]
  selected: string[]
  onChange: (userIds: string[]) => void
  disabled?: boolean
}) {
  const chosen = selected
    .map((id) => members.find((member) => member.user_id === id))
    .filter((member): member is OrgMemberResponse => Boolean(member))
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, alignItems: 'flex-start' }}>
      {chosen.map((member) => (
        <div key={member.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, width: '100%', maxWidth: 360 }}>
          <ActorAvatar actor={member} size={20} />
          <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>{memberLabel(member)}</span>
          <IconButton
            size="small"
            variant="invisible"
            aria-label={`remove ${memberLabel(member)}`}
            disabled={disabled}
            onClick={() => onChange(selected.filter((id) => id !== member.user_id))}
            icon={XIcon}
          />
        </div>
      ))}
      <MemberPanel
        title={label}
        anchorLabel={label}
        anchor="button"
        members={members}
        selected={selected}
        onChange={onChange}
        disabled={disabled}
      />
    </div>
  )
}
