import { useEffect, useState } from 'react'
import { api, errorMessage, type WhatsAppStatus } from '../api'

const labels: Record<WhatsAppStatus['state'], string> = {
  disconnected: 'Desconectado',
  connecting: 'Conectando…',
  pairing: 'Aguardando leitura do QR code',
  connected: 'Conectado',
}

export default function WhatsApp() {
  const [status, setStatus] = useState<WhatsAppStatus | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = () => api<WhatsAppStatus>('/api/whatsapp').then(setStatus).catch((e) => setError(errorMessage(e)))
    load()
    // The QR code rotates every few seconds while pairing.
    const timer = setInterval(load, 2000)
    return () => clearInterval(timer)
  }, [])

  const run = async (path: string) => {
    setError('')
    try {
      await api(path, 'POST')
      setStatus(await api<WhatsAppStatus>('/api/whatsapp'))
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  const logout = () => {
    if (confirm('Desconectar esta conta do WhatsApp? Os bots param de responder até um novo pareamento.')) {
      run('/api/whatsapp/logout')
    }
  }

  if (!status) return <p className="muted">Carregando…</p>

  return (
    <section>
      <h2>WhatsApp</h2>
      <div className="card">
        <p>
          <span className={`badge ${status.state}`}>{labels[status.state]}</span>
          {status.phone && <span className="muted"> Conta: +{status.phone}</span>}
        </p>

        {status.state === 'disconnected' && (
          <>
            <p className="muted">
              O pingzap entra no WhatsApp como um aparelho conectado, como o WhatsApp Web. Os bots respondem a partir
              dessa conta.
            </p>
            <button className="primary" onClick={() => run('/api/whatsapp/pair')}>
              Conectar
            </button>
          </>
        )}

        {status.state === 'pairing' && (
          <div className="qr">
            {status.qr_image ? <img src={status.qr_image} alt="QR code de pareamento" /> : <p>Gerando QR code…</p>}
            <ol>
              <li>Abra o WhatsApp no celular.</li>
              <li>
                Toque em <b>Aparelhos conectados</b> e depois em <b>Conectar um aparelho</b>.
              </li>
              <li>Aponte a câmera para este código.</li>
            </ol>
          </div>
        )}

        {status.state === 'connected' && (
          <button className="danger" onClick={logout}>
            Desconectar conta
          </button>
        )}
        {error && <p className="error">{error}</p>}
      </div>
    </section>
  )
}
