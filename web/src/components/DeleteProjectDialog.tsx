import { useEffect, useState } from 'react'
import { FormControl, Text, TextInput } from '@primer/react'
import { deleteProject } from '../api/endpoints'
import type { ProjectResponse } from '../api/models'
import { FormDialog } from './FormDialog'
import { Mono } from './ui'

function Body({
  org,
  project,
  onClose,
  onDeleted,
}: {
  org: string
  project: ProjectResponse
  onClose: () => void
  onDeleted: () => void
}) {
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === project.slug

  useEffect(() => setTyped(''), [project])

  return (
    <FormDialog
      title={`Delete ${project.name}`}
      width="large"
      submitLabel="Delete project"
      submitVariant="danger"
      submitDisabled={!matches}
      onClose={onClose}
      onSubmit={async () => {
        await deleteProject(org, project.slug)
        onDeleted()
      }}
    >
      <Text as="p">
        This deletes the repository <strong>{project.name}</strong> and makes it inaccessible. The slug{' '}
        <Mono>{project.slug}</Mono> cannot be reused after deletion.
      </Text>
      <FormControl required>
        <FormControl.Label>
          Type <Mono>{project.slug}</Mono> to confirm
        </FormControl.Label>
        <TextInput block autoFocus value={typed} onChange={(event) => setTyped(event.target.value)} />
      </FormControl>
    </FormDialog>
  )
}

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
  if (!project) return null
  return <Body key={project.id} org={org} project={project} onClose={onClose} onDeleted={onDeleted} />
}
