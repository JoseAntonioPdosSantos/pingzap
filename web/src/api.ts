// Types and calls of the pingzap API.

export type User = { id: number; username: string; created_at: string }

export type Device = {
  id: number
  name: string
  client_id: string
  token: string
  cmd_topic: string
  data_topic: string
  status_topic: string
  status: string
  last_seen?: string
  last_data: string
}

export type TargetKind = 'contact' | 'group'
export type Target = { kind: TargetKind; value: string; label: string }

export type FlowOption = {
  label: string
  command: string
  confirm: string
  next_state: string
  reply: string
}
export type FlowState = { id: string; name: string; menu: string; options: FlowOption[] }
export type Flow = {
  start_command: string
  menu_command: string
  reminder_minutes: number
  max_attempts: number
  alarm_after_reminders: number
  alarm_command: string
  alarm_clear_command: string
  states: FlowState[]
}
export type AlertConfig = { keyword: string; on_payload: string; off_payload: string }

export type BotKind = 'flow' | 'alert'
export type Bot = {
  id: number
  name: string
  kind: BotKind
  enabled: boolean
  device_id: number | null
  config: Flow | AlertConfig
  current_state: string
  targets: Target[]
}

export type WhatsAppState = 'disconnected' | 'connecting' | 'pairing' | 'connected'
export type WhatsAppStatus = { state: WhatsAppState; phone: string; qr_image?: string }
export type Contact = { phone: string; name: string }
export type Group = { jid: string; name: string }

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
  }
}

// Called when the session expires, so the app can show the login screen.
let onUnauthorized = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T = void>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    // The server only accepts state changes sent as JSON.
    headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/api/auth/')) onUnauthorized()
    throw new ApiError(data.error ?? `erro ${res.status}`, res.status)
  }
  return data as T
}

export const errorMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))
