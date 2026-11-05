import { useCallback, useEffect, useRef, useState } from 'react'
import { listTags } from '../../api/endpoints'
import type { TagResponse } from '../../api/models'

export function useTags(org: string, project: string) {
  const [tags, setTags] = useState<TagResponse[] | null>(null)
  const started = useRef(false)
  const scope = useRef(0)

  useEffect(() => {
    scope.current += 1
    started.current = false
    setTags(null)
  }, [org, project])

  const load = useCallback(() => {
    if (started.current) return
    started.current = true
    const generation = scope.current
    listTags(org, project, { limit: 100 })
      .then((result) => {
        if (scope.current === generation) setTags(result)
      })
      .catch(() => {
        if (scope.current !== generation) return
        started.current = false
        setTags([])
      })
  }, [org, project])

  return { tags, load }
}
