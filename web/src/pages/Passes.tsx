import { useEffect, useState, type FormEvent } from 'react'
import {
  api,
  errorMessage,
  type BotRemote,
  type Contact,
  type PassInput,
  type PassStatus,
  type PassWindow,
  type RemotePass,
} from '../api'

// Temporary accesses: web remotes made here for people outside the bot's
// chats, limited by dates, number of uses and days/hours of the week.

const DAYS = ['D', 'S', 'T', 'Q', 'Q', 'S', 'S']
const DAY_NAMES = ['domingo', 'segunda', 'terça', 'quarta', 'quinta', 'sexta', 'sábado']
const DAY_SHORT = ['dom', 'seg', 'ter', 'qua', 'qui', 'sex', 'sáb']

const STATUS: Record<PassStatus, { label: string; tone: 'ok' | 'wait' | 'off' }> = {
  active: { label: 'Ativo', tone: 'ok' },
  outside: { label: 'Fora do horário', tone: 'wait' },
  scheduled: { label: 'Ainda não começou', tone: 'wait' },
  expired: { label: 'Expirado', tone: 'off' },
  used_up: { label: 'Usos esgotados', tone: 'off' },
}

const fmt = (iso: string) => new Date(iso).toLocaleString('pt-BR', { dateStyle: 'short', timeStyle: 'short' })

// toLocalInput turns an ISO date into the value of a datetime-local input.
const toLocalInput = (iso?: string) => {
  if (!iso) return ''
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}
const fromLocalInput = (v: string) => (v ? new Date(v).toISOString() : null)
const inHours = (h: number) => toLocalInput(new Date(Date.now() + h * 3600_000).toISOString())

// relative says how long until (or since) a moment, in words.
function relative(iso: string) {
  const diff = new Date(iso).getTime() - Date.now()
  const abs = Math.abs(diff)
  const min = Math.round(abs / 60000)
  const text = min < 60 ? `${min} min` : min < 2880 ? `${Math.round(min / 60)} h` : `${Math.round(min / 1440)} dias`
  return diff >= 0 ? `em ${text}` : `há ${text}`
}

const browserZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'

function describeWindow(w: PassWindow) {
  const days = w.days.length === 7 ? 'todos os dias' : w.days.map((d) => DAY_SHORT[d]).join(', ')
  return `${days} · ${w.start}–${w.end}`
}

/* Form */

type Draft = {
  name: string
  startsAt: string
  expiresAt: string
  maxUses: number
  windows: PassWindow[]
  resetUses: boolean
  // WhatsApp number to send the link to when the access is created.
  phone: string
}

const blankDraft = (): Draft => ({
  name: '',
  startsAt: '',
  expiresAt: '',
  maxUses: 0,
  windows: [],
  resetUses: false,
  phone: '',
})

const draftOf = (p: RemotePass): Draft => ({
  name: p.name,
  startsAt: toLocalInput(p.starts_at),
  expiresAt: toLocalInput(p.expires_at),
  maxUses: p.max_uses,
  windows: p.schedule.windows.map((w) => ({ ...w, days: [...w.days] })),
  resetUses: false,
  phone: '',
})

// Presets fill the form for the most common cases.
const PRESETS: { label: string; hint: string; apply: (d: Draft) => Draft }[] = [
  {
    label: 'Abrir e fechar uma vez',
    hint: '2 usos, vale por 24 horas',
    apply: (d) => ({ ...d, maxUses: 2, startsAt: '', expiresAt: inHours(24), windows: [] }),
  },
  {
    label: 'Por algumas horas',
    hint: 'sem limite de usos, vale por 4 horas',
    apply: (d) => ({ ...d, maxUses: 0, startsAt: '', expiresAt: inHours(4), windows: [] }),
  },
  {
    label: 'Dias e horários fixos',
    hint: 'seg a sex, 08:00–18:00, sem data final',
    apply: (d) => ({
      ...d,
      maxUses: 0,
      startsAt: '',
      expiresAt: '',
      windows: [{ days: [1, 2, 3, 4, 5], start: '08:00', end: '18:00' }],
    }),
  },
]

