import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import {
  api,
  errorMessage,
  type AlertConfig,
  type Bot,
  type BotKind,
  type Contact,
  type Device,
  type Flow,
  type FlowOption,
  type FlowState,
  type Group,
  type Target,
} from '../api'

const newId = () => `s${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`

const emptyOption = (): FlowOption => ({ label: '', shortcut: '', command: '', confirm: '', next_state: '', reply: '' })

const blankFlow = (): Flow => ({
  start_command: '/start',
  menu_command: '/menu',
  reminder_minutes: 3,
  max_attempts: 3,
  alarm_after_reminders: 3,
  alarm_command: 'ON',
  alarm_clear_command: 'OFF',
  states: [{ id: newId(), name: 'Inicial', menu: '', options: [emptyOption()] }],
})

const blankAlert = (): AlertConfig => ({ keyword: '', on_payload: 'ON', off_payload: 'OFF' })

async function newBot(tipo: string | null): Promise<Bot> {
  const base = { id: 0, enabled: true, device_id: null, current_state: '', targets: [] }
  if (tipo === 'alert') return { ...base, name: 'Alerta', kind: 'alert', config: blankAlert() }
  if (tipo === 'portao') {
    const flow = await api<Flow>('/api/templates/gate')
    return { ...base, name: 'Portão', kind: 'flow', config: flow }
  }
  return { ...base, name: '', kind: 'flow', config: blankFlow() }
}

export default function BotEditor() {
  const { id } = useParams()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const isNew = id === 'novo'

  const [bot, setBot] = useState<Bot | null>(null)
  const [devices, setDevices] = useState<Device[]>([])
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<Device[]>('/api/devices').then(setDevices)
    const load = isNew ? newBot(params.get('tipo')) : api<Bot>(`/api/bots/${id}`)
    load.then((b) => setBot(b)).catch((e) => setError(errorMessage(e)))
  }, [id])

  if (!bot) return error ? <p className="error">{error}</p> : <p className="muted">Carregando…</p>

  const update = (patch: Partial<Bot>) => setBot({ ...bot, ...patch })

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setError('')
    try {
      if (isNew) await api('/api/bots', 'POST', bot)
      else await api(`/api/bots/${bot.id}`, 'PUT', bot)
      navigate('/bots')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!confirm(`Remover o bot "${bot.name}"?`)) return
    try {
      await api(`/api/bots/${bot.id}`, 'DELETE')
      navigate('/bots')
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={save}>
      <div className="section-head">
        <h2>{isNew ? 'Novo bot' : bot.name}</h2>
        <div className="actions">
          <button type="button" onClick={() => navigate('/bots')}>
            Voltar
          </button>
          {!isNew && (
            <button type="button" className="danger" onClick={remove}>
              Remover
            </button>
          )}
          <button className="primary" disabled={saving}>
            Salvar
          </button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}

      <div className="card form">
        <h3>Geral</h3>
        <label>
          Nome
          <input value={bot.name} onChange={(e) => update({ name: e.target.value })} required />
        </label>
        <label>
          Tipo
          <select
            value={bot.kind}
            disabled={!isNew}
            onChange={(e) => {
              const kind = e.target.value as BotKind
              update({ kind, config: kind === 'flow' ? blankFlow() : blankAlert() })
            }}
          >
            <option value="flow">Fluxo de conversa</option>
            <option value="alert">Alerta</option>
          </select>
        </label>
        <label>
          Dispositivo
          <select
            value={bot.device_id ?? ''}
            onChange={(e) => update({ device_id: e.target.value ? Number(e.target.value) : null })}
          >
            <option value="">— nenhum —</option>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} ({d.cmd_topic})
              </option>
            ))}
          </select>
          <small className="muted">Os comandos do bot são publicados no tópico de comandos deste dispositivo.</small>
        </label>
        <label className="check">
          <input type="checkbox" checked={bot.enabled} onChange={(e) => update({ enabled: e.target.checked })} />
          Ativo
        </label>
      </div>

      <TargetsEditor targets={bot.targets} onChange={(targets) => update({ targets })} />

      {bot.kind === 'flow' ? (
        <FlowEditor flow={bot.config as Flow} onChange={(config) => update({ config })} />
      ) : (
        <AlertEditor config={bot.config as AlertConfig} onChange={(config) => update({ config })} />
      )}

      {!isNew && (
        <p className="muted">Salvar reinicia as conversas em andamento deste bot.</p>
      )}
    </form>
  )
}

