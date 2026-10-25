import { useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { api, setUnauthorizedHandler, type User } from './api'
import Login from './pages/Login'
import WhatsApp from './pages/WhatsApp'
import Devices from './pages/Devices'
import Bots from './pages/Bots'
import BotEditor from './pages/BotEditor'
import Users from './pages/Users'

type AuthStatus = { setup_required: boolean; user?: User }

export default function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null)

  const refresh = () => api<AuthStatus>('/api/auth/status').then(setAuth)

  useEffect(() => {
    refresh()
    setUnauthorizedHandler(() => setAuth({ setup_required: false }))
  }, [])

  if (!auth) return <div className="center muted">Carregando…</div>
  if (!auth.user) return <Login setupRequired={auth.setup_required} onLogin={refresh} />

  const logout = async () => {
    await api('/api/auth/logout', 'POST')
    setAuth({ setup_required: false })
  }

  return (
    <div className="layout">
      <header className="topbar">
        <span className="brand">pingzap</span>
        <nav>
          <NavLink to="/whatsapp">WhatsApp</NavLink>
          <NavLink to="/bots">Bots</NavLink>
          <NavLink to="/dispositivos">Dispositivos</NavLink>
          <NavLink to="/usuarios">Usuários</NavLink>
        </nav>
        <span className="who">
          {auth.user.username}
          <button className="link" onClick={logout}>
            Sair
          </button>
        </span>
      </header>
      <main>
        <Routes>
          <Route path="/whatsapp" element={<WhatsApp />} />
          <Route path="/bots" element={<Bots />} />
          <Route path="/bots/:id" element={<BotEditor />} />
          <Route path="/dispositivos" element={<Devices />} />
          <Route path="/usuarios" element={<Users current={auth.user} />} />
          <Route path="*" element={<Navigate to="/bots" replace />} />
        </Routes>
      </main>
    </div>
  )
}
