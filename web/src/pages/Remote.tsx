import { useCallback, useEffect, useRef, useState } from 'react'
import './remote.css'

// The web remote: a personal page, opened from the link the bot sends on
// WhatsApp, with the bot's commands as buttons. The token in the address is
// the credential; there is no login.

type Button = { state: string; option: number; label: string; enabled: boolean }
type RemoteView = {
  owner: string
  bot: string
  state: { id: string; name: string }
  offline: boolean
  buttons: Button[]
  fixes: Button[]
  retry?: Button
  // Set for a temporary access: what is left of it.
  access?: { uses_left: number; expires_at?: string; schedule?: string; note?: string }
}
// A temporary access that does not work now: for good (expired, no uses
// left) or only until its hours come.
type Locked = { text: string; permanent: boolean }
type Toast = { text: string; kind: 'ok' | 'error' }

const POLL_MS = 4000
const keyOf = (b: Button, retry = false) => `${retry ? 'r' : 'b'}:${b.state}:${b.option}`

export default function Remote({ token }: { token: string }) {
  const [view, setView] = useState<RemoteView | null>(null)
  const [gone, setGone] = useState(false)
  const [lost, setLost] = useState(false)
  const [blocked, setBlocked] = useState('')
  const [locked, setLocked] = useState<Locked | null>(null)
  const [busy, setBusy] = useState('')
  const [toast, setToast] = useState<Toast | null>(null)
  const toastTimer = useRef(0)
  const acting = useRef(false)
  const permanent = useRef(false)
  const url = `/api/remote/${encodeURIComponent(token)}`

  const notify = (t: Toast) => {
    setToast(t)
    window.clearTimeout(toastTimer.current)
    toastTimer.current = window.setTimeout(() => setToast(null), 3500)
  }

  const lock = async (res: Response) => {
    const data = await res.json().catch(() => ({}))
    setView(null)
    setLocked({ text: data.error ?? 'Este acesso não está disponível agora.', permanent: res.status === 410 })
  }

  const load = useCallback(async () => {
    // A press in flight brings the fresh state itself.
    if (acting.current) return
    try {
      const res = await fetch(url, { cache: 'no-store' })
      if (res.status === 404) return setGone(true)
      if (res.status === 410 || res.status === 423) return lock(res)
      if (res.status === 503) {
        // Whether the person is still in the group could not be checked.
        const data = await res.json().catch(() => ({}))
        setView(null)
        return setBlocked(data.error ?? 'Não foi possível confirmar o acesso.')
      }
      if (!res.ok) throw new Error()
      const data = (await res.json()) as RemoteView
      if (!acting.current) setView(data)
      setLocked(null)
      setLost(false)
      setBlocked('')
    } catch {
      setLost(true)
    }
  }, [url])

  useEffect(() => {
    document.documentElement.classList.add('remote-page')
    load()
    const timer = window.setInterval(() => !document.hidden && !permanent.current && load(), POLL_MS)
    const onVisible = () => !document.hidden && load()
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      document.documentElement.classList.remove('remote-page')
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [load])

  useEffect(() => {
    permanent.current = !!locked?.permanent
  }, [locked])

  useEffect(() => {
    if (view) document.title = `${view.bot} · controle remoto`
  }, [view?.bot])

  const act = async (b: Button, retry = false) => {
    if (acting.current) return
    acting.current = true
    setBusy(keyOf(b, retry))
    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ state: b.state, option: b.option, retry }),
      })
      const data = await res.json().catch(() => ({}))
      if (res.status === 404) return setGone(true)
      if (res.status === 410 || res.status === 423) {
        setLocked({ text: data.error ?? 'Este acesso não está disponível agora.', permanent: res.status === 410 })
        return setView(null)
      }
      if (!res.ok) {
        if (data.remote) setView(data.remote)
        return notify({ text: data.error ?? 'Não foi possível enviar o comando.', kind: 'error' })
      }
      setView(data)
      setLocked(null)
      setLost(false)
      navigator.vibrate?.(35)
      notify({ text: retry ? 'Comando enviado de novo' : `${b.label}: feito`, kind: 'ok' })
    } catch {
      notify({ text: 'Sem conexão com o servidor. Tente de novo.', kind: 'error' })
    } finally {
      acting.current = false
      setBusy('')
    }
  }

  if (gone) {
    return (
      <div className="remote">
        <div className="remote-message">
          <div className="remote-orb off">
            <span>Link inválido</span>
          </div>
          <p>
            Este link não vale mais: foi revogado ou você não está mais no grupo. Peça um novo pelo menu do bot no
            WhatsApp.
          </p>
        </div>
      </div>
    )
  }
  if (locked) {
    return (
      <div className="remote">
        <div className="remote-message">
          <div className="remote-orb off">
            <span>{locked.permanent ? 'Acesso encerrado' : 'Fora do horário'}</span>
          </div>
          <p>{locked.text}</p>
          {!locked.permanent && <p className="remote-note">Esta página abre sozinha quando o acesso for liberado.</p>}
        </div>
      </div>
    )
  }
  if (!view) {
    return (
      <div className="remote">
        <div className="remote-message">
          <div className="remote-orb off">
            <span>{blocked ? 'Aguardando' : lost ? 'Sem conexão' : 'Carregando…'}</span>
          </div>
          {blocked && <p>{blocked}</p>}
          {lost && !blocked && <p>Não consegui falar com o servidor. Tentando de novo…</p>}
        </div>
      </div>
    )
  }

  return (
    <div className="remote">
      <header className="remote-head">
        <small>Controle remoto</small>
        <h1>{view.bot}</h1>
      </header>

      <section className="remote-status" aria-live="polite">
        <div className="remote-orb" key={view.state.id}>
          <small>Estado atual</small>
          <span>{view.state.name}</span>
        </div>
        {view.offline && <p className="remote-warn">Dispositivo sem resposta: o comando pode não chegar.</p>}
        {lost && !view.offline && <p className="remote-warn">Sem conexão com o servidor. Tentando de novo…</p>}
      </section>

      <section className="remote-pad">
        {view.buttons.map((b) => (
          <button
            key={keyOf(b)}
            className="remote-key"
            disabled={!b.enabled || busy !== ''}
            data-off={!b.enabled || undefined}
            data-busy={busy === keyOf(b) || undefined}
            onClick={() => act(b)}
          >
            <span>{b.label}</span>
          </button>
        ))}
        {view.buttons.length === 0 && <p className="remote-note">Este bot não tem comandos para enviar.</p>}

        {view.retry && (
          <button
            className="remote-link"
            disabled={busy !== ''}
            data-busy={busy === keyOf(view.retry, true) || undefined}
            onClick={() => act(view.retry!, true)}
          >
            ↻ Não funcionou? Enviar “{view.retry.label}” de novo
          </button>
        )}
      </section>

      {view.access && <AccessInfo access={view.access} />}

      {view.fixes.length > 0 && (
        <details className="remote-fixes">
          <summary>O estado mostrado está errado?</summary>
          {view.fixes.map((b) => (
            <button key={keyOf(b)} className="remote-link" disabled={busy !== ''} onClick={() => act(b)}>
              {b.label}
            </button>
          ))}
        </details>
      )}

      <footer className="remote-foot">
        {view.access ? (
          <>
            Acesso temporário de <b>{view.owner}</b>. Cada uso fica registrado.
          </>
        ) : (
          <>
            Controle pessoal de <b>{view.owner}</b>. Cada uso fica registrado no seu nome e é avisado no WhatsApp.
          </>
        )}
      </footer>

      {toast && (
        <div className={`remote-toast ${toast.kind}`} role="status">
          {toast.text}
        </div>
      )}
    </div>
  )
}

function AccessInfo({ access }: { access: NonNullable<RemoteView['access']> }) {
  const when = (iso: string) => new Date(iso).toLocaleString('pt-BR', { dateStyle: 'short', timeStyle: 'short' })
  return (
    <section className="remote-access">
      {access.uses_left >= 0 && (
        <div>
          <b>{access.uses_left}</b> {access.uses_left === 1 ? 'uso restante' : 'usos restantes'}
        </div>
      )}
      {access.expires_at && <div>Vale até {when(access.expires_at)}</div>}
      {access.schedule && <div>Horários: {access.schedule}</div>}
      {access.note && <div className="remote-warn">{access.note}</div>}
    </section>
  )
}
