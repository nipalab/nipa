import { useEffect, useState } from 'react'
import { Spinner, Stack } from '@primer/react'
import HomePage from './pages/HomePage'
import LoginPage from './pages/LoginPage'
import { bootstrap, logout, subscribeSession } from './api/client'

type Status = 'loading' | 'authenticated' | 'anonymous'

export default function App() {
  const [status, setStatus] = useState<Status>('loading')

  useEffect(() => {
    let active = true
    bootstrap().then((authenticated) => {
      if (active) {
        setStatus(authenticated ? 'authenticated' : 'anonymous')
      }
    })
    return () => {
      active = false
    }
  }, [])

  useEffect(
    () =>
      subscribeSession((event) => {
        if (event === 'logout') {
          setStatus('anonymous')
          return
        }
        bootstrap().then((authenticated) => {
          setStatus(authenticated ? 'authenticated' : 'anonymous')
        })
      }),
    [],
  )

  async function handleLogout() {
    await logout()
    setStatus('anonymous')
  }

  if (status === 'loading') {
    return (
      <Stack direction="vertical" align="center" style={{ marginTop: 64 }}>
        <Spinner size="large" />
      </Stack>
    )
  }

  if (status === 'anonymous') {
    return <LoginPage onLogin={() => setStatus('authenticated')} />
  }

  return <HomePage onLogout={handleLogout} />
}
