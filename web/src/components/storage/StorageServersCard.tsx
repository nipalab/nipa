import { useState } from 'react'
import { Banner, Button, Stack } from '@primer/react'
import { ApiError } from '../../api/client'
import { deleteStorageServer, listStorageServers, testStorageServer } from '../../api/endpoints'
import type { StorageServerResponse, StorageServerTestResponse } from '../../api/models'
import { errorMessage, useAsync } from '../../hooks'
import { useConfirmAction } from '../confirm'
import { Row } from '../RowList'
import { SettingsCard } from '../SettingsCard'
import { EmptyState, ErrorBanner, Loading, Mono } from '../ui'
import { StorageServerDialog } from './StorageServerDialog'
import { isOwnStorageError, OwnStorageBanner, ProbeFailureBanner, useByoStorage } from './storage'

type DialogState = { mode: 'add' } | { mode: 'edit'; server: StorageServerResponse }

export function StorageServersTab() {
  const { enabled, loading } = useByoStorage()
  if (loading) return <Loading />
  if (!enabled) return <EmptyState>Own storage servers are not available on this server.</EmptyState>
  return <StorageServersCard />
}

export function StorageServersCard() {
  const confirm = useConfirmAction()
  const { data, error, loading, reload } = useAsync(() => listStorageServers(), [])
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [testing, setTesting] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, StorageServerTestResponse>>({})

  async function test(server: StorageServerResponse) {
    setTesting(server.id)
    setActionError(null)
    try {
      const result = await testStorageServer(server.id)
      setResults((current) => ({ ...current, [server.id]: result }))
    } catch (err) {
      setActionError(err)
    } finally {
      setTesting(null)
    }
  }

  async function remove(server: StorageServerResponse) {
    const ok = await confirm({
      title: `Delete ${server.name}?`,
      content: 'Nipa forgets this server and its keys. The bucket and its content are left untouched.',
      confirmLabel: 'Delete server',
    })
    if (!ok) return
    setActionError(null)
    try {
      await deleteStorageServer(server.id)
      reload()
    } catch (err) {
      setActionError(
        err instanceof ApiError && err.status === 409
          ? new Error(
              `${server.name} is still used by a repository or an organization default. Delete those repositories or clear the default first.`,
            )
          : err,
      )
    }
  }

  const servers = data ?? []

  return (
    <SettingsCard
      title="Storage servers"
      description="S3-compatible buckets you own. Repositories on them keep their content in your bucket; Nipa stores only metadata."
      actions={
        <Button onClick={() => setDialog({ mode: 'add' })} disabled={servers.length >= 20}>
          Add storage server
        </Button>
      }
    >
      {isOwnStorageError(actionError) ? (
        <OwnStorageBanner />
      ) : (
        <ErrorBanner
          error={actionError ? errorMessage(actionError) : error}
          onDismiss={actionError ? () => setActionError(null) : undefined}
        />
      )}
      {loading ? (
        <Loading />
      ) : servers.length === 0 ? (
        <EmptyState
          bordered={false}
          description="Register an S3-compatible bucket (AWS S3, MinIO, Cloudflare R2) to keep repository content in your own storage. Choose it when you create a repository or set it as an organization default. Using it needs the Own storage add-on."
        >
          No storage servers yet.
        </EmptyState>
      ) : (
        servers.map((server, index) => (
          <div key={server.id}>
            <Row
              first={index === 0}
              title={server.name}
              meta={
                <>
                  <Mono>{server.endpoint}</Mono> · bucket <Mono>{server.bucket}</Mono>
                  {server.prefix && (
                    <>
                      {' '}
                      · prefix <Mono>{server.prefix}</Mono>
                    </>
                  )}
                  {server.region && <> · region {server.region}</>} · key <Mono>{server.access_key_id}</Mono>
                </>
              }
              actions={
                <Stack direction="horizontal" gap="condensed">
                  <Button size="small" loading={testing === server.id} onClick={() => void test(server)}>
                    Test connection
                  </Button>
                  <Button size="small" onClick={() => setDialog({ mode: 'edit', server })}>
                    Edit
                  </Button>
                  <Button size="small" variant="danger" onClick={() => void remove(server)}>
                    Delete
                  </Button>
                </Stack>
              }
            />
            <TestResult result={results[server.id]} />
          </div>
        ))
      )}
      {dialog && (
        <StorageServerDialog
          server={dialog.mode === 'edit' ? dialog.server : undefined}
          onClose={() => setDialog(null)}
          onSaved={() => {
            if (dialog.mode === 'edit') {
              const id = dialog.server.id
              setResults((current) => {
                const next = { ...current }
                delete next[id]
                return next
              })
            }
            reload()
          }}
        />
      )}
    </SettingsCard>
  )
}

function TestResult({ result }: { result?: StorageServerTestResponse }) {
  if (!result) return null
  return (
    <div style={{ padding: '0 var(--base-size-16) var(--base-size-12)' }}>
      {result.ok ? (
        <Banner variant="success" title="Connection works">
          Every check passed.
        </Banner>
      ) : (
        <ProbeFailureBanner check={result.check ?? 'open'} detail={result.error} />
      )}
    </div>
  )
}
