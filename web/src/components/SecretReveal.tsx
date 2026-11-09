import { useState } from 'react'
import { Banner, IconButton, Stack, Text } from '@primer/react'
import { CheckIcon, CopyIcon } from '@primer/octicons-react'
import { Mono } from './ui'

// SecretReveal shows a secret that is only returned once, with a copy button.
export function SecretReveal({
  title,
  message,
  secret,
  copyLabel,
  onDismiss,
}: {
  title: string
  message: string
  secret: string
  copyLabel: string
  onDismiss: () => void
}) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div style={{ padding: 'var(--base-size-16)' }}>
      <Banner variant="success" title={title} onDismiss={onDismiss}>
        <Stack direction="vertical" gap="condensed">
          <Text as="p" style={{ margin: 0 }}>
            {message}
          </Text>
          <div className="nipa-token-value">
            <Mono>{secret}</Mono>
            <IconButton
              size="small"
              variant="invisible"
              icon={copied ? CheckIcon : CopyIcon}
              aria-label={copied ? 'Copied' : copyLabel}
              onClick={() => void copy()}
            />
          </div>
        </Stack>
      </Banner>
    </div>
  )
}
