const UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ['year', 365 * 24 * 60 * 60],
  ['month', 30 * 24 * 60 * 60],
  ['week', 7 * 24 * 60 * 60],
  ['day', 24 * 60 * 60],
  ['hour', 60 * 60],
  ['minute', 60],
  ['second', 1],
]

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

export function relativeTime(iso?: string): string {
  if (!iso) return ''
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return ''
  const seconds = (then - Date.now()) / 1000
  const abs = Math.abs(seconds)
  for (const [unit, size] of UNITS) {
    if (abs >= size || unit === 'second') {
      return relative.format(Math.round(seconds / size), unit)
    }
  }
  return ''
}

export function absoluteTime(iso?: string): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString()
}

export function formatBytes(bytes?: number): string {
  if (bytes === undefined || !Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  const rounded = value >= 10 ? Math.round(value) : Math.round(value * 10) / 10
  return `${rounded} ${units[unit]}`
}

export function shortSha(hash?: string, length = 7): string {
  return hash ? hash.slice(0, length) : ''
}

export function commitTitle(message: string): string {
  const line = message.split('\n', 1)[0] ?? ''
  return line.trim() || 'Untitled commit'
}
