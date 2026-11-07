import { Banner } from '@primer/react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'

export function isSeatLimitError(err: unknown): err is ApiError {
  return err instanceof ApiError && err.status === 402 && /seat/i.test(err.message)
}

export function SeatLimitBanner({ message }: { message: string }) {
  return (
    <Banner variant="warning" title="Seat limit reached">
      {message} Everyone added as an organization member, a group member or a user access rule takes a seat.{' '}
      <Link to="/settings/billing">Manage seats in billing settings</Link>.
    </Banner>
  )
}
