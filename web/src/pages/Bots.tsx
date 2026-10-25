import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, errorMessage, type Bot, type Device, type Flow } from '../api'

export default function Bots() {
  const [bots, setBots] = useState<Bot[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [error, setError] = useState('')

  const load = () =>
    Promise.all([api<Bot[]>('/api/bots'), api<Device[]>('/api/devices')])
      .then(([b, d]) => {
        setBots(b)
        setDevices(d)
      })
      .catch((e) => setError(errorMessage(e)))

  useEffect(() => {
    load()
    // The state changes as people use the bot on WhatsApp.
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [])

  const setState = async (bot: Bot, state: string) => {
    setError('')
    try {
      await api(`/api/bots/${bot.id}/state`, 'PUT', { state })
      load()
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  const deviceName = (id: number | null) => devices.find((d) => d.id === id)?.name

  return (
    <section>
      <div className="section-head">
        <h2>Bots</h2>
        <div className="actions">
          <Link className="button primary" to="/bots/novo?tipo=portao">
            Novo bot de portão
          </Link>
          <Link className="button" to="/bots/novo?tipo=flow">
            Novo fluxo em branco
          </Link>
          <Link className="button" to="/bots/novo?tipo=alert">
            Novo alerta
          </Link>
        </div>
      </div>
      <p className="muted">
        Cada contato ou grupo conversa com um único bot. Bots de <b>fluxo</b> respondem com menus e enviam comandos ao
        dispositivo; bots de <b>alerta</b> ligam o alerta do dispositivo quando chega mensagem e desligam quando você
        responde.
      </p>
      {error && <p className="error">{error}</p>}
      {bots.length === 0 && <p className="empty">Nenhum bot criado ainda.</p>}

      <div className="grid">
        {bots.map((bot) => {
          const flow = bot.kind === 'flow' ? (bot.config as Flow) : null
          return (
            <article key={bot.id} className={`card ${bot.enabled ? '' : 'disabled'}`}>
              <div className="row">
                <h3>{bot.name}</h3>
                <span className="badge">{bot.kind === 'flow' ? 'Fluxo' : 'Alerta'}</span>
              </div>
              {!bot.enabled && <p className="muted">Desativado</p>}
              <p className="muted">
                {bot.kind === 'flow'
                  ? 'Responde a comandos com menus.'
                  : 'Não responde mensagens: só liga o alerta (LED e buzzer).'}
              </p>
              <dl>
                <dt>Dispositivo</dt>
                <dd>{deviceName(bot.device_id) ?? <span className="error">nenhum</span>}</dd>
                <dt>Conversa com</dt>
                <dd>
                  {bot.targets.length === 0
                    ? 'ninguém ainda'
                    : bot.targets.map((t) => t.label || t.value).join(', ')}
                </dd>
                {flow && (
                  <>
                    <dt>Comandos</dt>
                    <dd>
                      <code>{[flow.start_command, flow.menu_command].filter(Boolean).join(' ou ')}</code>
                    </dd>
                    <dt>Estado atual</dt>
                    <dd>
                      <select value={bot.current_state} onChange={(e) => setState(bot, e.target.value)}>
                        {flow.states.map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name}
                          </option>
                        ))}
                      </select>
                      <small className="muted block">
                        Ajuste aqui se o estado mudou por fora do bot (ex.: portão fechado pelo controle).
                      </small>
                    </dd>
                  </>
                )}
              </dl>
              <div className="actions">
                <Link className="button" to={`/bots/${bot.id}`}>
                  Editar
                </Link>
              </div>
            </article>
          )
        })}
      </div>
    </section>
  )
}
