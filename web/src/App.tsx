import { useState } from 'react'
import HomePage from './pages/HomePage'
import LoginPage from './pages/LoginPage'
import {
  clearSession,
  isAuthenticated,
  setSession,
} from './api/client'
import type { LoginResponse } from './api/models'

export default function App() {
  const [authed, setAuthed] = useState(isAuthenticated)

  function handleLogin(tokens: LoginResponse) {
    setSession(tokens)
    setAuthed(true)
  }

  function handleLogout() {
    clearSession()
    setAuthed(false)
  }

  if (!authed) {
    return <LoginPage onLogin={handleLogin} />
  }
  return <HomePage onLogout={handleLogout} />
}