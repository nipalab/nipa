import { useCallback, type ReactNode } from 'react'
import { useConfirm } from '@primer/react'

export interface ConfirmActionOptions {
  title: string
  content: ReactNode
  confirmLabel?: string
  danger?: boolean
}

export function useConfirmAction(): (options: ConfirmActionOptions) => Promise<boolean> {
  const confirm = useConfirm()
  return useCallback(
    ({ title, content, confirmLabel = 'Confirm', danger = true }) =>
      confirm({
        title,
        content,
        confirmButtonContent: confirmLabel,
        confirmButtonType: danger ? 'danger' : 'primary',
      }),
    [confirm],
  )
}
