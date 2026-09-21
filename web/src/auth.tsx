import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { Spinner, Stack } from '@primer/react'
import { bootstrap, clearSession, logout as apiLogout, subscribeSession } from './api/client'
import { getMe } from './api/endpoints'
import type { UserResponse } from './api/models'

type Status = 'loading' | 'authenticated' | 'anonymous'

interface AuthState {
  status: Status
  me: UserResponse | null
  signIn: () => void
  signOut: () => Promise<void>
  refreshMe: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('loading')
  const [me, setMe] = useState<UserResponse | null>(null)

  useEffect(() => {
    let active = true
    bootstrap().then(async (authenticated) => {
      if (!active) return
      if (!authenticated) {
        setStatus('anonymous')
        return
      }
      try {
        setMe(await getMe())
      } catch {
        clearSession()
        setStatus('anonymous')
        return
      }
      setStatus('authenticated')
    })
    return () => {
      active = false
    }
  }, [])

  useEffect(
    () =>
      subscribeSession((event) => {
        if (event === 'logout') {
          setMe(null)
          setStatus('anonymous')
          return
        }
        bootstrap().then(async (authenticated) => {
          if (!authenticated) {
            setStatus('anonymous')
            return
          }
          try {
            setMe(await getMe())
          } catch {
            setMe(null)
          }
          setStatus('authenticated')
        })
      }),
    [],
  )

  async function signOut() {
    await apiLogout()
    setMe(null)
    setStatus('anonymous')
  }

  async function refreshMe() {
    setMe(await getMe())
  }

  return (
    <AuthContext.Provider
      value={{ status, me, signIn: () => setStatus('authenticated'), signOut, refreshMe }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthState {
  const value = useContext(AuthContext)
  if (!value) {
    throw new Error('useAuth must be used inside AuthProvider')
  }
  return value
}

export function RequireAuth() {
  const { status } = useAuth()
  const location = useLocation()

  if (status === 'loading') {
    return (
      <Stack direction="vertical" align="center" style={{ marginTop: 64 }}>
        <Spinner size="large" />
      </Stack>
    )
  }
  if (status === 'anonymous') {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }
  return <Outlet />
}
