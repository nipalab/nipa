import { useEffect, useRef, useState } from 'react'
import { Button, Select, Stack, Text, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { listGroups, listUsers } from '../api/endpoints'
import type { GroupResponse, OrgMemberResponse, UserResponse } from '../api/models'
import { fetchOrgMembers } from '../orgHooks'
import { ActorAvatar } from './ActorAvatar'

type PickerKind = 'user' | 'group' | 'user-or-group'

export interface PickerEntry {
  id: string
  label: string
  sublabel: string
  avatarUrl?: string
  isGroup: boolean
}

interface Directory {
  users: UserResponse[] | null
  members: OrgMemberResponse[] | null
  groups: GroupResponse[] | null
  searchable: boolean
}

async function loadDirectory(org: string, kind: PickerKind): Promise<Directory> {
  const wantsUsers = kind === 'user' || kind === 'user-or-group'
  const wantsGroups = kind === 'group' || kind === 'user-or-group'
  const [users, members, groups] = await Promise.allSettled([
    wantsUsers ? listUsers() : Promise.resolve(null),
    wantsUsers ? fetchOrgMembers(org) : Promise.resolve(null),
    wantsGroups ? listGroups(org) : Promise.resolve(null),
  ])
  const usersOk = users.status === 'fulfilled' && users.value !== null
  const membersOk = members.status === 'fulfilled' && members.value !== null
  const groupsOk = groups.status === 'fulfilled' && groups.value !== null
  return {
    users: users.status === 'fulfilled' ? users.value : null,
    members: members.status === 'fulfilled' ? members.value : null,
    groups: groups.status === 'fulfilled' ? groups.value : null,
    searchable: usersOk || membersOk || groupsOk,
  }
}

function filterDirectory(directory: Directory, query: string): PickerEntry[] {
  const q = query.toLowerCase()
  const results: PickerEntry[] = []
  const seen = new Set<string>()
  const memberMap = new Map<string, OrgMemberResponse>()
  for (const m of directory.members ?? []) {
    memberMap.set(m.user_id, m)
    if (m.name.toLowerCase().includes(q) || m.email.toLowerCase().includes(q) || m.user_id.includes(q)) {
      seen.add(m.user_id)
      results.push({ id: m.user_id, label: m.name, sublabel: m.email, avatarUrl: m.photo_url, isGroup: false })
    }
  }
  for (const u of directory.users ?? []) {
    if (seen.has(u.id)) continue
    const m = memberMap.get(u.id)
    const name = m?.name || u.name
    const email = m?.email || u.email
    if (name.toLowerCase().includes(q) || email.toLowerCase().includes(q) || u.id.includes(q)) {
      results.push({ id: u.id, label: name, sublabel: email, avatarUrl: u.photo_url, isGroup: false })
    }
  }
  for (const g of directory.groups ?? []) {
    if (g.name.toLowerCase().includes(q) || g.id.includes(q)) {
      results.push({ id: g.id, label: g.name, sublabel: g.description || 'Group', isGroup: true })
    }
  }
  return results.slice(0, 10)
}

export function UserGroupPicker({
  org,
  kind,
  onSelect,
  placeholder = 'Search users or groups…',
}: {
  org: string
  kind: PickerKind
  onSelect: (entry: PickerEntry) => void
  placeholder?: string
}) {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [entries, setEntries] = useState<PickerEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [searchable, setSearchable] = useState<boolean | null>(null)
  const [highlight, setHighlight] = useState(0)
  const [manual, setManual] = useState(false)
  const [manualId, setManualId] = useState('')
  const [manualIsGroup, setManualIsGroup] = useState(kind === 'group')
  const containerRef = useRef<HTMLDivElement>(null)
  const directoryRef = useRef<Directory | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout>>()

  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  useEffect(() => {
    directoryRef.current = null
  }, [org, kind])

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (!query.trim()) {
      setEntries([])
      return
    }
    let active = true
    debounceRef.current = setTimeout(async () => {
      setLoading(true)
      try {
        const directory = directoryRef.current ?? (await loadDirectory(org, kind))
        if (!active) return
        if (directory.searchable) directoryRef.current = directory
        setSearchable(directory.searchable)
        setEntries(filterDirectory(directory, query.trim()))
        setHighlight(0)
      } finally {
        if (active) setLoading(false)
      }
    }, 200)
    return () => {
      active = false
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [query, org, kind])

  function select(entry: PickerEntry) {
    onSelect(entry)
    setQuery('')
    setOpen(false)
  }

  function handleKeyDown(event: React.KeyboardEvent) {
    if (!open || entries.length === 0) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setHighlight((h) => Math.min(h + 1, entries.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setHighlight((h) => Math.max(h - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      const entry = entries[highlight]
      if (entry) select(entry)
    } else if (event.key === 'Escape') {
      setOpen(false)
    }
  }

  function handleManualSubmit(event: React.FormEvent) {
    event.preventDefault()
    const id = manualId.trim()
    if (!id) return
    select({
      id,
      label: id,
      sublabel: manualIsGroup ? 'Group ID' : 'User ID',
      isGroup: manualIsGroup,
    })
    setManualId('')
    setManual(false)
  }

  if (manual) {
    return (
      <Stack direction="vertical" gap="condensed">
        <form
          onSubmit={handleManualSubmit}
          style={{ display: 'flex', gap: 8, alignItems: 'center' }}
        >
          {kind === 'user-or-group' && (
            <Select
              value={manualIsGroup ? 'group' : 'user'}
              onChange={(e) => setManualIsGroup(e.target.value === 'group')}
              aria-label="Subject type"
            >
              <Select.Option value="user">User</Select.Option>
              <Select.Option value="group">Group</Select.Option>
            </Select>
          )}
          <TextInput
            block
            placeholder={manualIsGroup ? 'Group id (base36)' : 'User id (base36)'}
            value={manualId}
            onChange={(e) => setManualId(e.target.value)}
            aria-label="Subject id"
            style={{ flex: 1 }}
          />
          <Button type="submit" variant="primary" disabled={!manualId.trim()}>
            Use ID
          </Button>
        </form>
        <Button variant="link" onClick={() => setManual(false)} style={{ alignSelf: 'flex-start' }}>
          Back to search
        </Button>
      </Stack>
    )
  }

  return (
    <div ref={containerRef} style={{ position: 'relative' }}>
      <TextInput
        leadingVisual={SearchIcon}
        placeholder={placeholder}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        aria-label={placeholder}
      />
      {open && query.trim() && (
        <div
          style={{
            position: 'absolute',
            top: '100%',
            left: 0,
            right: 0,
            marginTop: 4,
            background: 'var(--bgColor-default)',
            border: '1px solid var(--borderColor-default)',
            borderRadius: 'var(--borderRadius-medium)',
            boxShadow: 'var(--shadow-floating-medium)',
            zIndex: 100,
            maxHeight: 280,
            overflowY: 'auto',
          }}
        >
          {loading && (
            <div style={{ padding: 8 }}>
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                Searching…
              </Text>
            </div>
          )}
          {!loading && searchable === false && (
            <div style={{ padding: 8 }}>
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                Search is unavailable with your permissions.
              </Text>
              <Button
                variant="link"
                onClick={() => {
                  setManual(true)
                  setOpen(false)
                }}
              >
                Enter an ID manually
              </Button>
            </div>
          )}
          {!loading && searchable !== false && entries.length === 0 && (
            <div style={{ padding: 8 }}>
              <Text as="p" style={{ color: 'var(--fgColor-muted)' }}>
                No matches found.
              </Text>
            </div>
          )}
          {!loading &&
            entries.map((entry, i) => (
              <Button
                key={`${entry.isGroup ? 'g' : 'u'}-${entry.id}`}
                variant="invisible"
                block
                alignContent="start"
                onClick={() => select(entry)}
                style={{ height: 'auto', padding: 'var(--base-size-8) var(--base-size-12)', background: i === highlight ? 'var(--bgColor-muted)' : undefined }}
              >
                <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--base-size-12)', textAlign: 'left' }}>
                  <ActorAvatar actor={{ user_id: entry.id, name: entry.label, photo_url: entry.avatarUrl }} size={24} />
                  <span>
                    <span style={{ display: 'block', fontWeight: 600, fontSize: 14 }}>{entry.label}</span>
                    <span style={{ display: 'block', color: 'var(--fgColor-muted)', fontSize: 12, fontWeight: 400 }}>
                      {entry.sublabel}
                    </span>
                  </span>
                </span>
              </Button>
            ))}
        </div>
      )}
      <div style={{ marginTop: 'var(--base-size-4)' }}>
        <Button variant="link" size="small" onClick={() => setManual(true)}>
          Enter an ID manually
        </Button>
      </div>
    </div>
  )
}