function PassForm({
  initial,
  editing,
  saving,
  onSubmit,
  onCancel,
}: {
  initial: Draft
  editing: boolean
  saving: boolean
  onSubmit: (d: Draft) => void
  onCancel: () => void
}) {
  const [d, setD] = useState(initial)
  const set = (patch: Partial<Draft>) => setD((cur) => ({ ...cur, ...patch }))
  const setWindow = (i: number, patch: Partial<PassWindow>) =>
    set({ windows: d.windows.map((w, j) => (j === i ? { ...w, ...patch } : w)) })
  const toggleDay = (i: number, day: number) => {
    const w = d.windows[i]
    setWindow(i, { days: w.days.includes(day) ? w.days.filter((x) => x !== day) : [...w.days, day].sort() })
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    onSubmit(d)
  }

  return (
    <form className="card form pass-form" onSubmit={submit}>
      <h3>{editing ? 'Editar acesso' : 'Novo acesso temporário'}</h3>

      {!editing && (
        <div>
          <small className="muted">Comece por um modelo e ajuste o que precisar:</small>
          <div className="pass-presets">
            {PRESETS.map((p) => (
              <button key={p.label} type="button" onClick={() => setD(p.apply(d))}>
                <b>{p.label}</b>
                <small>{p.hint}</small>
              </button>
            ))}
          </div>
        </div>
      )}

      <label>
        Nome <small className="muted">(quem vai usar; aparece no histórico)</small>
        <input value={d.name} maxLength={60} required placeholder="Ex.: Maria (faxina)" onChange={(e) => set({ name: e.target.value })} />
      </label>

      <div className="inline pass-dates">
        <label>
          Válido a partir de <small className="muted">(opcional)</small>
          <input type="datetime-local" value={d.startsAt} onChange={(e) => set({ startsAt: e.target.value })} />
        </label>
        <label>
          Expira em <small className="muted">(opcional)</small>
          <input type="datetime-local" value={d.expiresAt} onChange={(e) => set({ expiresAt: e.target.value })} />
        </label>
      </div>

      <label>
        Limite de usos <small className="muted">(0 = sem limite; 2 = uma abertura e um fechamento)</small>
        <input
          type="number"
          min={0}
          max={1000}
          value={d.maxUses}
          onChange={(e) => set({ maxUses: Math.max(0, Math.floor(Number(e.target.value) || 0)) })}
        />
      </label>

      <div>
        <div className="row">
          <b>Dias e horários permitidos</b>
          <button type="button" className="link" onClick={() => set({ windows: [...d.windows, { days: [1, 2, 3, 4, 5], start: '08:00', end: '18:00' }] })}>
            + adicionar horário
          </button>
        </div>
        {d.windows.length === 0 && <p className="muted pass-always">Sem restrição: funciona em qualquer dia e hora (dentro das datas acima).</p>}
        {d.windows.map((w, i) => (
          <div className="pass-window" key={i}>
            <div className="pass-days" role="group" aria-label="Dias da semana">
              {DAYS.map((l, day) => (
                <button
                  key={day}
                  type="button"
                  className={w.days.includes(day) ? 'on' : ''}
                  aria-pressed={w.days.includes(day)}
                  title={DAY_NAMES[day]}
                  onClick={() => toggleDay(i, day)}
                >
                  {l}
                </button>
              ))}
            </div>
            <div className="pass-times">
              <input type="time" value={w.start} required onChange={(e) => setWindow(i, { start: e.target.value })} />
              <span>até</span>
              <input type="time" value={w.end} required onChange={(e) => setWindow(i, { end: e.target.value })} />
              <button type="button" className="link danger" onClick={() => set({ windows: d.windows.filter((_, j) => j !== i) })}>
                remover
              </button>
            </div>
          </div>
        ))}
        {d.windows.length > 0 && <small className="muted">Horários no fuso {browserZone()}. Se o fim for antes do início, vale até o dia seguinte.</small>}
      </div>

      {!editing && (
        <label>
          Enviar o link por WhatsApp para <small className="muted">(opcional)</small>
          <input
            list="pass-contacts"
            inputMode="tel"
            value={d.phone}
            placeholder="DDD + número, ex.: 67 99999-9999"
            onChange={(e) => set({ phone: e.target.value })}
          />
          <small className="muted">Sem número, o acesso é só criado: depois use “Copiar link” ou “Enviar por WhatsApp” no cartão.</small>
        </label>
      )}

      {editing && (
        <label className="check">
          <input type="checkbox" checked={d.resetUses} onChange={(e) => set({ resetUses: e.target.checked })} />
          Zerar a contagem de usos (renovar o acesso)
        </label>
      )}

      <div className="actions">
        <button className="primary" disabled={saving}>
          {editing ? 'Salvar' : d.phone.trim() ? 'Criar e enviar' : 'Criar acesso'}
        </button>
        <button type="button" onClick={onCancel}>
          Cancelar
        </button>
      </div>
    </form>
  )
}

