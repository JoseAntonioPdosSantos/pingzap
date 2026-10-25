import { useState, type FormEvent } from 'react'
import { api, errorMessage } from '../api'

type Props = { setupRequired: boolean; onLogin: () => void }

export default function Login({ setupRequired, onLogin }: Props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api(setupRequired ? '/api/auth/setup' : '/api/auth/login', 'POST', { username, password })
      onLogin()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="center">
      <form className="card login" onSubmit={submit}>
        <h1>pingzap</h1>
        <p className="muted">
          {setupRequired
            ? 'Primeiro acesso: crie o usuário administrador.'
            : 'Entre para gerenciar os bots e dispositivos.'}
        </p>
        <label>
          Usuário
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus required />
        </label>
        <label>
          Senha
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={setupRequired ? 8 : undefined}
            required
          />
        </label>
        {setupRequired && <small className="muted">Mínimo de 8 caracteres.</small>}
        {error && <p className="error">{error}</p>}
        <button className="primary" disabled={busy}>
          {setupRequired ? 'Criar e entrar' : 'Entrar'}
        </button>
      </form>
    </div>
  )
}
