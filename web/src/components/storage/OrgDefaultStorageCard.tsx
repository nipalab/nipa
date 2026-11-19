import { useEffect, useState } from 'react'
import { Banner, Button, FormControl, Select, Stack, Text } from '@primer/react'
import {
  clearOrgStorageServer,
  getOrgStorageServer,
  listStorageServers,
  setOrgStorageServer,
} from '../../api/endpoints'
import type { StorageServerSummary } from '../../api/models'
import { errorMessage, useAsync } from '../../hooks'
import { SettingsCard, SettingsRow } from '../SettingsCard'
import { ErrorBanner, Loading } from '../ui'
import { isOwnStorageError, OwnStorageBanner } from './storage'

export function OrgDefaultStorageCard({ org }: { org: string }) {
  const { data, error, loading, reload } = useAsync(() => getOrgStorageServer(org), [org])
  const canManage = Boolean(data?.can_manage)
  const { data: servers } = useAsync(
    () => (canManage ? listStorageServers().catch(() => []) : Promise.resolve([])),
    [canManage],
  )
  const current = data?.server?.id ?? ''
  const [choice, setChoice] = useState(current)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => setChoice(current), [current])

  async function save() {
    setSaving(true)
    setSaveError(null)
    setSaved(false)
    try {
      if (choice === '') await clearOrgStorageServer(org)
      else await setOrgStorageServer(org, choice)
      setSaved(true)
      reload()
    } catch (err) {
      setSaveError(err)
    } finally {
      setSaving(false)
    }
  }

  const options: StorageServerSummary[] = (servers ?? []).map(({ id, name }) => ({ id, name }))
  if (data?.server && !options.some((server) => server.id === data.server?.id)) options.unshift(data.server)

  return (
    <SettingsCard
      title="Default storage"
      description="Where new repositories in this organization keep their content. Existing repositories keep the storage they were created with."
      footer={
        canManage && (
          <Button variant="primary" loading={saving} disabled={choice === current} onClick={() => void save()}>
            Save default
          </Button>
        )
      }
    >
      {isOwnStorageError(saveError) ? (
        <OwnStorageBanner />
      ) : (
        <ErrorBanner error={saveError ? errorMessage(saveError) : error} title="Could not save" />
      )}
      {saved && <Banner variant="success" title="Default storage saved." onDismiss={() => setSaved(false)} />}
      {loading && !data ? (
        <Loading variant="skeleton" lines={1} />
      ) : (
        <SettingsRow label="New repositories" subtext="Only affects repositories created from now on.">
          {canManage ? (
            <FormControl>
              <FormControl.Label visuallyHidden>Default storage</FormControl.Label>
              <Select block value={choice} onChange={(event) => setChoice(event.target.value)}>
                <Select.Option value="">Nipa storage</Select.Option>
                {options.map((server) => (
                  <Select.Option key={server.id} value={server.id}>
                    {server.name}
                  </Select.Option>
                ))}
              </Select>
              <FormControl.Caption>Choose one of your own storage servers, or Nipa storage.</FormControl.Caption>
            </FormControl>
          ) : (
            <Stack direction="vertical" gap="none">
              <Text style={{ fontWeight: 600 }}>{data?.server ? data.server.name : 'Nipa storage'}</Text>
              <Text style={{ color: 'var(--fgColor-muted)', fontSize: 12 }}>
                Only an organization owner who is its billing owner can change this.
              </Text>
            </Stack>
          )}
        </SettingsRow>
      )}
    </SettingsCard>
  )
}