function TargetsEditor({ targets, onChange }: { targets: Target[]; onChange: (t: Target[]) => void }) {
  const [contacts, setContacts] = useState<Contact[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [listError, setListError] = useState('')
  const [number, setNumber] = useState('')
  const [label, setLabel] = useState('')
  const [group, setGroup] = useState('')

  useEffect(() => {
    Promise.all([api<Contact[]>('/api/whatsapp/contacts'), api<Group[]>('/api/whatsapp/groups')])
      .then(([c, g]) => {
        setContacts(c)
        setGroups(g)
      })
      .catch(() => setListError('Conecte o WhatsApp para escolher contatos e grupos da lista.'))
  }, [])

  const addContact = () => {
    const value = number.trim()
    if (!value) return
    const known = contacts.find((c) => c.phone === value.replace(/\D/g, ''))
    onChange([...targets, { kind: 'contact', value, label: label.trim() || known?.name || '' }])
    setNumber('')
    setLabel('')
  }

  const addGroup = () => {
    const g = groups.find((g) => g.jid === group)
    if (!g) return
    onChange([...targets, { kind: 'group', value: g.jid, label: g.name }])
    setGroup('')
  }

  return (
    <div className="card form">
      <h3>Quem conversa com este bot</h3>
      {targets.length === 0 && <p className="muted">Nenhum contato ou grupo. O bot não responde a ninguém.</p>}
      {targets.length > 0 && <ul className="chips">
        {targets.map((t, i) => (
          <li key={`${t.kind}:${t.value}`}>
            <span className="badge">{t.kind === 'group' ? 'Grupo' : 'Contato'}</span> {t.label || t.value}
            {t.label && t.kind === 'contact' && <span className="muted"> {t.value}</span>}
            <button type="button" className="link" onClick={() => onChange(targets.filter((_, j) => j !== i))}>
              remover
            </button>
          </li>
        ))}
      </ul>}
      {listError && <p className="muted">{listError}</p>}

      <div className="inline">
        <input
          list="contacts"
          placeholder="Número (DDD + telefone) ou contato"
          value={number}
          onChange={(e) => setNumber(e.target.value)}
        />
        <datalist id="contacts">
          {contacts.map((c) => (
            <option key={c.phone} value={c.phone}>
              {c.name}
            </option>
          ))}
        </datalist>
        <input placeholder="Nome (opcional)" value={label} onChange={(e) => setLabel(e.target.value)} />
        <button type="button" onClick={addContact}>
          Adicionar contato
        </button>
      </div>

      {groups.length > 0 && (
        <div className="inline">
          <select value={group} onChange={(e) => setGroup(e.target.value)}>
            <option value="">— escolha um grupo —</option>
            {groups.map((g) => (
              <option key={g.jid} value={g.jid}>
                {g.name}
              </option>
            ))}
          </select>
          <button type="button" onClick={addGroup}>
            Adicionar grupo
          </button>
        </div>
      )}
      <small className="muted">Em grupos, qualquer participante pode usar o bot.</small>
    </div>
  )
}

function AlertEditor({ config, onChange }: { config: AlertConfig; onChange: (c: AlertConfig) => void }) {
  return (
    <div className="card form">
      <h3>Alerta</h3>
      <p className="warning">
        Este bot <b>não responde</b> no WhatsApp e não tem menu: ele só liga o alerta (LED e buzzer) do dispositivo.
        Para um bot que responde a <code>/start</code> com opções, crie um <b>bot de portão</b> ou um <b>fluxo</b> na
        página Bots.
      </p>
      <p className="muted">
        Uma mensagem dos contatos acima publica o comando de ligar. Quando você responde a todos que dispararam, o
        comando de desligar é publicado.
      </p>
      <label>
        Palavra-chave
        <input value={config.keyword} onChange={(e) => onChange({ ...config, keyword: e.target.value })} />
        <small className="muted">Ex.: /urgente. Vazio = qualquer mensagem dispara.</small>
      </label>
      <div className="inline">
        <label>
          Comando para ligar
          <input value={config.on_payload} onChange={(e) => onChange({ ...config, on_payload: e.target.value })} required />
        </label>
        <label>
          Comando para desligar
          <input value={config.off_payload} onChange={(e) => onChange({ ...config, off_payload: e.target.value })} required />
        </label>
      </div>
    </div>
  )
}

function FlowEditor({ flow, onChange }: { flow: Flow; onChange: (f: Flow) => void }) {
  const setState = (i: number, s: FlowState) => onChange({ ...flow, states: flow.states.map((x, j) => (j === i ? s : x)) })

  const addState = () =>
    onChange({
      ...flow,
      states: [...flow.states, { id: newId(), name: `Estado ${flow.states.length + 1}`, menu: '', options: [emptyOption()] }],
    })

  const removeState = (i: number) => {
    const gone = flow.states[i].id
    // Options that led to the removed state now keep the current state.
    const states = flow.states
      .filter((_, j) => j !== i)
      .map((s) => ({ ...s, options: s.options.map((o) => (o.next_state === gone ? { ...o, next_state: '' } : o)) }))
    onChange({ ...flow, states })
  }

  return (
    <>
      <div className="card form">
        <h3>Fluxo</h3>
        <div className="inline">
          <label>
            Comando de início
            <input value={flow.start_command} onChange={(e) => onChange({ ...flow, start_command: e.target.value })} />
          </label>
          <label>
            Comando de menu
            <input value={flow.menu_command} onChange={(e) => onChange({ ...flow, menu_command: e.target.value })} />
          </label>
        </div>
        <div className="inline">
          <label>
            Insistir sem resposta a cada (min)
            <input
              type="number"
              min={0}
              value={flow.reminder_minutes}
              onChange={(e) => onChange({ ...flow, reminder_minutes: Number(e.target.value) })}
            />
            <small className="muted">0 = não insiste.</small>
          </label>
          <label>
            Máximo de tentativas
            <input
              type="number"
              min={0}
              value={flow.max_attempts}
              onChange={(e) => onChange({ ...flow, max_attempts: Number(e.target.value) })}
            />
            <small className="muted">Envios do comando quando a resposta é "não". 0 = sem limite.</small>
          </label>
        </div>
        <div className="inline">
          <label>
            Alarme após (lembretes sem resposta)
            <input
              type="number"
              min={0}
              value={flow.alarm_after_reminders ?? 0}
              onChange={(e) => onChange({ ...flow, alarm_after_reminders: Number(e.target.value) })}
            />
            <small className="muted">
              Ninguém confirmou? Aciona o alarme do dispositivo (LED e buzzer) para alguém verificar. 0 = nunca.
            </small>
          </label>
          <label>
            Comando do alarme
            <input
              value={flow.alarm_command ?? ''}
              disabled={!flow.alarm_after_reminders}
              onChange={(e) => onChange({ ...flow, alarm_command: e.target.value })}
            />
          </label>
          <label>
            Comando para desligar
            <input
              value={flow.alarm_clear_command ?? ''}
              disabled={!flow.alarm_after_reminders}
              placeholder="vazio = não desliga"
              onChange={(e) => onChange({ ...flow, alarm_clear_command: e.target.value })}
            />
            <small className="muted">Enviado quando alguém responde.</small>
          </label>
        </div>
        <p className="muted">
          O bot tem um estado atual, compartilhado por todos que conversam com ele. Cada estado mostra o seu menu; cada
          opção pode enviar um comando ao dispositivo, pedir confirmação (1 Sim / 2 Não) e mudar o estado.
        </p>
      </div>

      {flow.states.map((s, i) => (
        <StateEditor
          key={s.id}
          state={s}
          states={flow.states}
          onChange={(st) => setState(i, st)}
          onRemove={flow.states.length > 1 ? () => removeState(i) : undefined}
        />
      ))}
      <button type="button" onClick={addState}>
        Adicionar estado
      </button>
    </>
  )
}

type StateProps = {
  state: FlowState
  states: FlowState[]
  onChange: (s: FlowState) => void
  onRemove?: () => void
}

function StateEditor({ state, states, onChange, onRemove }: StateProps) {
  const setOption = (i: number, o: FlowOption) => onChange({ ...state, options: state.options.map((x, j) => (j === i ? o : x)) })

  const move = (i: number, delta: number) => {
    const options = [...state.options]
    const [o] = options.splice(i, 1)
    options.splice(i + delta, 0, o)
    onChange({ ...state, options })
  }

  return (
    <div className="card form state">
      <div className="row">
        <h3>Estado: {state.name || 'sem nome'}</h3>
        {onRemove && (
          <button type="button" className="link danger" onClick={onRemove}>
            remover estado
          </button>
        )}
      </div>
      <div className="state-grid">
        <div className="form">
          <label>
            Nome do estado
            <input value={state.name} onChange={(e) => onChange({ ...state, name: e.target.value })} required />
          </label>
          <label>
            Texto do menu
            <textarea
              rows={2}
              value={state.menu}
              placeholder="Ex.: O portão está *fechado*."
              onChange={(e) => onChange({ ...state, menu: e.target.value })}
            />
          </label>

          {state.options.map((o, i) => (
            <fieldset key={i} className="option">
              <legend>
                Opção {i + 1}
                <span className="option-tools">
                  <button type="button" className="link" disabled={i === 0} onClick={() => move(i, -1)}>
                    ↑
                  </button>
                  <button type="button" className="link" disabled={i === state.options.length - 1} onClick={() => move(i, 1)}>
                    ↓
                  </button>
                  {state.options.length > 1 && (
                    <button
                      type="button"
                      className="link danger"
                      onClick={() => onChange({ ...state, options: state.options.filter((_, j) => j !== i) })}
                    >
                      remover
                    </button>
                  )}
                </span>
              </legend>
              <div className="inline">
                <label>
                  Texto da opção
                  <input value={o.label} onChange={(e) => setOption(i, { ...o, label: e.target.value })} required />
                </label>
                <label>
                  Atalho
                  <input
                    value={o.shortcut ?? ''}
                    placeholder="ex.: /abrir"
                    onChange={(e) => setOption(i, { ...o, shortcut: e.target.value.trim() })}
                  />
                  <small className="muted">Executa a opção direto, sem passar pelo menu.</small>
                </label>
              </div>
              <div className="inline">
                <label>
                  Comando MQTT
                  <input
                    value={o.command}
                    placeholder="vazio = só muda o estado"
                    onChange={(e) =>
                      // A confirmation only makes sense after a command.
                      setOption(i, { ...o, command: e.target.value, confirm: e.target.value ? o.confirm : '' })
                    }
                  />
                </label>
                <label>
                  Vai para o estado
                  <select value={o.next_state} onChange={(e) => setOption(i, { ...o, next_state: e.target.value })}>
                    <option value="">— continua no mesmo —</option>
                    {states.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <label>
                Pergunta de confirmação
                <input
                  value={o.confirm}
                  disabled={!o.command}
                  placeholder={o.command ? 'Ex.: O portão abriu? (vazio = não pergunta)' : 'Só para opções com comando'}
                  onChange={(e) => setOption(i, { ...o, confirm: e.target.value })}
                />
              </label>
              <label>
                Resposta ao concluir
                <input
                  value={o.reply}
                  placeholder="vazio = mensagem padrão"
                  onChange={(e) => setOption(i, { ...o, reply: e.target.value })}
                />
              </label>
              <label className="check">
                <input
                  type="checkbox"
                  checked={o.show_in_confirm ?? false}
                  onChange={(e) => setOption(i, { ...o, show_in_confirm: e.target.checked })}
                />
                Oferecer também na confirmação das opções que levam a este estado
              </label>
            </fieldset>
          ))}
          <button type="button" onClick={() => onChange({ ...state, options: [...state.options, emptyOption()] })}>
            Adicionar opção
          </button>
        </div>
        <MenuPreview state={state} />
      </div>
    </div>
  )
}

// MenuPreview shows the message the bot sends for this state, as built by
// the server (internal/flow).
function MenuPreview({ state }: { state: FlowState }) {
  const lines = state.options.map((o, i) => `*${i + 1}* - ${o.label || '…'}${o.shortcut ? ` (${o.shortcut})` : ''}`)
  const text = `${state.menu ? state.menu + '\n\n' : ''}Digite o número da opção:\n${lines.join('\n')}`
  return (
    <div className="preview">
      <small className="muted">Prévia no WhatsApp</small>
      <div className="bubble">{text}</div>
    </div>
  )
}
