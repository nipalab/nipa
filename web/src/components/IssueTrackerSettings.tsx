import { useCallback, useEffect, useState } from 'react'
import { Banner, Button, Checkbox, FormControl, Radio, RadioGroup, Select, Stack, Text, TextInput } from '@primer/react'
import { ApiError } from '../api/client'
import {
  deleteIssueTracker,
  deleteProjectIssueTracker,
  getIssueTracker,
  getProjectIssueTracker,
  putIssueTracker,
  putProjectIssueTracker,
  testIssueTracker,
} from '../api/endpoints'
import type {
  IssueTrackerResponse,
  IssueTrackerTestResponse,
  LinkCommits,
  ProjectIssueTrackerResponse,
} from '../api/models'
import { errorMessage } from '../hooks'
import { useConfirmAction } from './confirm'
import { SettingsCard, SettingsRow } from './SettingsCard'
import { ErrorBanner, Loading } from './ui'

const CHECK_LABELS: Record<string, string> = {
  auth: 'Sign-in failed: check the account and token.',
  reach: 'The server could not reach this address.',
  tls: 'The TLS certificate was not accepted.',
}

type Loaded<T> = { data: T | null; missing: boolean; unavailable: boolean; error: string | null; loading: boolean }

// useOptional loads a resource whose 404 means "not set up yet" and whose
// 503 means the integration is off on this server.
function useOptional<T>(load: () => Promise<T>, deps: unknown[]): Loaded<T> & { reload: () => void } {
  const [state, setState] = useState<Loaded<T>>({ data: null, missing: false, unavailable: false, error: null, loading: true })
  const [nonce, setNonce] = useState(0)
  const reload = useCallback(() => setNonce((n) => n + 1), [])
  useEffect(() => {
    let active = true
    setState((prev) => ({ ...prev, loading: true }))
    load()
      .then((data) => active && setState({ data, missing: false, unavailable: false, error: null, loading: false }))
      .catch((err: unknown) => {
        if (!active) return
        const status = err instanceof ApiError ? err.status : 0
        setState({
          data: null,
          missing: status === 404,
          unavailable: status === 503,
          error: status === 404 || status === 503 ? null : errorMessage(err),
          loading: false,
        })
      })
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])
  return { ...state, reload }
}

export function IssueTrackerUnavailable() {
  return (
    <Banner variant="warning" title="Issue tracker integration is off">
      This server has no INTEGRATIONS_ENCRYPTION_KEY, so issue tracker connections cannot be stored. Ask the server
      administrator to set it.
    </Banner>
  )
}

export function OrgIssueTrackerSettings({ org }: { org: string }) {
  const tracker = useOptional(() => getIssueTracker(org), [org])
  if (tracker.loading && !tracker.data && !tracker.missing) return <Loading variant="skeleton" />
  if (tracker.unavailable) return <IssueTrackerUnavailable />
  if (tracker.data && !tracker.data.can_manage) return <IssueTrackerSummary tracker={tracker.data} />
  return (
    <Stack direction="vertical" gap="normal">
      <ErrorBanner error={tracker.error} />
      <IssueTrackerForm org={org} current={tracker.data} onChanged={tracker.reload} />
    </Stack>
  )
}

function IssueTrackerSummary({ tracker }: { tracker: IssueTrackerResponse }) {
  return (
    <SettingsCard title="Issue tracker" description="Only organization owners can change the connection.">
      <SettingsRow label="Kind">{tracker.kind === 'jira' ? 'Jira' : tracker.kind}</SettingsRow>
      <SettingsRow label="Name">{tracker.name}</SettingsRow>
      <SettingsRow label="Base URL">{tracker.base_url}</SettingsRow>
    </SettingsCard>
  )
}