/* Card */

function WeekStrip({ windows }: { windows: PassWindow[] }) {
  return (
    <div className="pass-week" aria-label="Dias permitidos">
      {DAYS.map((l, day) => {
        const hours = windows.filter((w) => w.days.includes(day)).map((w) => `${w.start}–${w.end}`)
        return (
          <span key={day} className={hours.length ? 'on' : ''} title={`${DAY_NAMES[day]}: ${hours.join(', ') || 'não permitido'}`}>
            {l}
          </span>
        )
      })}
    </div>
  )
}

function PassCard({
  pass,
  onEdit,
  onRevoke,
  onSend,
}: {
  pass: RemotePass
  onEdit: () => void
  onRevoke: () => void
  onSend: (phone: string) => Promise<boolean>
}) {
  const [copied, setCopied] = useState(false)
  const [sending, setSending] = useState<{ phone: string; busy: boolean } | null>(null)
  const st = STATUS[pass.status]
  const link = pass.url || window.location.origin + pass.path

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link)
    } catch {
      window.prompt('Copie o link:', link)
      return
    }
    setCopied(true)
    window.setTimeout(() => setCopied(false), 2000)
  }

  const used = pass.max_uses > 0 ? Math.min(pass.uses, pass.max_uses) : pass.uses
  const done = pass.status === 'expired' || pass.status === 'used_up'

  return (
    <div className={`pass-card ${st.tone}${done ? ' done' : ''}`}>
      <div className="pass-top">
        <b className="pass-name">{pass.name}</b>
        <span className={`pass-badge ${st.tone}`}>{st.label}</span>
      </div>

      <div className="pass-meta">
        {pass.max_uses > 0 ? (
          <div className="pass-uses" title={`${used} de ${pass.max_uses} usos`}>
            <span className="pass-dots">
              {pass.max_uses <= 12 ? (
                Array.from({ length: pass.max_uses }, (_, i) => <i key={i} className={i < used ? 'used' : ''} />)
              ) : (
                <progress value={used} max={pass.max_uses} />
              )}
            </span>
            <span>
              {used}/{pass.max_uses} usos
            </span>
          </div>
        ) : (
          <div className="muted">{pass.uses} usos · sem limite</div>
        )}

        <div>
          {pass.starts_at && new Date(pass.starts_at) > new Date() && <>Começa {fmt(pass.starts_at)} ({relative(pass.starts_at)}) · </>}
          {pass.expires_at ? (
            <>
              {new Date(pass.expires_at) > new Date() ? 'Expira' : 'Expirou'} {fmt(pass.expires_at)}{' '}
              <span className="muted">({relative(pass.expires_at)})</span>
            </>
          ) : (
            <span className="muted">Sem data para expirar</span>
          )}
        </div>

        {pass.schedule.windows.length > 0 && (
          <div className="pass-sched">
            <WeekStrip windows={pass.schedule.windows} />
            <ul>
              {pass.schedule.windows.map((w, i) => (
                <li key={i}>{describeWindow(w)}</li>
              ))}
            </ul>
          </div>
        )}

        <div className="muted">
          {pass.last_used ? `Último uso: ${fmt(pass.last_used)}` : 'Ainda não usado'}
        </div>
        {pass.status !== 'active' && pass.status_text && <div className="pass-reason">{pass.status_text}</div>}
      </div>

      <div className="actions">
        <button className="primary" onClick={copy} title="Copia o link para enviar à pessoa">
          {copied ? 'Link copiado ✓' : 'Copiar link'}
        </button>
        <a className="button" href={link} target="_blank" rel="noreferrer">
          Abrir
        </a>
        <button onClick={onEdit}>{done ? 'Renovar / editar' : 'Editar'}</button>
        {!done && (
          <button onClick={() => setSending(sending ? null : { phone: '', busy: false })}>Enviar por WhatsApp</button>
        )}
        <button className="link danger" onClick={onRevoke}>
          revogar
        </button>
      </div>

      {sending && (
        <form
          className="pass-send"
          onSubmit={async (e) => {
            e.preventDefault()
            setSending({ ...sending, busy: true })
            const ok = await onSend(sending.phone)
            setSending(ok ? null : { ...sending, busy: false })
          }}
        >
          <input
            autoFocus
            list="pass-contacts"
            inputMode="tel"
            required
            value={sending.phone}
            placeholder="DDD + número, ex.: 67 99999-9999"
            onChange={(e) => setSending({ ...sending, phone: e.target.value })}
          />
          <button className="primary" disabled={sending.busy}>
            {sending.busy ? 'Enviando…' : 'Enviar link'}
          </button>
        </form>
      )}
    </div>
  )
}

