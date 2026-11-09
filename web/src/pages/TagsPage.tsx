import { useState } from 'react'
import { Button, FormControl, IconButton, Label, Link as PrimerLink, Textarea, TextInput } from '@primer/react'
import { FileCodeIcon, GitCommitIcon, TagIcon, TrashIcon } from '@primer/octicons-react'
import { Link, useParams } from 'react-router-dom'
import { createTag, deleteTag, listTags } from '../api/endpoints'
import type { BranchResponse, TagResponse } from '../api/models'
import { useConfirmAction } from '../components/confirm'
import { CopyButton } from '../components/CopyButton'
import { FormDialog } from '../components/FormDialog'
import { LoadMore } from '../components/LoadMore'
import { commitUrl, treeUrl } from '../components/repo/repoPaths'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { absoluteTime, relativeTime, shortSha } from '../components/repo/format'
import { SourcePicker } from '../components/repo/SourcePicker'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading } from '../components/ui'
import { sliceCursorPage, useAction, usePaged } from '../hooks'

const PAGE_SIZE = 50

function CreateTagDialog({
  org,
  project,
  branches,
  defaultBranch,
  onClose,
  onCreated,
}: {
  org: string
  project: string
  branches: BranchResponse[]
  defaultBranch: string
  onClose: () => void
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [from, setFrom] = useState('')
  const [message, setMessage] = useState('')
  const invalid = /\s/.test(name.trim())
  return (
    <FormDialog
      title="Create a new tag"
      submitLabel="Create tag"
      submitDisabled={name.trim() === '' || invalid}
      onClose={onClose}
      onSubmit={async () => {
        await createTag(org, project, name.trim(), from.trim(), message)
        onCreated()
      }}
    >
      <FormControl required>
        <FormControl.Label>Tag name</FormControl.Label>
        <TextInput
          block
          autoFocus
          leadingVisual={TagIcon}
          placeholder="e.g. v1.0.0"
          value={name}
          validationStatus={invalid ? 'error' : undefined}
          onChange={(event) => setName(event.target.value)}
        />
        {invalid ? (
          <FormControl.Validation variant="error">Tag names cannot contain spaces.</FormControl.Validation>
        ) : (
          <FormControl.Caption>Tags are immutable: once created, a tag always points to the same commit.</FormControl.Caption>
        )}
      </FormControl>
      <SourcePicker label="Target" branches={branches} defaultBranch={defaultBranch} onChange={setFrom} />
      <FormControl>
        <FormControl.Label>Release notes (optional)</FormControl.Label>
        <Textarea block rows={4} placeholder="What changed in this release?" value={message} onChange={(event) => setMessage(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

export default function TagsPage() {
  const { org = '', project = '' } = useParams()
  const confirm = useConfirmAction()
  const { branches, canWrite, canAdmin, defaultBranch } = useRepoChrome(org, project)
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

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="tags"
      rev={defaultBranch}
      canAdmin={canAdmin}
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
      {loading && <Loading />}
      {!loading && items.length === 0 && (
        <EmptyState>
          <TagIcon size={24} />
          <div style={{ marginTop: 8, fontWeight: 600 }}>There aren’t any tags yet</div>
          <div>Tags mark releases and other important points in history.</div>
        </EmptyState>
      )}
      {items.length > 0 && (
        <div className="nipa-list-box">
          {items.map((tag) => (
            <div key={tag.id} className="nipa-list-row">
              <span className="nipa-list-row-icon" style={{ color: 'var(--fgColor-muted)' }}>
                <TagIcon />
              </span>
              <div style={{ minWidth: 0, flex: 1 }}>
                <PrimerLink as={Link} to={treeUrl(org, project, tag.commit_id)} className="nipa-list-row-title">
                  {tag.name}
                </PrimerLink>
                {tag.message && <p className="nipa-tag-message">{tag.message}</p>}
                <div className="nipa-list-row-meta nipa-commit-row-meta">
                  <span title={absoluteTime(tag.created_at)}>{relativeTime(tag.created_at)}</span>
                  <span>·</span>
                  <GitCommitIcon size={12} />
                  <PrimerLink as={Link} to={commitUrl(org, project, tag.commit_id)} className="nipa-mono-link">
                    {shortSha(tag.commit_id, 7)}
                  </PrimerLink>
                  <CopyButton value={tag.name} label={`Copy tag name ${tag.name}`} />
                </div>
              </div>
              <Button as={Link} to={treeUrl(org, project, tag.commit_id)} size="small" leadingVisual={FileCodeIcon}>
                Browse files
              </Button>
              {canWrite && (
                <IconButton
                  icon={TrashIcon}
                  variant="invisible"
                  aria-label={`Delete tag ${tag.name}`}
                  onClick={() => void remove(tag)}
                />
              )}
            </div>
          ))}
        </div>
      )}
      <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
      {creating && (
        <CreateTagDialog
          org={org}
          project={project}
          branches={branches ?? []}
          defaultBranch={defaultBranch}
          onClose={() => setCreating(false)}
          onCreated={reload}
        />
      )}
    </RepoPageShell>
  )
}
