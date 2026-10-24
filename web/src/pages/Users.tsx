import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type User } from '../api'

export default function Users({ current }: { current: User }) {
  const [users, setUsers] = useState<User[]>([])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () => api<User[]>('/api/users').then(setUsers).catch((e) => setError(errorMessage(e)))
  useEffect(() => {
    load()
  }, [])

  const run = async (action: () => Promise<unknown>, done?: string) => {
    setError('')
    setNotice('')
    try {
      await action()
      if (done) setNotice(done)
      load()
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  const create = (e: FormEvent) => {
    e.preventDefault()
    run(async () => {
      await api('/api/users', 'POST', { username, password })
      setUsername('')
      setPassword('')
    }, 'Usuário criado.')
  }

  const changePassword = (u: User) => {
    const pw = prompt(`Nova senha para ${u.username} (mínimo de 8 caracteres):`)
    if (pw) run(() => api(`/api/users/${u.id}/password`, 'PUT', { password: pw }), 'Senha alterada.')
  }

  const remove = (u: User) => {
    if (confirm(`Remover o usuário ${u.username}?`)) run(() => api(`/api/users/${u.id}`, 'DELETE'))
  }

  return (
    <section>
      <h2>Usuários</h2>
      <div className="card">
        <table>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>
                  {u.username}
                  {u.id === current.id && <span className="muted"> (você)</span>}
                </td>
                <td className="actions">
                  <button onClick={() => changePassword(u)}>Trocar senha</button>
                  {u.id !== current.id && (
                    <button className="danger" onClick={() => remove(u)}>
                      Remover
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <form className="card form" onSubmit={create}>
        <h3>Novo usuário</h3>
        <label>
          Usuário
          <input value={username} onChange={(e) => setUsername(e.target.value)} required />
        </label>
        <label>
          Senha
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} minLength={8} required />
        </label>
        <div className="actions">
          <button className="primary">Criar usuário</button>
        </div>
      </form>
      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}
    </section>
  )
}
