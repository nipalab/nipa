import { useState } from 'react'
import { Button, FormControl, IconButton, TextInput } from '@primer/react'
import { CheckIcon, CopyIcon, EyeClosedIcon, EyeIcon, SyncIcon } from '@primer/octicons-react'

export const MIN_PASSWORD_LENGTH = 8

const PASSWORD_ALPHABET = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_!@#%'

export function generatePassword(length = 16): string {
  const values = new Uint32Array(length)
  crypto.getRandomValues(values)
  return Array.from(values, (value) => PASSWORD_ALPHABET[value % PASSWORD_ALPHABET.length]).join('')
}

export function passwordProblem(password: string, confirm: string): string | null {
  if (password.length < MIN_PASSWORD_LENGTH) return `Use at least ${MIN_PASSWORD_LENGTH} characters.`
  if (password !== confirm) return 'The passwords do not match.'
  return null
}

// PasswordFields asks for a password twice, or generates one to hand over.
export function PasswordFields({
  password,
  confirm,
  onChange,
  label = 'Password',
  autoFocus = false,
}: {
  password: string
  confirm: string
  onChange: (password: string, confirm: string) => void
  label?: string
  autoFocus?: boolean
}) {
  const [visible, setVisible] = useState(false)
  const [copied, setCopied] = useState(false)
  const tooShort = password !== '' && password.length < MIN_PASSWORD_LENGTH
  const mismatch = confirm !== '' && password !== confirm

  async function copy() {
    try {
      await navigator.clipboard.writeText(password)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <>
      <FormControl required>
        <FormControl.Label>{label}</FormControl.Label>
        <div style={{ display: 'flex', gap: 'var(--base-size-8)', width: '100%' }}>
          <TextInput
            block
            autoFocus={autoFocus}
            type={visible ? 'text' : 'password'}
            autoComplete="new-password"
            value={password}
            validationStatus={tooShort ? 'error' : undefined}
            onChange={(event) => {
              setCopied(false)
              onChange(event.target.value, confirm)
            }}
            trailingAction={
              <TextInput.Action
                icon={visible ? EyeClosedIcon : EyeIcon}
                aria-label={visible ? 'Hide password' : 'Show password'}
                onClick={() => setVisible((value) => !value)}
              />
            }
            style={{ flex: 1 }}
          />
          {visible && password !== '' && (
            <IconButton icon={copied ? CheckIcon : CopyIcon} aria-label={copied ? 'Copied' : 'Copy password'} onClick={() => void copy()} />
          )}
          <Button
            leadingVisual={SyncIcon}
            onClick={() => {
              const generated = generatePassword()
              setVisible(true)
              setCopied(false)
              onChange(generated, generated)
            }}
          >
            Generate
          </Button>
        </div>
        {tooShort ? (
          <FormControl.Validation variant="error">Use at least {MIN_PASSWORD_LENGTH} characters.</FormControl.Validation>
        ) : (
          <FormControl.Caption>
            At least {MIN_PASSWORD_LENGTH} characters. Share it with the user over a safe channel; they can change it from their profile.
          </FormControl.Caption>
        )}
      </FormControl>
      <FormControl required>
        <FormControl.Label>Confirm password</FormControl.Label>
        <TextInput
          block
          type={visible ? 'text' : 'password'}
          autoComplete="new-password"
          value={confirm}
          validationStatus={mismatch ? 'error' : undefined}
          onChange={(event) => onChange(password, event.target.value)}
        />
        {mismatch && <FormControl.Validation variant="error">The passwords do not match.</FormControl.Validation>}
      </FormControl>
    </>
  )
}