/* Panel */

export default function PassesPanel({ botId }: { botId: number }) {
  const [passes, setPasses] = useState<RemotePass[] | null>(null)
  const [error, setError] = useState('')
  const [form, setForm] = useState<{ pass?: RemotePass } | null>(null)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState('')
  const [contacts, setContacts] = useState<Contact[]>([])

  // A notice is only a confirmation: it goes away by itself.
  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(''), 6000)
    return () => window.clearTimeout(timer)
  }, [notice])

  useEffect(() => {
    // The list only helps to fill in the number; typing one always works.
    api<Contact[]>('/api/whatsapp/contacts')
      .then(setContacts)
      .catch(() => {})
  }, [])

  const load = () =>
    api<BotRemote>(`/api/bots/${botId}/remote`)
      .then((r) => setPasses(r.passes ?? []))
      .catch((e) => setError(errorMessage(e)))

  useEffect(() => {
    load()
    // Statuses change with time (hours, expiry) and with use.
    const timer = window.setInterval(() => !document.hidden && load(), 20000)
    return () => window.clearInterval(timer)
  }, [botId])

  const send = async (p: RemotePass, phone: string) => {
    setError('')
    try {
      await api(`/api/bots/${botId}/passes/${p.id}/send`, 'POST', { phone })
      setNotice(`Link de ${p.name} enviado por WhatsApp para ${phone}.`)
      return true
    } catch (e) {
      setError(`Não consegui enviar o link: ${errorMessage(e)}`)
      return false
    }
  }

  const save = async (d: Draft) => {
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const body: PassInput = {
        name: d.name,
        starts_at: fromLocalInput(d.startsAt),
        expires_at: fromLocalInput(d.expiresAt),
        max_uses: d.maxUses,
        timezone: browserZone(),
        schedule: { windows: d.windows },
        reset_uses: d.resetUses,
      }
      if (form?.pass) {
        await api(`/api/bots/${botId}/passes/${form.pass.id}`, 'PUT', body)
        setNotice(`Acesso de ${d.name} atualizado.`)
      } else {
        const created = await api<RemotePass>(`/api/bots/${botId}/passes`, 'POST', body)
        setNotice(`Acesso de ${d.name} criado. Ele aparece na lista abaixo.`)
        if (d.phone.trim()) await send(created, d.phone)
      }
      setForm(null)
      await load()
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setSaving(false)
    }
  }

  const revoke = async (p: RemotePass) => {
    if (!confirm(`Revogar o acesso de ${p.name}? O link deixa de funcionar na hora.`)) return
    try {
      await api(`/api/bots/${botId}/passes/${p.id}`, 'DELETE')
      load()
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  return (
    <div className="card">
      <div className="section-head">
        <div>
          <h3>Acessos temporários</h3>
          <small className="muted">
            Links de controle remoto para quem não está no WhatsApp do bot, com validade, limite de usos e horários.
          </small>
        </div>
        {!form && (
          <button className="primary" onClick={() => setForm({})}>
            + Novo acesso
          </button>
        )}
      </div>

      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}

      {form && (
        <PassForm
          key={form.pass?.id ?? 'new'}
          initial={form.pass ? draftOf(form.pass) : blankDraft()}
          editing={!!form.pass}
          saving={saving}
          onSubmit={save}
          onCancel={() => setForm(null)}
        />
      )}

      {passes?.length === 0 && !form && (
        <p className="muted">
          Nenhum acesso criado. Crie um para dar a alguém (visita, faxineira, entregador) um link que abre e fecha o
          portão só quando você permitir.
        </p>
      )}

      {/* Shared by the form and the cards. */}
      <datalist id="pass-contacts">
        {contacts.map((c) => (
          <option key={c.phone} value={c.phone}>
            {c.name}
          </option>
        ))}
      </datalist>

      <div className="pass-grid">
        {passes?.map((p) => (
          <PassCard
            key={p.id}
            pass={p}
            onEdit={() => {
              setNotice('')
              setForm({ pass: p })
            }}
            onRevoke={() => revoke(p)}
            onSend={(phone) => send(p, phone)}
          />
        ))}
      </div>
    </div>
  )
}
