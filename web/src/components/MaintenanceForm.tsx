import { useState, type FormEvent } from 'react'
import { Banner, Button, Checkbox, FormControl, Select, Stack, Textarea, TextInput } from '@primer/react'
import type { MaintenanceMode, MaintenanceWindow, SetMaintenanceInput } from '../api/models'
import { errorMessage } from '../hooks'
import { recheckMaintenance } from '../maintenance'
import { MaintenanceWindowBanner } from './MaintenanceBanner'
import { useConfirmAction } from './confirm'
import { SettingsCard } from './SettingsCard'
import { EmptyState, ErrorBanner } from './ui'

export const MAX_MESSAGE = 500

const MODE_OPTIONS: { value: MaintenanceMode; label: string }[] = [
  { value: 'read_only', label: 'Read-only: browsing works, changes are refused' },
  { value: 'full', label: 'Closed: nothing can be opened' },
  { value: 'frozen', label: 'Frozen: no database writes, sign-in paused (for database moves)' },
]

const FROZEN_END = 'A frozen window can only be ended from the host with `nipad maintenance end`.'

export function toLocalInput(iso?: string): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function fromLocalInput(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export function MaintenanceForm({
  scope,
  target,
  window,
  source = 'db',
  notifyLabel,
  onSave,
  onEnd,
  onChanged,
}: {
  scope: 'server' | 'org' | 'project'
  target: string
  window: MaintenanceWindow | null
  source?: string
  notifyLabel: string
  onSave: (input: SetMaintenanceInput) => Promise<MaintenanceWindow>
  onEnd: () => Promise<void>
  onChanged: () => void
}) {
  const confirm = useConfirmAction()
  const [mode, setMode] = useState<MaintenanceMode>(window?.mode ?? 'read_only')
  const [message, setMessage] = useState(window?.message ?? '')
  const [start, setStart] = useState(toLocalInput(window?.starts_at))
  const [end, setEnd] = useState(toLocalInput(window?.ends_at))
  const [notify, setNotify] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState<{ notified: boolean; notify: boolean } | null>(null)

  const fileControlled = scope === 'server' && source === 'file'
  const frozenNow = window?.mode === 'frozen' && window.active
  const disabled = fileControlled || frozenNow || busy
  const modes = scope === 'server' ? MODE_OPTIONS : MODE_OPTIONS.filter((option) => option.value !== 'frozen')
  const startISO = fromLocalInput(start)
  const endISO = fromLocalInput(end)
  const problem =
    message.length > MAX_MESSAGE
      ? `The message is limited to ${MAX_MESSAGE} characters.`
      : startISO && endISO && endISO <= startISO
        ? 'The estimated end must be after the start.'
        : null

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (disabled || problem) return
    setBusy(true)
    setError(null)
    setSaved(null)
    try {
      const stored = await onSave({ mode, message: message.trim(), starts_at: startISO, ends_at: endISO, notify })
      setSaved({ notified: Boolean(stored?.notified), notify })
      setNotify(false)
      onChanged()
      void recheckMaintenance()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function endWindow() {
    const ok = await confirm({
      title: 'End maintenance?',
      content: window?.active ? 'Everyone gets full access again right away.' : 'The scheduled window is removed.',
      confirmLabel: 'End maintenance',
    })
    if (!ok) return
    setBusy(true)
    setError(null)
    setSaved(null)
    try {
      await onEnd()
      onChanged()
      void recheckMaintenance()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingsCard
      title="Maintenance window"
      description="Users see the message, the mode and the estimated end. Maintenance never ends on its own: end it here when the work is done."
    >
      <Stack direction="vertical" gap="normal" style={{ padding: 'var(--base-size-16)' }}>
        {window ? (
          <MaintenanceWindowBanner item={{ scope, target, window }} />
        ) : (
          <EmptyState bordered={false}>No maintenance is scheduled.</EmptyState>
        )}
        {fileControlled && (
          <Banner variant="warning" title="Controlled by MAINTENANCE_FILE">
            The server window comes from the override file on the host. Remove the file to manage it here.
          </Banner>
        )}
        {frozenNow && <Banner variant="warning" title="The server is frozen">{FROZEN_END}</Banner>}
        <ErrorBanner error={error} title="Could not save the maintenance window" onDismiss={() => setError(null)} />
        {saved && (
          <Banner
            variant="success"
            title={
              saved.notified
                ? 'Saved. The announcement email is on its way.'
                : saved.notify
                  ? 'Saved. No announcement email was sent (email is off or there is no recipient).'
                  : 'Saved.'
            }
          />
        )}
        <form onSubmit={(event) => void submit(event)} aria-label="Maintenance window">
          <Stack direction="vertical" gap="normal">
            <FormControl disabled={disabled}>
              <FormControl.Label>Mode</FormControl.Label>
              <Select value={mode} onChange={(event) => setMode(event.target.value as MaintenanceMode)} block>
                {modes.map((option) => (
                  <Select.Option key={option.value} value={option.value}>
                    {option.label}
                  </Select.Option>
                ))}
              </Select>
              {mode === 'frozen' && <FormControl.Caption>{FROZEN_END}</FormControl.Caption>}
            </FormControl>
            <FormControl disabled={disabled}>
              <FormControl.Label>Message</FormControl.Label>
              <Textarea
                block
                value={message}
                maxLength={MAX_MESSAGE}
                onChange={(event) => setMessage(event.target.value)}
                placeholder="What is happening and what users should expect"
              />
              <FormControl.Caption>
                {message.length}/{MAX_MESSAGE}
              </FormControl.Caption>
            </FormControl>
            <Stack direction="horizontal" gap="normal" wrap="wrap">
              <FormControl disabled={disabled}>
                <FormControl.Label>Start</FormControl.Label>
                <Stack direction="horizontal" gap="condensed">
                  <TextInput type="datetime-local" name="starts_at" value={start} onChange={(event) => setStart(event.target.value)} />
                  <Button type="button" disabled={disabled} onClick={() => setStart(toLocalInput(new Date().toISOString()))}>
                    Now
                  </Button>
                </Stack>
                <FormControl.Caption>Empty starts a new window now and keeps the start of an existing one.</FormControl.Caption>
              </FormControl>
              <FormControl disabled={disabled}>
                <FormControl.Label>Estimated end</FormControl.Label>
                <TextInput type="datetime-local" name="ends_at" value={end} onChange={(event) => setEnd(event.target.value)} />
                <FormControl.Caption>Shown to users only; it never ends maintenance.</FormControl.Caption>
              </FormControl>
            </Stack>
            <FormControl disabled={disabled}>
              <Checkbox checked={notify} onChange={(event) => setNotify(event.target.checked)} />
              <FormControl.Label>{notifyLabel}</FormControl.Label>
            </FormControl>
            {problem && <Banner variant="critical" title={problem} />}
            <Stack direction="horizontal" gap="condensed">
              <Button type="submit" variant="primary" loading={busy} disabled={disabled || problem !== null}>
                {window ? 'Update maintenance' : 'Schedule maintenance'}
              </Button>
              {window && (
                <Button type="button" variant="danger" disabled={disabled} onClick={() => void endWindow()}>
                  End maintenance
                </Button>
              )}
            </Stack>
          </Stack>
        </form>
      </Stack>
    </SettingsCard>
  )
}
