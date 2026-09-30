import { useEffect, useState } from 'react'
import { Banner, Button, Dialog, Stack, Text } from '@primer/react'
import { deleteBranch } from '../api/endpoints'
import type { BranchResponse } from '../api/models'

export function DeleteBranchDialog({
  org,
  project,
  branch,
  onClose,
  onDeleted,
}: {
  org: string
  project: string
  branch: BranchResponse | null
  onClose: () => void
  onDeleted: () => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setError(null)
    setLoading(false)
  }, [branch])

  if (!branch) return null

  const current = branch

  async function handleDelete() {
    if (loading) return
    setLoading(true)
    setError(null)
    try {
      await deleteBranch(org, project, current.name)
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setLoading(false)
    }
  }

  return (
    <Dialog title={`Delete branch ${current.name}`} onClose={onClose} width="medium">
      <Dialog.Body>
        <Stack direction="vertical" gap="normal">
          {error && (
            <Banner variant="critical" title="Delete failed">
              {error}
            </Banner>
          )}
          <Text as="p">
            This deletes the branch <strong>{current.name}</strong>. Commits reachable only from this branch become
            inaccessible.
          </Text>
        </Stack>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button variant="danger" loading={loading} onClick={() => void handleDelete()}>
          Delete branch
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
