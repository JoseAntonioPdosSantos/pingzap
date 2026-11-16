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
}
type Toast = { text: string; kind: 'ok' | 'error' }

const POLL_MS = 4000
const keyOf = (b: Button, retry = false) => `${retry ? 'r' : 'b'}:${b.state}:${b.option}`

export default function Remote({ token }: { token: string }) {
  const [view, setView] = useState<RemoteView | null>(null)
  const [gone, setGone] = useState(false)
  const [lost, setLost] = useState(false)
  const [blocked, setBlocked] = useState('')
  const [busy, setBusy] = useState('')
  const [toast, setToast] = useState<Toast | null>(null)
  const toastTimer = useRef(0)
  const acting = useRef(false)
  const url = `/api/remote/${encodeURIComponent(token)}`

  const notify = (t: Toast) => {
    setToast(t)
    window.clearTimeout(toastTimer.current)
    toastTimer.current = window.setTimeout(() => setToast(null), 3500)
  }

  const load = useCallback(async () => {
    // A press in flight brings the fresh state itself.
    if (acting.current) return
    try {
      const res = await fetch(url, { cache: 'no-store' })
      if (res.status === 404) return setGone(true)
      if (res.status === 503) {
        // Whether the person is still in the group could not be checked.
        const data = await res.json().catch(() => ({}))
        setView(null)
        return setBlocked(data.error ?? 'Não foi possível confirmar o acesso.')
      }
      if (!res.ok) throw new Error()
      const data = (await res.json()) as RemoteView
      if (!acting.current) setView(data)
      setLost(false)
      setBlocked('')
    } catch {
      setLost(true)
    }
  }, [url])

  useEffect(() => {
    document.documentElement.classList.add('remote-page')
    load()
    const timer = window.setInterval(() => !document.hidden && load(), POLL_MS)
    const onVisible = () => !document.hidden && load()
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      document.documentElement.classList.remove('remote-page')
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [load])

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
      if (!res.ok) {
        if (data.remote) setView(data.remote)
        return notify({ text: data.error ?? 'Não foi possível enviar o comando.', kind: 'error' })
      }
      setView(data)
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
        Controle pessoal de <b>{view.owner}</b>. Cada uso fica registrado no seu nome e é avisado no WhatsApp.
      </footer>

      {toast && (
        <div className={`remote-toast ${toast.kind}`} role="status">
          {toast.text}
        </div>
      )}
    </div>
  )
}
