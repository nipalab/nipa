import { useState } from 'react'
import { Button, FormControl, Label, Link as PrimerLink, Text, TextInput } from '@primer/react'
import { FileDirectoryIcon, FileIcon, GitBranchIcon, GitPullRequestIcon, LockIcon, SearchIcon, UnlockIcon } from '@primer/octicons-react'
import { Link, useParams } from 'react-router-dom'
import { listFileLocks, lockFile, unlockFile } from '../api/endpoints'
import type { BranchResponse, FileLockResponse } from '../api/models'
import { useAuth } from '../auth'
import { ActorAvatar } from '../components/ActorAvatar'
import { useConfirmAction } from '../components/confirm'
import { FormDialog } from '../components/FormDialog'
import { absoluteTime, relativeTime } from '../components/repo/format'
import { BranchPicker } from '../components/repo/MemberPicker'
import { blobUrl, repoUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { useAction, useAsync } from '../hooks'

type LockFilter = 'all' | 'mine'

export function looksLikeFile(path: string): boolean {
  const last = path.split('/').filter(Boolean).pop() ?? ''
  return /\.[^.]+$/.test(last)
}

function LockFileDialog({
  org,
  project,
  branches,
  defaultBranch,
  onClose,
  onLocked,
}: {
  org: string
  project: string
  branches: BranchResponse[]
  defaultBranch: string
  onClose: () => void
  onLocked: () => void
}) {
  const [path, setPath] = useState('')
  const [branch, setBranch] = useState('')
  const scope = branch || defaultBranch
  const cleaned = path.trim().replace(/^\/+|\/+$/g, '')
  return (
    <FormDialog
      title="Lock a file or folder"
      submitLabel="Lock"
      submitDisabled={cleaned === ''}
      onClose={onClose}
      onSubmit={async () => {
        await lockFile(org, project, cleaned, branch)
        onLocked()
      }}
    >
      <FormControl required>
        <FormControl.Label>Path</FormControl.Label>
        <TextInput
          block
          autoFocus
          leadingVisual={() => <span style={{ color: 'var(--fgColor-muted)' }}>/</span>}
          placeholder="e.g. assets/textures/orc.png or assets/textures"
          value={path}
          onChange={(event) => setPath(event.target.value)}
        />
        <FormControl.Caption>Locking a folder covers every file under it.</FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Branch</FormControl.Label>
        <BranchPicker label="Choose the lock branch" branches={branches} value={scope} onChange={setBranch} />
        <FormControl.Caption>
          {scope === defaultBranch
            ? 'A lock on the default branch is a mainline lock and applies across the project.'
            : `The lock only applies to pushes to ${scope}.`}
        </FormControl.Caption>
      </FormControl>
    </FormDialog>
  )
}

export default function LocksPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { me } = useAuth()
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { data: locks, error, loading, reload } = useAsync(() => listFileLocks(org, project), [org, project])
  const { run, error: actionError, clearError } = useAction(reload)
  const [locking, setLocking] = useState(false)
  const [filter, setFilter] = useState<LockFilter>('all')
  const [query, setQuery] = useState('')

  async function handleUnlock(lock: FileLockResponse, force: boolean) {
    const ok = await confirm({
      title: force ? `Force unlock ${lock.path}?` : `Unlock ${lock.path}?`,
      content: force
        ? `${lock.held_by_name || 'The holder'} loses the lock immediately; their unpushed edits to it may conflict.`
        : `${lock.path} becomes editable by anyone with write access.`,
      confirmLabel: force ? 'Force unlock' : 'Unlock',
    })
    if (ok) await run(() => unlockFile(org, project, lock.path, lock.global ? '' : (lock.branch ?? '')))
  }

  const all = locks ?? []
  const mine = all.filter((lock) => lock.held_by === me?.id)
  const needle = query.trim().toLowerCase()
  const visible = (filter === 'mine' ? mine : all).filter(
    (lock) => !needle || lock.path.toLowerCase().includes(needle) || (lock.held_by_name ?? '').toLowerCase().includes(needle),
  )
  const tabs: { key: LockFilter; label: string; count: number }[] = [
    { key: 'all', label: 'All locks', count: all.length },
    { key: 'mine', label: 'Mine', count: mine.length },
  ]

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="locks"
      rev={defaultBranch}
      canAdmin={canAdmin}
      heading="File locks"
      actions={
        canWrite && (
          <Button variant="primary" leadingVisual={LockIcon} onClick={() => setLocking(true)}>
            Lock file
          </Button>
        )
      }
    >
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)' }}>
        Binary files can only be changed by whoever holds their lock. Locks are released automatically when the change is pushed.
      </Text>
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <TextInput
        block
        leadingVisual={SearchIcon}
        aria-label="Search locks"
        placeholder="Filter by path or holder…"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="nipa-list-box">
        <div className="nipa-list-header">
          <nav className="nipa-state-tabs" aria-label="Lock filter">
            {tabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                aria-pressed={filter === tab.key}
                className={filter === tab.key ? 'nipa-state-tab nipa-state-tab-active' : 'nipa-state-tab'}
                onClick={() => setFilter(tab.key)}
              >
                <LockIcon />
                {tab.label}
                <span className="nipa-count">{tab.count}</span>
              </button>
            ))}
          </nav>
        </div>
        {loading && <Loading />}
        {!loading && visible.length === 0 && (
          <EmptyState bordered={false}>
            {all.length === 0
              ? 'No locks. Tracked binary files can only change while you hold a lock on them.'
              : filter === 'mine' && !needle
                ? 'You hold no locks.'
                : 'No locks match these filters.'}
          </EmptyState>
        )}
        {visible.map((lock) => {
          const own = lock.held_by === me?.id
          const branch = lock.global ? defaultBranch : (lock.branch ?? defaultBranch)
          const file = looksLikeFile(lock.path)
          return (
            <div key={lock.id} className="nipa-list-row" style={{ alignItems: 'center' }}>
              <span className="nipa-list-row-icon" style={{ color: own ? 'var(--fgColor-accent)' : 'var(--fgColor-attention)' }}>
                {file ? <FileIcon /> : <FileDirectoryIcon />}
              </span>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <PrimerLink
                    as={Link}
                    to={file ? blobUrl(org, project, branch, lock.path) : treeUrl(org, project, branch, lock.path)}
                    className="nipa-branch-name"
                  >
                    {lock.path}
                  </PrimerLink>
                  {lock.global ? (
                    <Label variant="secondary">Mainline</Label>
                  ) : (
                    <Label variant="secondary">
                      <GitBranchIcon size={12} /> {lock.branch}
                    </Label>
                  )}
                  {lock.merge_request_number != null && (
                    <PrimerLink as={Link} to={`${repoUrl(org, project)}/merges/${lock.merge_request_number}`}>
                      <Label variant="accent">
                        <GitPullRequestIcon size={12} /> MR #{lock.merge_request_number}
                      </Label>
                    </PrimerLink>
                  )}
                </div>
                <div className="nipa-list-row-meta nipa-commit-row-meta">
                  <ActorAvatar actor={{ user_id: lock.held_by, name: lock.held_by_name || lock.held_by }} size={16} />
                  <strong>{own ? 'You' : lock.held_by_name || lock.held_by}</strong> locked{' '}
                  <span title={absoluteTime(lock.acquired_at)}>{relativeTime(lock.acquired_at)}</span>
                  {lock.expires_at && (
                    <>
                      {' · expires '}
                      <span title={absoluteTime(lock.expires_at)}>{relativeTime(lock.expires_at)}</span>
                    </>
                  )}
                </div>
              </div>
              {own && (
                <Button size="small" leadingVisual={UnlockIcon} onClick={() => void handleUnlock(lock, false)}>
                  Unlock
                </Button>
              )}
              {!own && canAdmin && (
                <Button size="small" variant="danger" leadingVisual={UnlockIcon} onClick={() => void handleUnlock(lock, true)}>
                  Force unlock
                </Button>
              )}
            </div>
          )
        })}
      </div>
      {locking && (
        <LockFileDialog
          org={org}
          project={project}
          branches={branches ?? []}
          defaultBranch={defaultBranch}
          onClose={() => setLocking(false)}
          onLocked={reload}
        />
      )}
    </RepoPageShell>
  )
}
