import { useCallback, useState } from 'react'
import { resendVerification } from './api/endpoints'
import { errorMessage } from './hooks'

export const RESEND_NOTICE = 'If the account is unconfirmed, a new email is on its way.'

export function useResendVerification() {
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const resend = useCallback(async (email: string) => {
    const address = email.trim()
    if (!address) return false
    setBusy(true)
    setNotice(null)
    setError(null)
    try {
      await resendVerification(address)
      setNotice(RESEND_NOTICE)
      return true
    } catch (err) {
      setError(errorMessage(err))
      return false
    } finally {
      setBusy(false)
    }
  }, [])

  const reset = useCallback(() => {
    setNotice(null)
    setError(null)
  }, [])

  return { resend, busy, notice, error, reset }
}
