import { Button, Checkbox, FormControl, Stack, Text } from '@primer/react'
import { CheckIcon, CopyIcon, DownloadIcon } from '@primer/octicons-react'
import { useClipboard } from '../hooks'
import { Mono } from './ui'

export const RECOVERY_CODES_FILE = 'nipa-recovery-codes.txt'

export function recoveryCodesText(codes: string[], account?: string): string {
  const header = account ? `Nipa recovery codes for ${account}` : 'Nipa recovery codes'
  return `${header}\nEach code signs you in once when you cannot use your authenticator app.\n\n${codes.join('\n')}\n`
}

export function downloadText(filename: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

export function RecoveryCodes({
  codes,
  account,
  saved,
  onSavedChange,
}: {
  codes: string[]
  account?: string
  saved: boolean
  onSavedChange: (saved: boolean) => void
}) {
  const { copied, copy } = useClipboard()
  const text = recoveryCodesText(codes, account)
  return (
    <Stack direction="vertical" gap="normal">
      <Text as="p" style={{ margin: 0 }}>
        Keep these recovery codes somewhere safe. Each one signs you in once if you lose your authenticator app. They
        are shown only now.
      </Text>
      <ul
        aria-label="Recovery codes"
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
          gap: 'var(--base-size-8)',
          listStyle: 'none',
          margin: 0,
          padding: 'var(--base-size-16)',
          border: '1px solid var(--borderColor-default)',
          borderRadius: 'var(--borderRadius-medium)',
        }}
      >
        {codes.map((code) => (
          <li key={code}>
            <Mono>{code}</Mono>
          </li>
        ))}
      </ul>
      <Stack direction="horizontal" gap="condensed" wrap="wrap">
        <Button leadingVisual={copied ? CheckIcon : CopyIcon} onClick={() => void copy(codes.join('\n'))}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
        <Button leadingVisual={DownloadIcon} onClick={() => downloadText(RECOVERY_CODES_FILE, text)}>
          Download .txt
        </Button>
      </Stack>
      <FormControl required>
        <Checkbox checked={saved} onChange={(event) => onSavedChange(event.target.checked)} />
        <FormControl.Label>I saved these codes</FormControl.Label>
      </FormControl>
    </Stack>
  )
}
