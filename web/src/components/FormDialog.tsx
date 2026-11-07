import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { Button, Dialog, Stack } from '@primer/react'
import { errorMessage } from '../hooks'
import { isSeatLimitError, SeatLimitBanner } from './SeatLimitBanner'
import { ErrorBanner } from './ui'

export function FormDialog({
  title,
  onClose,
  onSubmit,
  submitLabel = 'Save',
  submitVariant = 'primary',
  submitDisabled = false,
  width = 'medium',
  children,
}: {
  title: string
  onClose: () => void
  onSubmit: () => Promise<unknown | false>
  submitLabel?: string
  submitVariant?: 'primary' | 'danger'
  submitDisabled?: boolean
  width?: 'small' | 'medium' | 'large'
  children: ReactNode
}) {
  const formId = useId()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [seatLimit, setSeatLimit] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy || submitDisabled) return
    setBusy(true)
    setError(null)
    setSeatLimit(false)
    try {
      const result = await onSubmit()
      if (result === false) {
        setBusy(false)
        return
      }
      onClose()
    } catch (err) {
      setError(errorMessage(err))
      setSeatLimit(isSeatLimitError(err))
      setBusy(false)
    }
  }

  return (
    <Dialog title={title} width={width} onClose={busy ? () => undefined : onClose}>
      <Dialog.Body>
        <form id={formId} onSubmit={(event) => void submit(event)}>
          <Stack direction="vertical" gap="normal">
            {error && seatLimit ? (
              <SeatLimitBanner message={error} />
            ) : (
              <ErrorBanner error={error} title="Could not save" />
            )}
            {children}
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>
        <Button onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" form={formId} variant={submitVariant} loading={busy} disabled={submitDisabled}>
          {submitLabel}
        </Button>
      </Dialog.Footer>
    </Dialog>
  )
}
