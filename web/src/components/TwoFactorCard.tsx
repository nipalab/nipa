import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react'
import { Button, Dialog, FormControl, Label, Stack, Text, TextInput } from '@primer/react'
import QRCode from 'qrcode'
import {
  confirmMyTOTP,
  disableMyTOTP,
  regenerateMyRecoveryCodes,
  startMyTOTP,
} from '../api/endpoints'
import type { MFAStatusResponse, TOTPEnrollmentResponse } from '../api/models'
import { useAuth } from '../auth'
import { errorMessage } from '../hooks'
import { CopyButton } from './CopyButton'
import { FormDialog } from './FormDialog'
import { normalizeCode } from './MFACodeStep'
import { RecoveryCodes } from './RecoveryCodes'
import { SettingsCard, SettingsRow } from './SettingsCard'
import { Timestamp } from './Timestamp'
import { ErrorBanner, Mono } from './ui'

const LOW_RECOVERY_CODES = 3

type Dialogs = 'enroll' | 'regenerate' | 'disable' | null

export function TwoFactorCard({ status, onChanged }: { status: MFAStatusResponse; onChanged: () => void }) {
  const [open, setOpen] = useState<Dialogs>(null)
  if (!status.available) return null
  const enabled = status.totp.enabled

  function close() {
    setOpen(null)
    onChanged()
  }

  return (
    <SettingsCard
      title="Two-factor authentication"
      description="Ask for a code from an authenticator app after your password when you sign in."
      actions={
        enabled ? (
          <Label variant="success">Enabled</Label>
        ) : (
          <Button variant="primary" onClick={() => setOpen('enroll')}>
            Enable two-factor authentication
          </Button>
        )
      }
    >
      {enabled ? (
        <>
          <SettingsRow label="Authenticator app">
            <Stack direction="horizontal" align="center" justify="space-between" wrap="wrap" gap="condensed">
              <span>
                Enabled <Timestamp value={status.totp.confirmed_at} />
              </span>
              <Button variant="danger" onClick={() => setOpen('disable')}>
                Disable
              </Button>
            </Stack>
          </SettingsRow>
          <SettingsRow label="Recovery codes" subtext="Each code signs you in once when you cannot use your app.">
            <Stack direction="horizontal" align="center" justify="space-between" wrap="wrap" gap="condensed">
              <span style={status.recovery_codes_left <= LOW_RECOVERY_CODES ? { color: 'var(--fgColor-attention)' } : undefined}>
                {status.recovery_codes_left} recovery {status.recovery_codes_left === 1 ? 'code' : 'codes'} left
              </span>
              <Button onClick={() => setOpen('regenerate')}>Regenerate recovery codes</Button>
            </Stack>
          </SettingsRow>
        </>
      ) : (
        <div style={{ padding: 'var(--base-size-16)', color: 'var(--fgColor-muted)' }}>
          Two-factor authentication is off. Your password alone signs you in.
        </div>
      )}
      {open === 'enroll' && <EnrollDialog onClose={close} />}
      {open === 'regenerate' && <RegenerateDialog onClose={close} />}
      {open === 'disable' && <DisableDialog onClose={() => setOpen(null)} onDisabled={onChanged} />}
    </SettingsCard>
  )
}

function CodeInput({ value, onChange, caption }: { value: string; onChange: (value: string) => void; caption: string }) {
  return (
    <FormControl required>
      <FormControl.Label>Authentication code</FormControl.Label>
      <TextInput
        block
        autoFocus
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete="one-time-code"
        spellCheck={false}
      />
      <FormControl.Caption>{caption}</FormControl.Caption>
    </FormControl>
  )
}

function StepDialog({
  title,
  onClose,
  canClose,
  footer,
  formId,
  onSubmit,
  error,
  children,
}: {
  title: string
  onClose: () => void
  canClose: boolean
  footer: ReactNode
  formId: string
  onSubmit: () => void
  error: string | null
  children: ReactNode
}) {
  return (
    <Dialog title={title} width="medium" onClose={canClose ? onClose : () => undefined}>
      <Dialog.Body>
        <form
          id={formId}
          onSubmit={(event: FormEvent) => {
            event.preventDefault()
            onSubmit()
          }}
        >
          <Stack direction="vertical" gap="normal">
            <ErrorBanner error={error} title="Could not continue" />
            {children}
          </Stack>
        </form>
      </Dialog.Body>
      <Dialog.Footer>{footer}</Dialog.Footer>
    </Dialog>
  )
}

function useBusy() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function run(fn: () => Promise<void>) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return { busy, error, run }
}

export function QRCodeImage({ uri }: { uri: string }) {
  const [src, setSrc] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    QRCode.toDataURL(uri, { margin: 2, width: 200 })
      .then((url) => {
        if (active) setSrc(url)
      })
      .catch(() => {
        if (active) setSrc(null)
      })
    return () => {
      active = false
    }
  }, [uri])
  if (!src) return null
  return (
    <img
      src={src}
      width={200}
      height={200}
      alt="QR code for your authenticator app"
      style={{ alignSelf: 'center', borderRadius: 'var(--borderRadius-medium)', background: '#fff' }}
    />
  )
}

