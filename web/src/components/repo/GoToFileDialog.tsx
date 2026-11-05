import { Button, Dialog, TextInput } from '@primer/react'
import { SearchIcon } from '@primer/octicons-react'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { getTree } from '../../api/endpoints'
import { useAsync } from '../../hooks'
import { EmptyState, ErrorBanner, Loading } from '../ui'
import { fileIcon } from './fileIcon'
import { blobUrl } from './repoPaths'

const MAX_RESULTS = 100

export function GoToFileDialog({
  org,
  project,
  rev,
  path,
  open,
  onClose,
}: {
  org: string
  project: string
  rev: string
  path: string
  open: boolean
  onClose: () => void
}) {
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const { data, error, loading } = useAsync(
    () => (open ? getTree(org, project, rev, path, { recursive: true }) : Promise.resolve(null)),
    [open, org, project, rev, path],
  )
  const files = data?.entries ?? []
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return files.slice(0, MAX_RESULTS)
    return files.filter((file) => file.path.toLowerCase().includes(needle)).slice(0, MAX_RESULTS)
  }, [files, query])

  if (!open) return null

  const openFile = (filePath: string) => {
    onClose()
    navigate(blobUrl(org, project, rev, filePath))
  }

  return (
    <Dialog title="Go to file" onClose={onClose} width="large">
      <Dialog.Body>
        <TextInput
          block
          autoFocus
          aria-label="Search files by path"
          leadingVisual={SearchIcon}
          placeholder="Search files by path"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <div style={{ marginTop: 'var(--base-size-12)', maxHeight: 400, overflowY: 'auto' }}>
          {loading && <Loading />}
          <ErrorBanner error={error} />
          {!loading &&
            filtered.map((file) => {
              const { Icon, color } = fileIcon(file.name, 'file')
              return (
                <Button
                  key={file.path}
                  variant="invisible"
                  block
                  alignContent="start"
                  leadingVisual={() => (
                    <span style={{ color, display: 'inline-flex' }}>
                      <Icon size={16} />
                    </span>
                  )}
                  onClick={() => openFile(file.path)}
                >
                  {file.path}
                </Button>
              )
            })}
          {!loading && !error && filtered.length === 0 && <EmptyState bordered={false}>No files found.</EmptyState>}
        </div>
      </Dialog.Body>
    </Dialog>
  )
}
