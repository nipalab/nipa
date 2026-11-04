import { Avatar } from '@primer/react'

export interface ActorLike {
  user_id: string
  name?: string
  photo_url?: string
}

export function actorName(actor: ActorLike | undefined): string {
  if (!actor) return 'unknown'
  return actor.name || actor.user_id
}

export function ActorAvatar({ actor, size = 20 }: { actor?: ActorLike; size?: number }) {
  const label = actorName(actor)
  if (actor?.photo_url) {
    return <Avatar src={actor.photo_url} size={size} alt={label} />
  }
  return (
    <span
      aria-hidden="true"
      title={label}
      style={{
        width: size,
        height: size,
        borderRadius: '50%',
        background: 'var(--bgColor-neutral-emphasis)',
        color: 'var(--fgColor-onEmphasis)',
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: Math.round(size * 0.45),
        fontWeight: 600,
        flexShrink: 0,
      }}
    >
      {label.slice(0, 2).toUpperCase()}
    </span>
  )
}

export function mergeActorMaps(...maps: Map<string, ActorLike>[]): Map<string, ActorLike> {
  const merged = new Map<string, ActorLike>()
  for (const map of maps) {
    for (const [id, actor] of map) {
      if (!merged.has(id)) merged.set(id, actor)
    }
  }
  return merged
}

export function actorsFrom(
  ...groups: (ActorLike | undefined)[][]
): Map<string, ActorLike> {
  const actors = new Map<string, ActorLike>()
  for (const group of groups) {
    for (const actor of group) {
      if (actor && !actors.has(actor.user_id)) actors.set(actor.user_id, actor)
    }
  }
  return actors
}
