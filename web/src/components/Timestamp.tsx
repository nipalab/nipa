import { absoluteTime, relativeTime } from './repo/format'

export function Timestamp({
  value,
  mode = 'relative',
  fallback = '',
}: {
  value?: string | null
  mode?: 'relative' | 'absolute'
  fallback?: string
}) {
  const absolute = absoluteTime(value ?? undefined)
  if (!value || !absolute) return <>{fallback}</>
  return (
    <time dateTime={value} title={absolute}>
      {mode === 'relative' ? relativeTime(value) : absolute}
    </time>
  )
}
