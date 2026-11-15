import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { APP_HOME } from './routes'
import { Spinner, Stack } from '@primer/react'
import { bootstrap, clearSession, logout as apiLogout, subscribeSession } from './api/client'
import { getMe } from './api/endpoints'
import type { UserResponse } from './api/models'
import { clearOrgMembersCache } from './orgMembersCache'
import { closesApp, effectiveWindow, useMaintenance, windowsFor } from './maintenance'
import { MaintenancePage } from './pages/MaintenancePage'

type Status = 'loading' | 'authenticated' | 'anonymous'

interface AuthState {
  status: Status
  me: UserResponse | null
  signIn: () => void
  signOut: () => Promise<void>
  refreshMe: () => Promise<void>
  restore: () => void
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

  const restore = useCallback(() => {
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
  }, [])

  useEffect(
    () =>
      subscribeSession((event) => {
        if (event === 'logout') {
          setMe(null)
          setStatus('anonymous')
          return
        }
        restore()
      }),
    [restore],
  )

  useEffect(() => {
    if (status === 'anonymous') clearOrgMembersCache()
  }, [status])

  async function signOut() {
    await apiLogout()
    clearOrgMembersCache()
    setMe(null)
    setStatus('anonymous')
  }

  async function refreshMe() {
    setMe(await getMe())
  }

  return (
    <AuthContext.Provider
      value={{ status, me, signIn: () => setStatus('authenticated'), signOut, refreshMe, restore }}
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
  const { status, restore } = useAuth()
  const location = useLocation()
  const maintenance = useMaintenance()
  const paused = status === 'anonymous' && maintenance.sessionPaused
  const closed = paused ? effectiveWindow(windowsFor(maintenance)) : null
  const blocked = paused && closesApp(closed, true)

  useEffect(() => {
    if (paused && !blocked) restore()
  }, [paused, blocked, restore])

  if (blocked && closed) return <MaintenancePage item={closed} />
  if (paused) {
    return (
      <Stack direction="vertical" align="center" style={{ marginTop: 64 }}>
        <Spinner size="large" />
      </Stack>
    )
  }

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

export function RedirectIfAuthenticated() {
  const { status } = useAuth()
  const location = useLocation()

  if (status === 'loading') {
    return (
      <Stack direction="vertical" align="center" style={{ marginTop: 64 }}>
        <Spinner size="large" />
      </Stack>
    )
  }
  if (status === 'authenticated') {
    const from = (location.state as { from?: string } | null)?.from ?? APP_HOME
    return <Navigate to={from} replace />
  }
  return <Outlet />
}
