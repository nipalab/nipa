import { Button } from '@primer/react'

export function LoadMore({
  hasMore,
  loading,
  onClick,
}: {
  hasMore: boolean
  loading: boolean
  onClick: () => void
}) {
  if (!hasMore) return null
  return (
    <div style={{ display: 'flex', justifyContent: 'center' }}>
      <Button onClick={onClick} loading={loading}>
        Load more
      </Button>
    </div>
  )
}
