import { useEffect, useRef, useState } from 'react'
import { Button, Stack, Text, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { listGroups, listOrgMembers, listUsers } from '../api/endpoints'
import type { OrgMemberResponse } from '../api/models'
import { ActorAvatar } from './repo/ActorAvatar'

type PickerKind = 'user' | 'group' | 'user-or-group'

export interface PickerEntry {
  id: string
  label: string
  sublabel: string
  avatarUrl?: string
  isGroup: boolean
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
    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (!query.trim()) {
      setEntries([])
      return
    }
    debounceRef.current = setTimeout(async () => {
      setLoading(true)
      try {
        const q = query.toLowerCase()
        const results: PickerEntry[] = []

        if (kind === 'user' || kind === 'user-or-group') {
          const [usersResult, membersResult] = await Promise.allSettled([listUsers(), listOrgMembers(org)])
          setSearchable(usersResult.status === 'fulfilled' || membersResult.status === 'fulfilled')
          const seen = new Set<string>()
          const members = membersResult.status === 'fulfilled' ? membersResult.value : []
          const memberMap = new Map<string, OrgMemberResponse>()
          for (const m of members) {
            memberMap.set(m.user_id, m)
            if (
              m.name.toLowerCase().includes(q) ||
              m.email.toLowerCase().includes(q) ||
              m.user_id.includes(q)
            ) {
              seen.add(m.user_id)
              results.push({
                id: m.user_id,
                label: m.name,
                sublabel: m.email,
                avatarUrl: m.photo_url,
                isGroup: false,
              })
            }
          }
          if (usersResult.status === 'fulfilled') {
            for (const u of usersResult.value) {
              if (seen.has(u.id)) continue
              const m = memberMap.get(u.id)
              const name = m?.name || u.name
              const email = m?.email || u.email
              if (name.toLowerCase().includes(q) || email.toLowerCase().includes(q) || u.id.includes(q)) {
                results.push({
                  id: u.id,
                  label: name,
                  sublabel: email,
                  avatarUrl: u.photo_url,
                  isGroup: false,
                })
              }
            }
          }
        }

        if (kind === 'group' || kind === 'user-or-group') {
          const groupsResult = await Promise.allSettled([listGroups(org)])
          const groups = groupsResult[0].status === 'fulfilled' ? groupsResult[0].value : []
          if (groupsResult[0].status === 'fulfilled') setSearchable(true)
          else if (kind === 'group') setSearchable(false)
          for (const g of groups) {
            if (g.name.toLowerCase().includes(q) || g.id.includes(q)) {
              results.push({
                id: g.id,
                label: g.name,
                sublabel: g.description || 'Group',
                isGroup: true,
              })
            }
          }
        }

        setEntries(results.slice(0, 10))
        setHighlight(0)
      } finally {
        setLoading(false)
      }
    }, 200)
    return () => {
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
            <select
              value={manualIsGroup ? 'group' : 'user'}
              onChange={(e) => setManualIsGroup(e.target.value === 'group')}
              aria-label="Subject type"
              style={{ padding: 6 }}
            >
              <option value="user">User</option>
              <option value="group">Group</option>
            </select>
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
        <button
          type="button"
          onClick={() => setManual(false)}
          style={{ background: 'none', border: 'none', padding: 0, color: 'var(--fgColor-accent)', cursor: 'pointer', alignSelf: 'flex-start' }}
        >
          Back to search
        </button>
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
            borderRadius: 6,
            boxShadow: '0 8px 24px rgba(0,0,0,0.12)',
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
              <button
                type="button"
                onClick={() => {
                  setManual(true)
                  setOpen(false)
                }}
                style={{ background: 'none', border: 'none', padding: 0, color: 'var(--fgColor-accent)', cursor: 'pointer' }}
              >
                Enter an ID manually
              </button>
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
              <button
                key={`${entry.isGroup ? 'g' : 'u'}-${entry.id}`}
                type="button"
                onClick={() => select(entry)}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 10,
                  width: '100%',
                  padding: '8px 12px',
                  background: i === highlight ? 'var(--bgColor-muted)' : 'transparent',
                  border: 'none',
                  cursor: 'pointer',
                  textAlign: 'left',
                }}
              >
                <ActorAvatar
                  actor={{ user_id: entry.id, name: entry.label, photo_url: entry.avatarUrl }}
                  size={24}
                />
                <div>
                  <div style={{ fontWeight: 600, fontSize: 14 }}>{entry.label}</div>
                  <div style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>{entry.sublabel}</div>
                </div>
              </button>
            ))}
        </div>
      )}
      <button
        type="button"
        onClick={() => setManual(true)}
        style={{
          background: 'none',
          border: 'none',
          padding: 0,
          marginTop: 4,
          color: 'var(--fgColor-accent)',
          cursor: 'pointer',
          fontSize: 12,
        }}
      >
        Enter an ID manually
      </button>
    </div>
  )
}
