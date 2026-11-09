import { useState } from 'react'
import { IconButton } from '@primer/react'
import { CheckIcon, CopyIcon } from '@primer/octicons-react'

export function CopyButton({ value, label, size = 'small' }: { value: string; label: string; size?: 'small' | 'medium' }) {
  const [copied, setCopied] = useState(false)
  return (
    <IconButton
      size={size}
      variant="invisible"
      icon={copied ? CheckIcon : CopyIcon}
      aria-label={copied ? 'Copied' : label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        } catch {
          setCopied(false)
        }
      }}
    />
  )
}