function IssueTrackerForm({
  org,
  current,
  onChanged,
}: {
  org: string
  current: IssueTrackerResponse | null
  onChanged: () => void
}) {
  const confirm = useConfirmAction()
  const [name, setName] = useState(current?.name ?? '')
  const [baseURL, setBaseURL] = useState(current?.base_url ?? '')
  const [authKind, setAuthKind] = useState<'basic' | 'bearer'>(current?.auth_kind ?? 'basic')
  const [account, setAccount] = useState(current?.account ?? '')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [test, setTest] = useState<IssueTrackerTestResponse | null>(null)

  useEffect(() => {
    setName(current?.name ?? '')
    setBaseURL(current?.base_url ?? '')
    setAuthKind(current?.auth_kind ?? 'basic')
    setAccount(current?.account ?? '')
    setToken('')
  }, [current])

  async function act(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    setNotice(null)
    setTest(null)
    try {
      await fn()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const save = () =>
    act(async () => {
      await putIssueTracker(org, {
        kind: 'jira',
        name: name.trim(),
        base_url: baseURL.trim(),
        auth_kind: authKind,
        account: authKind === 'basic' ? account.trim() : '',
        ...(token.trim() ? { token: token.trim() } : {}),
      })
      setNotice('Connection saved and verified.')
      onChanged()
    })

  const runTest = () =>
    act(async () => {
      setTest(await testIssueTracker(org))
    })

  async function remove() {
    const ok = await confirm({
      title: 'Delete the issue tracker connection?',
      content: 'Projects stop showing issue links. Remove it from every project first.',
      confirmLabel: 'Delete connection',
    })
    if (ok)
      await act(async () => {
        await deleteIssueTracker(org)
        onChanged()
      })
  }

  return (
    <SettingsCard
      title="Issue tracker"
      description="Connect one Jira site for this organization. Projects then choose their Jira project keys. Use a dedicated Jira account limited to the linked projects."
      footer={
        <>
          {current && (
            <>
              <Button variant="danger" disabled={busy} onClick={() => void remove()}>
                Delete
              </Button>
              <Button disabled={busy} onClick={() => void runTest()}>
                Test connection
              </Button>
            </>
          )}
          <Button variant="primary" loading={busy} disabled={!baseURL.trim() || (!current && !token.trim())} onClick={() => void save()}>
            {current ? 'Save' : 'Connect'}
          </Button>
        </>
      }
    >
      <div style={{ padding: 'var(--base-size-16)' }}>
        <Stack direction="vertical" gap="normal">
          <ErrorBanner error={error} title="Could not save" onDismiss={() => setError(null)} />
          {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
          {test &&
            (test.ok ? (
              <Banner variant="success" title="Connection works">
                {[test.site?.title, test.site?.deployment, test.site?.account_name && `signed in as ${test.site.account_name}`]
                  .filter(Boolean)
                  .join(' · ')}
              </Banner>
            ) : (
              <Banner variant="critical" title={`Check failed: ${test.check ?? 'unknown'}`}>
                {CHECK_LABELS[test.check ?? ''] ?? ''} {test.error}
              </Banner>
            ))}
          <FormControl disabled>
            <FormControl.Label>Kind</FormControl.Label>
            <Select value="jira">
              <Select.Option value="jira">Jira (Cloud or Data Center)</Select.Option>
            </Select>
          </FormControl>
          <FormControl>
            <FormControl.Label>Name</FormControl.Label>
            <TextInput block value={name} placeholder="Jira" onChange={(event) => setName(event.target.value)} />
          </FormControl>
          <FormControl required>
            <FormControl.Label>Base URL</FormControl.Label>
            <TextInput
              block
              value={baseURL}
              placeholder="https://studio.atlassian.net"
              onChange={(event) => setBaseURL(event.target.value)}
            />
            <FormControl.Caption>A Data Center on a private network must be in the server's egress allowlist.</FormControl.Caption>
          </FormControl>
          <RadioGroup name="issue-tracker-auth" onChange={(value) => setAuthKind(value === 'bearer' ? 'bearer' : 'basic')}>
            <RadioGroup.Label>Authentication</RadioGroup.Label>
            <FormControl>
              <Radio value="basic" checked={authKind === 'basic'} />
              <FormControl.Label>Jira Cloud: account email and API token</FormControl.Label>
            </FormControl>
            <FormControl>
              <Radio value="bearer" checked={authKind === 'bearer'} />
              <FormControl.Label>Jira Data Center: personal access token</FormControl.Label>
            </FormControl>
          </RadioGroup>
          {authKind === 'basic' && (
            <FormControl required>
              <FormControl.Label>Account email</FormControl.Label>
              <TextInput block type="email" value={account} onChange={(event) => setAccount(event.target.value)} />
            </FormControl>
          )}
          <FormControl required={!current}>
            <FormControl.Label>Token</FormControl.Label>
            <TextInput
              block
              type="password"
              autoComplete="off"
              value={token}
              placeholder={current ? 'Leave blank to keep the stored token' : ''}
              onChange={(event) => setToken(event.target.value)}
            />
            <FormControl.Caption>
              Write-only: the token is never shown again. Enter it again when the base URL changes.
            </FormControl.Caption>
          </FormControl>
        </Stack>
      </div>
    </SettingsCard>
  )
}

const LINK_COMMIT_OPTIONS: { value: LinkCommits; label: string }[] = [
  { value: 'off', label: 'Off' },
  { value: 'default_branch', label: 'Default branch only' },
  { value: 'all', label: 'All branches' },
]

export function ProjectIssueTrackerSettings({ org, project }: { org: string; project: string }) {
  const settings = useOptional(() => getProjectIssueTracker(org, project), [org, project])
  const connection = useOptional(() => getIssueTracker(org), [org])
  if ((settings.loading && !settings.data && !settings.missing) || (connection.loading && !connection.data && !connection.missing)) return <Loading variant="skeleton" />
  if (settings.unavailable || connection.unavailable) return <IssueTrackerUnavailable />
  if (connection.missing && !settings.data) {
    return (
      <SettingsCard title="Issue tracker">
        <div style={{ padding: 'var(--base-size-16)' }}>
          <Text as="p" style={{ margin: 0 }}>
            This organization has no issue tracker connection. An organization owner can add one in the organization
            settings.
          </Text>
        </div>
      </SettingsCard>
    )
  }
  return (
    <Stack direction="vertical" gap="normal">
      <ErrorBanner error={settings.error ?? connection.error} />
      <ProjectIssueTrackerForm
        org={org}
        project={project}
        connection={connection.data}
        current={settings.data}
        onChanged={settings.reload}
      />
    </Stack>
  )
}

function ProjectIssueTrackerForm({
  org,
  project,
  connection,
  current,
  onChanged,
}: {
  org: string
  project: string
  connection: IssueTrackerResponse | null
  current: ProjectIssueTrackerResponse | null
  onChanged: () => void
}) {
  const confirm = useConfirmAction()
  const [keys, setKeys] = useState((current?.keys ?? []).join(', '))
  const [linkMRs, setLinkMRs] = useState(current?.link_merge_requests ?? true)
  const [linkCommits, setLinkCommits] = useState<LinkCommits>(current?.link_commits ?? 'off')
  const [comment, setComment] = useState(current?.comment_on_merge ?? true)
  const [merged, setMerged] = useState(current?.merged_status ?? '')
  const [opened, setOpened] = useState(current?.opened_status ?? '')
  const [active, setActive] = useState(current?.active ?? true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const parsedKeys = keys
    .split(/[\s,]+/)
    .map((key) => key.trim().toUpperCase())
    .filter(Boolean)

  async function act(fn: () => Promise<void>, done: string) {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      await fn()
      setNotice(done)
      onChanged()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const save = () =>
    act(
      () =>
        putProjectIssueTracker(org, project, {
          keys: parsedKeys,
          link_merge_requests: linkMRs,
          link_commits: linkCommits,
          comment_on_merge: comment,
          merged_status: merged.trim(),
          opened_status: opened.trim(),
          active,
        }).then(() => undefined),
      'Issue tracker settings saved.',
    )

  async function remove() {
    const ok = await confirm({
      title: 'Stop using the issue tracker?',
      content: 'Issue keys are no longer recognized or shown in this repository.',
      confirmLabel: 'Remove',
    })
    if (ok) await act(() => deleteProjectIssueTracker(org, project), 'The repository no longer uses the issue tracker.')
  }

  return (
    <SettingsCard
      title="Issue tracker"
      description={
        connection
          ? `Link commits, branches and merge requests to issues in ${connection.name} (${connection.base_url}).`
          : 'Link commits, branches and merge requests to issues in the organization’s issue tracker.'
      }
      footer={
        <>
          {current && (
            <Button variant="danger" disabled={busy} onClick={() => void remove()}>
              Remove
            </Button>
          )}
          <Button variant="primary" loading={busy} disabled={parsedKeys.length === 0} onClick={() => void save()}>
            Save
          </Button>
        </>
      }
    >
      <div style={{ padding: 'var(--base-size-16)' }}>
        <Stack direction="vertical" gap="normal">
          <ErrorBanner error={error} title="Could not save" onDismiss={() => setError(null)} />
          {notice && <Banner variant="success" title={notice} onDismiss={() => setNotice(null)} />}
          <Banner variant="info" title="Who sees what">
            Everyone who can read this repository sees the titles and statuses of linked issues that the connection’s
            Jira account can read. Only these project keys are recognized and looked up.
          </Banner>
          <FormControl required>
            <FormControl.Label>Jira project keys</FormControl.Label>
            <TextInput block value={keys} placeholder="GAME, ART" onChange={(event) => setKeys(event.target.value)} />
            <FormControl.Caption>
              Comma-separated. Keys such as GAME-123 in commit messages, branch names and merge requests are linked.
            </FormControl.Caption>
          </FormControl>
          <FormControl>
            <Checkbox checked={linkMRs} onChange={(event) => setLinkMRs(event.target.checked)} />
            <FormControl.Label>Link merge requests on the Jira issue</FormControl.Label>
            <FormControl.Caption>
              Merge request titles and links become visible to Jira users who can see the issue, even for a private
              repository.
            </FormControl.Caption>
          </FormControl>
          <FormControl>
            <FormControl.Label>Link commits on the Jira issue</FormControl.Label>
            <Select value={linkCommits} onChange={(event) => setLinkCommits(event.target.value as LinkCommits)}>
              {LINK_COMMIT_OPTIONS.map((option) => (
                <Select.Option key={option.value} value={option.value}>
                  {option.label}
                </Select.Option>
              ))}
            </Select>
          </FormControl>
          <FormControl>
            <Checkbox checked={comment} onChange={(event) => setComment(event.target.checked)} />
            <FormControl.Label>Comment on the issue when a merge request merges</FormControl.Label>
          </FormControl>
          <FormControl>
            <FormControl.Label>Status when a merge request opens</FormControl.Label>
            <TextInput block value={opened} placeholder="In Review" onChange={(event) => setOpened(event.target.value)} />
            <FormControl.Caption>Leave empty to keep the issue where it is.</FormControl.Caption>
          </FormControl>
          <FormControl>
            <FormControl.Label>Status when a merge request merges</FormControl.Label>
            <TextInput block value={merged} placeholder="Done" onChange={(event) => setMerged(event.target.value)} />
            <FormControl.Caption>
              Only issues the request closes (Fixes GAME-123) move. Leave empty to only link.
            </FormControl.Caption>
          </FormControl>
          <FormControl>
            <Checkbox checked={active} onChange={(event) => setActive(event.target.checked)} />
            <FormControl.Label>Active</FormControl.Label>
            <FormControl.Caption>While inactive, no new links are recorded and no issue keys are shown.</FormControl.Caption>
          </FormControl>
        </Stack>
      </div>
    </SettingsCard>
  )
}