function groupSecret(secret: string): string {
  return secret.replace(/(.{4})/g, '$1 ').trim()
}

function EnrollDialog({ onClose }: { onClose: () => void }) {
  const { me } = useAuth()
  const formId = useId()
  const [step, setStep] = useState<'password' | 'scan' | 'codes'>('password')
  const [password, setPassword] = useState('')
  const [enrollment, setEnrollment] = useState<TOTPEnrollmentResponse | null>(null)
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[]>([])
  const [saved, setSaved] = useState(false)
  const { busy, error, run } = useBusy()

  function submit() {
    if (step === 'password') {
      void run(async () => {
        setEnrollment(await startMyTOTP(password))
        setPassword('')
        setStep('scan')
      })
    } else if (step === 'scan') {
      if (normalizeCode(code) === '') return
      void run(async () => {
        const result = await confirmMyTOTP(normalizeCode(code))
        setCodes(result.recovery_codes)
        setStep('codes')
      })
    } else if (saved) {
      onClose()
    }
  }

  const footer =
    step === 'codes' ? (
      <Button type="submit" form={formId} variant="primary" disabled={!saved}>
        Done
      </Button>
    ) : (
      <>
        <Button onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button
          type="submit"
          form={formId}
          variant="primary"
          loading={busy}
          disabled={step === 'scan' && normalizeCode(code) === ''}
        >
          {step === 'password' ? 'Continue' : 'Verify and enable'}
        </Button>
      </>
    )

  return (
    <StepDialog
      title={step === 'codes' ? 'Save your recovery codes' : 'Enable two-factor authentication'}
      onClose={onClose}
      canClose={!busy && (step !== 'codes' || saved)}
      footer={footer}
      formId={formId}
      onSubmit={submit}
      error={error}
    >
      {step === 'password' && (
        <FormControl>
          <FormControl.Label>Current password</FormControl.Label>
          <TextInput
            block
            autoFocus
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          <FormControl.Caption>
            Accounts that sign in only with GitHub or Google leave this empty; it works within 10 minutes of signing in.
          </FormControl.Caption>
        </FormControl>
      )}
      {step === 'scan' && enrollment && (
        <>
          <Text as="p" style={{ margin: 0 }}>
            Scan this QR code with your authenticator app, then enter the 6-digit code it shows.
          </Text>
          <QRCodeImage uri={enrollment.uri} />
          <Stack direction="horizontal" align="center" gap="condensed" wrap="wrap">
            <span style={{ color: 'var(--fgColor-muted)' }}>Or enter this key manually:</span>
            <Mono>{groupSecret(enrollment.secret)}</Mono>
            <CopyButton value={enrollment.secret} label="Copy secret key" />
          </Stack>
          <CodeInput value={code} onChange={setCode} caption="The code changes every 30 seconds." />
        </>
      )}
      {step === 'codes' && <RecoveryCodes codes={codes} account={me?.email} saved={saved} onSavedChange={setSaved} />}
    </StepDialog>
  )
}

function RegenerateDialog({ onClose }: { onClose: () => void }) {
  const { me } = useAuth()
  const formId = useId()
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[] | null>(null)
  const [saved, setSaved] = useState(false)
  const { busy, error, run } = useBusy()

  function submit() {
    if (codes) {
      if (saved) onClose()
      return
    }
    if (normalizeCode(code) === '') return
    void run(async () => {
      const result = await regenerateMyRecoveryCodes(normalizeCode(code))
      setCodes(result.recovery_codes)
    })
  }

  const footer = codes ? (
    <Button type="submit" form={formId} variant="primary" disabled={!saved}>
      Done
    </Button>
  ) : (
    <>
      <Button onClick={onClose} disabled={busy}>
        Cancel
      </Button>
      <Button type="submit" form={formId} variant="primary" loading={busy} disabled={normalizeCode(code) === ''}>
        Regenerate
      </Button>
    </>
  )

  return (
    <StepDialog
      title={codes ? 'Save your recovery codes' : 'Regenerate recovery codes'}
      onClose={onClose}
      canClose={!busy && (!codes || saved)}
      footer={footer}
      formId={formId}
      onSubmit={submit}
      error={error}
    >
      {codes ? (
        <RecoveryCodes codes={codes} account={me?.email} saved={saved} onSavedChange={setSaved} />
      ) : (
        <CodeInput
          value={code}
          onChange={setCode}
          caption="Enter a code from your authenticator app or a recovery code. Your current recovery codes stop working."
        />
      )}
    </StepDialog>
  )
}

function DisableDialog({ onClose, onDisabled }: { onClose: () => void; onDisabled: () => void }) {
  const [code, setCode] = useState('')
  return (
    <FormDialog
      title="Disable two-factor authentication"
      submitLabel="Disable"
      submitVariant="danger"
      submitDisabled={normalizeCode(code) === ''}
      onClose={onClose}
      onSubmit={async () => {
        await disableMyTOTP(normalizeCode(code))
        onDisabled()
      }}
    >
      <CodeInput
        value={code}
        onChange={setCode}
        caption="Enter a code from your authenticator app or a recovery code. Your recovery codes are deleted too."
      />
    </FormDialog>
  )
}
