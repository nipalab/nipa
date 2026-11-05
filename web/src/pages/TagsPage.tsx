import { useState } from 'react'
import { Button, FormControl, Label, Link as PrimerLink, Textarea, TextInput } from '@primer/react'
import { TagIcon } from '@primer/octicons-react'
import { Link, useParams } from 'react-router-dom'
import { createTag, deleteTag, listTags } from '../api/endpoints'
import type { TagResponse } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { DataTable, type Column } from '../components/DataTable'
import { FormDialog } from '../components/FormDialog'
import { LoadMore } from '../components/LoadMore'
import { commitUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { shortSha } from '../components/repo/format'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { Timestamp } from '../components/Timestamp'
import { ErrorBanner, Mono } from '../components/ui'
import { sliceCursorPage, useAction, usePaged } from '../hooks'

const PAGE_SIZE = 50

function CreateTagDialog({
  org,
  project,
  defaultBranch,
  onClose,
  onCreated,
}: {
  org: string
  project: string
  defaultBranch: string
  onClose: () => void
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [message, setMessage] = useState('')
  return (
    <FormDialog
      title="New tag"
      submitLabel="Create tag"
      submitDisabled={name.trim() === ''}
      onClose={onClose}
      onSubmit={async () => {
        await createTag(org, project, name.trim(), from.trim(), message)
        onCreated()
      }}
    >
      <FormControl required>
        <FormControl.Label>Tag name</FormControl.Label>
        <TextInput block autoFocus placeholder="v1.0.0" value={name} onChange={(event) => setName(event.target.value)} />
        <FormControl.Caption>Tags are immutable: once created they always point to the same commit.</FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Target (optional)</FormControl.Label>
        <TextInput block value={from} onChange={(event) => setFrom(event.target.value)} />
        <FormControl.Caption>
          Branch name or commit id. Tags the head of {defaultBranch || 'the default branch'} when empty.
        </FormControl.Caption>
      </FormControl>
      <FormControl>
        <FormControl.Label>Message (optional)</FormControl.Label>
        <Textarea block rows={3} value={message} onChange={(event) => setMessage(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

export default function TagsPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
  const { items, loading, error, hasMore, loadingMore, loadMore, reload } = usePaged<TagResponse, TagResponse>(
    async (after) => sliceCursorPage(await listTags(org, project, { limit: PAGE_SIZE + 1, after }), PAGE_SIZE, (rows) => rows[PAGE_SIZE - 1]),
    [org, project],
  )
  const { run, error: actionError, clearError } = useAction(reload)
  const [creating, setCreating] = useState(false)

  async function remove(tag: TagResponse) {
    const ok = await confirm({
      title: `Delete tag ${tag.name}?`,
      content: 'The commit stays in the history, but the tag name becomes free to reuse.',
      confirmLabel: 'Delete tag',
    })
    if (ok) await run(() => deleteTag(org, project, tag.name))
  }

  const columns: Column<TagResponse>[] = [
    {
      id: 'name',
      header: 'Tag',
      rowHeader: true,
      renderCell: (tag) => (
        <PrimerLink as={Link} to={treeUrl(org, project, tag.commit_id)}>
          {tag.name}
        </PrimerLink>
      ),
    },
    { id: 'message', header: 'Message', renderCell: (tag) => tag.message || '—' },
    {
      id: 'commit',
      header: 'Commit',
      renderCell: (tag) => (
        <PrimerLink as={Link} to={commitUrl(org, project, tag.commit_id)}>
          <Mono>{shortSha(tag.commit_id, 10)}</Mono>
        </PrimerLink>
      ),
    },
    { id: 'created', header: 'Created', renderCell: (tag) => <Timestamp value={tag.created_at} /> },
    ...(canWrite
      ? [
          {
            id: 'actions',
            header: '',
            align: 'end' as const,
            renderCell: (tag: TagResponse) => (
              <Button size="small" variant="danger" onClick={() => void remove(tag)}>
                Delete
              </Button>
            ),
          },
        ]
      : []),
  ]

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="tags"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading={
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--base-size-8)' }}>
          Tags {items.length > 0 && <Label>{hasMore ? `${items.length}+` : items.length}</Label>}
        </span>
      }
      actions={
        canWrite && (
          <Button variant="primary" leadingVisual={TagIcon} onClick={() => setCreating(true)}>
            New tag
          </Button>
        )
      }
    >
      <ErrorBanner error={actionError ?? error} onDismiss={actionError ? clearError : undefined} />
      <DataTable
        label="Tags"
        loading={loading}
        data={items}
        columns={columns}
        empty="No tags yet. Tags mark releases and other points in history."
      />
      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
      {creating && (
        <CreateTagDialog
          org={org}
          project={project}
          defaultBranch={defaultBranch}
          onClose={() => setCreating(false)}
          onCreated={reload}
        />
      )}
    </RepoPageShell>
  )
}
