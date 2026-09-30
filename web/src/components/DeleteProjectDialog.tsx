import { useEffect, useState } from 'react'
import { Banner, Button, Dialog, FormControl, Stack, Text, TextInput } from '@primer/react'
import { deleteProject } from '../api/endpoints'
import type { ProjectResponse } from '../api/models'
import { Mono } from './ui'

export function DeleteProjectDialog({
  org,
  project,
  onClose,
  onDeleted,
}: {
  org: string
  project: ProjectResponse | null
  onClose: () => void
  onDeleted: () => void
}) {
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setTyped('')
    setError(null)
    setLoading(false)
  }, [project])

  if (!project) return null

  const current = project
  const matches = typed.trim() === current.slug

  async function handleDelete() {
    if (!matches || loading) return
    setLoading(true)
    setError(null)
    try {
      await deleteProject(org, current.slug)
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setLoading(false)
    }
  }

  return (
    <Dialog title={`Delete ${current.name}`} onClose={onClose} width="large">
      <Dialog.Body>
        <Stack direction="vertical" gap="normal">
          {error && (
            <Banner variant="critical" title="Delete failed">
              {error}
            </Banner>
          )}
          <Text as="p">
            This deletes the repository <strong>{current.name}</strong> and makes it inaccessible. The slug{' '}
            <Mono>{current.slug}</Mono> cannot be reused after deletion.
          </Text>
          <FormControl required>
            <FormControl.Label>
              Type <Mono>{current.slug}</Mono> to confirm
            </FormControl.Label>
            <TextInput
              block
              autoFocus
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  void handleDelete()
                }
              }}
            />
          </FormControl>
        </Stack>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button
          variant="danger"
          disabled={!matches || loading}
          loading={loading}
          onClick={() => void handleDelete()}
        >
          Delete project
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
