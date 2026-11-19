import { Label } from '@primer/react'
import { CopyButton } from '../CopyButton'
import { originShortId, originSystemName } from './origin'

export function ImportedLabel({ origin }: { origin?: string }) {
  const system = originSystemName(origin)
  if (!origin || !system) return null
  const short = originShortId(origin)
  return (
    <span className="nipa-imported" title={origin}>
      <Label variant="secondary">Imported from {system}</Label>
      <span className="nipa-imported-id">{short}</span>
      <CopyButton value={origin} label={`Copy ${system} origin ${short}`} />
    </span>
  )
}
