import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type Device } from '../api'

type Draft = Pick<Device, 'name' | 'client_id' | 'cmd_topic' | 'data_topic' | 'status_topic'>

const empty: Draft = { name: '', client_id: '', cmd_topic: '', data_topic: '', status_topic: '' }

export default function Devices() {
  const [devices, setDevices] = useState<Device[]>([])
  // editing is the device being edited, 0 for a new one, null when closed.
  const [editing, setEditing] = useState<number | null>(null)
  const [draft, setDraft] = useState<Draft>(empty)
  const [error, setError] = useState('')

  const load = () => api<Device[]>('/api/devices').then(setDevices).catch((e) => setError(errorMessage(e)))

  useEffect(() => {
    load()
    // Status and telemetry arrive from the boards over MQTT.
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [])

  const open = (d?: Device) => {
    setError('')
    setEditing(d?.id ?? 0)
    setDraft(d ? { ...d } : empty)
  }

  // Suggest topics from the client ID, the usual device_type/device_id/function layout.
  const suggestTopics = () => {
    const base = `pingzap/${draft.client_id.trim() || 'dispositivo'}`
    setDraft({ ...draft, cmd_topic: `${base}/cmd`, data_topic: `${base}/data`, status_topic: `${base}/status` })
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      if (editing) await api(`/api/devices/${editing}`, 'PUT', draft)
      else await api('/api/devices', 'POST', draft)
      setEditing(null)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const remove = async (d: Device) => {
    if (!confirm(`Remover o dispositivo "${d.name}"? Os bots ligados a ele ficam sem dispositivo.`)) return
    await api(`/api/devices/${d.id}`, 'DELETE').catch((e) => setError(errorMessage(e)))
    load()
  }

  const regenerate = async (d: Device) => {
    if (!confirm('Gerar um novo token? A placa para de buscar a configuração até receber o novo token.')) return
    await api(`/api/devices/${d.id}/token`, 'POST').catch((e) => setError(errorMessage(e)))
    load()
  }

  const field = (key: keyof Draft, label: string, hint?: string) => (
    <label>
      {label}
      <input value={draft[key]} onChange={(e) => setDraft({ ...draft, [key]: e.target.value })} required />
      {hint && <small className="muted">{hint}</small>}
    </label>
  )

  return (
    <section>
      <div className="section-head">
        <h2>Dispositivos</h2>
        <button className="primary" onClick={() => open()}>
          Novo dispositivo
        </button>
      </div>
      <p className="muted">
        Cada ESP32 busca aqui, com o seu token, o tópico de onde recebe comandos e os tópicos para onde envia a
        telemetria e o status.
      </p>

      {editing !== null && (
        <form className="card form" onSubmit={save}>
          <h3>{editing ? 'Editar dispositivo' : 'Novo dispositivo'}</h3>
          {field('name', 'Nome', 'Ex.: Portão da garagem')}
          {field('client_id', 'Client ID MQTT', 'Único no broker; a placa se conecta com ele.')}
          <div className="row">
            <span className="muted">Tópicos</span>
            <button type="button" className="link" onClick={suggestTopics}>
              Sugerir a partir do client ID
            </button>
          </div>
          {field('cmd_topic', 'Comandos (pingzap → ESP32)')}
          {field('data_topic', 'Telemetria (ESP32 → pingzap)')}
          {field('status_topic', 'Status online/offline (ESP32 → pingzap)')}
          {error && <p className="error">{error}</p>}
          <div className="actions">
            <button className="primary">Salvar</button>
            <button type="button" onClick={() => setEditing(null)}>
              Cancelar
            </button>
          </div>
        </form>
      )}

      {devices.length === 0 && editing === null && <p className="empty">Nenhum dispositivo cadastrado.</p>}

      <div className="grid">
        {devices.map((d) => (
          <article key={d.id} className="card">
            <div className="row">
              <h3>{d.name}</h3>
              <span className={`badge ${d.status === 'online' ? 'connected' : 'disconnected'}`}>
                {d.status || 'sem notícias'}
              </span>
            </div>
            <dl>
              <dt>Client ID</dt>
              <dd>
                <code>{d.client_id}</code>
              </dd>
              <dt>Comandos</dt>
              <dd>
                <code>{d.cmd_topic}</code>
              </dd>
              <dt>Telemetria</dt>
              <dd>
                <code>{d.data_topic}</code>
              </dd>
              <dt>Status</dt>
              <dd>
                <code>{d.status_topic}</code>
              </dd>
              <dt>Token</dt>
              <dd>
                <code className="secret">{d.token}</code>
              </dd>
              {d.last_seen && (
                <>
                  <dt>Última mensagem</dt>
                  <dd>
                    {new Date(d.last_seen).toLocaleString('pt-BR')}
                    {d.last_data && <code className="block">{d.last_data}</code>}
                  </dd>
                </>
              )}
            </dl>
            <div className="actions">
              <button onClick={() => open(d)}>Editar</button>
              <button onClick={() => navigator.clipboard?.writeText(d.token)}>Copiar token</button>
              <button onClick={() => regenerate(d)}>Novo token</button>
              <button className="danger" onClick={() => remove(d)}>
                Remover
              </button>
            </div>
          </article>
        ))}
      </div>
    </section>
  )
}
