import { useCallback, useEffect, useRef, useState } from 'react'
import { listTags } from '../../api/endpoints'
import type { TagResponse } from '../../api/models'

export function useTags(org: string, project: string) {
  const [tags, setTags] = useState<TagResponse[] | null>(null)
  const started = useRef(false)

  useEffect(() => {
    started.current = false
    setTags(null)
  }, [org, project])

  const load = useCallback(() => {
    if (started.current) return
    started.current = true
    listTags(org, project, { limit: 100 })
      .then(setTags)
      .catch(() => {
        started.current = false
        setTags([])
      })
  }, [org, project])

  return { tags, load }
}
