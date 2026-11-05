import { useCallback, useEffect, useRef, useState } from 'react'

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

interface AsyncState<T> {
  data: T | null
  error: string | null
  loading: boolean
  reload: () => void
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): AsyncState<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => setNonce((value) => value + 1), [])

  useEffect(() => {
    let active = true
    setLoading(true)
    setError(null)
    fn()
      .then((result) => {
        if (active) {
          setData(result)
          setLoading(false)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(errorMessage(err))
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])

  return { data, error, loading, reload }
}

export function useAction(onSuccess?: () => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const onSuccessRef = useRef(onSuccess)
  onSuccessRef.current = onSuccess

  const run = useCallback(async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true)
    setError(null)
    try {
      const result = await fn()
      onSuccessRef.current?.()
      return result
    } catch (err) {
      setError(errorMessage(err))
      return undefined
    } finally {
      setBusy(false)
    }
  }, [])

  const clearError = useCallback(() => setError(null), [])

  return { run, busy, error, clearError }
}

export function useClipboard(resetMs = 2000) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = useCallback(
    async (text: string) => {
      try {
        await navigator.clipboard.writeText(text)
      } catch {
        return false
      }
      setCopied(true)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), resetMs)
      return true
    },
    [resetMs],
  )

  return { copied, copy }
}

export interface PageResult<T, C> {
  items: T[]
  next?: C
}

export function usePaged<T, C>(fetchPage: (cursor: C | undefined) => Promise<PageResult<T, C>>, deps: unknown[]) {
  const first = useAsync(() => fetchPage(undefined), deps)
  const [more, setMore] = useState<PageResult<T, C> | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState<string | null>(null)

  useEffect(() => {
    setMore(null)
    setMoreError(null)
  }, [first.data])

  const next = more ? more.next : first.data?.next
  const items = [...(first.data?.items ?? []), ...(more?.items ?? [])]

  const loadMore = useCallback(async () => {
    if (next === undefined || loadingMore) return
    setLoadingMore(true)
    setMoreError(null)
    try {
      const page = await fetchPage(next)
      setMore((current) => ({ items: [...(current?.items ?? []), ...page.items], next: page.next }))
    } catch (err) {
      setMoreError(errorMessage(err))
    } finally {
      setLoadingMore(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [next, loadingMore, ...deps])

  return {
    items,
    loading: first.loading,
    error: first.error ?? moreError,
    hasMore: next !== undefined,
    loadingMore,
    loadMore,
    reload: first.reload,
  }
}

export function sliceCursorPage<T, C>(rows: T[], limit: number, cursorOf: (rows: T[]) => C): PageResult<T, C> {
  if (rows.length <= limit) return { items: rows }
  return { items: rows.slice(0, limit), next: cursorOf(rows) }
}
