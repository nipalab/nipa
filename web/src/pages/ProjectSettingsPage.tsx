import { useState } from 'react'
import { Button, FormControl, Stack, Text, TextInput } from '@primer/react'
import { useParams } from 'react-router-dom'
import {
  createProjectRule,
  deleteProjectDefault,
  deleteProjectRule,
  listProjectDefaults,
  listProjectRules,
  setBranchProtection,
  setProjectDefault,
} from '../api/endpoints'
import { RepoPageShell } from '../components/repo/RepoPageShell'
import { useRepoChrome } from '../components/repo/useRepoChrome'
import { EmptyState, ErrorBanner, Loading, Mono } from '../components/ui'
import { useAsync } from '../hooks'
import { formatPermission, parsePermission } from '../api/permissions'

export default function ProjectSettingsPage() {
  const { org = '', project = '' } = useParams()
  const {
    branches,
    reloadBranches,
    canWrite,
    canAdmin,
    defaultBranch,
  } = useRepoChrome(org, project)

  const { data: rules, error: rulesError, loading: rulesLoading, reload: reloadRules } = useAsync(
    () => listProjectRules(org, project),
    [org, project],
  )
  const { data: defaults, reload: reloadDefaults } = useAsync(
    () => listProjectDefaults(org, project),
    [org, project],
  )

  const [subject, setSubject] = useState('')
  const [prefix, setPrefix] = useState('')
  const [permission, setPermission] = useState('read')
  const [defaultPrefix, setDefaultPrefix] = useState('')
  const [defaultPermission, setDefaultPermission] = useState('read')
  const [actionError, setActionError] = useState<string | null>(null)

  async function run(action: () => Promise<unknown>) {
    setActionError(null)
    try {
      await action()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  if (!canAdmin) {
    return (
      <RepoPageShell
        org={org}
        project={project}
        active="settings"
        rev={defaultBranch}
        canAdmin={canAdmin}
        canWrite={canWrite}
        heading="Settings"
      >
        <ErrorBanner error={rulesError} />
        <Text>You need project admin permission to manage this repository.</Text>
      </RepoPageShell>
    )
  }

  return (
    <RepoPageShell
      org={org}
      project={project}
      active="settings"
      rev={defaultBranch}
      canAdmin={canAdmin}
      canWrite={canWrite}
      heading="Settings"
    >
      <ErrorBanner error={actionError ?? rulesError} />

      <Text as="h3">Branch protection</Text>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <tbody>
          {branches?.map((branch) => (
            <tr key={branch.id} style={{ borderBottom: '1px solid var(--borderColor-muted)' }}>
              <td style={{ padding: '6px 4px' }}>
                {branch.name}
                {branch.is_default && <span style={{ color: 'var(--fgColor-accent)' }}> · default</span>}
              </td>
              <td style={{ padding: '6px 4px', textAlign: 'right' }}>
                <Button
                  size="small"
                  onClick={() =>
                    run(async () => {
                      await setBranchProtection(org, project, branch.name, !branch.is_protected)
                      reloadBranches()
                    })
                  }
                >
                  {branch.is_protected ? 'Unprotect' : 'Protect'}
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <Text as="h3">Access rules</Text>
      {rulesLoading && <Loading />}
      {!rulesLoading && rules && rules.length === 0 && <EmptyState>No rules.</EmptyState>}
      {rules?.map((rule) => (
        <div
          key={rule.id}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            borderBottom: '1px solid var(--borderColor-muted)',
            padding: '6px 0',
          }}
        >
          <span>
            {rule.user_id ? `user:${rule.user_id}` : `group:${rule.group_id}`} ·{' '}
            <Mono>{rule.path_prefix || '/'}</Mono> · {formatPermission(rule.permission)}
          </span>
          <Button
            size="small"
            variant="danger"
            onClick={() =>
              run(async () => {
                await deleteProjectRule(org, project, rule.id)
                reloadRules()
              })
            }
          >
            Revoke
          </Button>
        </div>
      ))}

      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            const mask = parsePermission(permission)
            await createProjectRule(org, project, subject, '', prefix, mask)
            setSubject('')
            setPrefix('')
            reloadRules()
          })
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Grant access</strong>
          <FormControl required>
            <FormControl.Label>User or group id (base36)</FormControl.Label>
            <TextInput block value={subject} onChange={(event) => setSubject(event.target.value)} />
          </FormControl>
          <FormControl>
            <FormControl.Label>Path prefix (empty = whole repository)</FormControl.Label>
            <TextInput block value={prefix} onChange={(event) => setPrefix(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Permissions</FormControl.Label>
            <TextInput block value={permission} onChange={(event) => setPermission(event.target.value)} />
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Grant
          </Button>
        </Stack>
      </form>

      <Text as="h3">Path defaults</Text>
      {defaults?.map((entry) => (
        <div
          key={entry.path_prefix}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            borderBottom: '1px solid var(--borderColor-muted)',
            padding: '6px 0',
          }}
        >
          <span>
            <Mono>{entry.path_prefix || '/'}</Mono> · {formatPermission(entry.permission)}
          </span>
          <Button
            size="small"
            variant="danger"
            onClick={() =>
              run(async () => {
                await deleteProjectDefault(org, project, entry.path_prefix)
                reloadDefaults()
              })
            }
          >
            Remove
          </Button>
        </div>
      ))}
      <form
        onSubmit={(event) => {
          event.preventDefault()
          run(async () => {
            await setProjectDefault(org, project, defaultPrefix, parsePermission(defaultPermission))
            setDefaultPrefix('')
            reloadDefaults()
          })
        }}
        style={{ border: '1px solid var(--borderColor-default)', borderRadius: 6, padding: 16 }}
      >
        <Stack direction="vertical" gap="normal">
          <strong>Set default</strong>
          <FormControl>
            <FormControl.Label>Path prefix (empty = whole repository)</FormControl.Label>
            <TextInput block value={defaultPrefix} onChange={(event) => setDefaultPrefix(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Permissions</FormControl.Label>
            <TextInput
              block
              value={defaultPermission}
              onChange={(event) => setDefaultPermission(event.target.value)}
            />
          </FormControl>
          <Button type="submit" variant="primary" style={{ alignSelf: 'flex-start' }}>
            Save default
          </Button>
        </Stack>
      </form>
    </RepoPageShell>
  )
}
