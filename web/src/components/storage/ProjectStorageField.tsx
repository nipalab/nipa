import { useEffect, useState } from 'react'
import { FormControl, Select, Text } from '@primer/react'
import { getOrgStorageServer, listStorageServers } from '../../api/endpoints'
import type { StorageServerResponse, StorageServerSummary } from '../../api/models'
import { useAsync } from '../../hooks'
import { useByoStorage } from './storage'

export interface ProjectStorageChoice {
  enabled: boolean
  servers: StorageServerResponse[]
  orgDefault: StorageServerSummary | null
  value: string
  setValue: (value: string) => void
}

export function useProjectStorageChoice(org: string): ProjectStorageChoice {
  const { enabled } = useByoStorage()
  const { data } = useAsync(async () => {
    if (!enabled) return null
    const [servers, orgDefault] = await Promise.all([
      listStorageServers().catch(() => [] as StorageServerResponse[]),
      getOrgStorageServer(org).catch(() => null),
    ])
    return { servers, orgDefault: orgDefault?.server ?? null }
  }, [enabled, org])
  const servers = data?.servers ?? []
  const orgDefault = data?.orgDefault ?? null
  const preferred = orgDefault && servers.some((server) => server.id === orgDefault.id) ? orgDefault.id : ''
  const [value, setValue] = useState('')

  useEffect(() => setValue(preferred), [preferred])

  return { enabled, servers, orgDefault, value, setValue }
}

export function ProjectStorageField({ choice }: { choice: ProjectStorageChoice }) {
  const { enabled, servers, orgDefault, value, setValue } = choice
  if (!enabled) return null
  if (servers.length === 0) {
    return orgDefault ? (
      <Text as="p" style={{ margin: 0, color: 'var(--fgColor-muted)', fontSize: 14 }}>
        Content will be stored on {orgDefault.name}, the organization&apos;s default storage.
      </Text>
    ) : null
  }
  const ownsDefault = orgDefault !== null && servers.some((server) => server.id === orgDefault.id)
  return (
    <FormControl>
      <FormControl.Label>Storage</FormControl.Label>
      <Select block value={value} onChange={(event) => setValue(event.target.value)}>
        {!ownsDefault && (
          <Select.Option value="">
            {orgDefault ? `Organization default (${orgDefault.name})` : 'Nipa storage'}
          </Select.Option>
        )}
        {servers.map((server) => (
          <Select.Option key={server.id} value={server.id}>
            {server.id === orgDefault?.id ? `${server.name} (organization default)` : server.name}
          </Select.Option>
        ))}
      </Select>
      <FormControl.Caption>
        Where this repository keeps its content. It cannot be changed after the repository is created.
      </FormControl.Caption>
    </FormControl>
  )
}
