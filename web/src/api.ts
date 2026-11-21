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
  shortcut?: string
  command: string
  next_state: string
  reply: string
  show_in_confirm?: boolean
}
export type FlowState = { id: string; name: string; menu: string; options: FlowOption[] }
export type Flow = {
  start_command: string
  menu_command: string
  // Empty or absent turns the web remote off.
  remote_command?: string
  // Text of the web remote option; empty = default.
  remote_label?: string
  // Line above the numbered options; empty = "Digite o número da opção:".
  prompt?: string
  // Optional text at the end of every menu.
  footer?: string
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

export type RemoteLink = {
  id: number
  phone: string
  name: string
  created_at: string
  last_used?: string
  uses: number
}
export type RemoteUse = { phone: string; name: string; action: string; command: string; state: string; at: string }
export type PassWindow = { days: number[]; start: string; end: string }
export type PassStatus = 'active' | 'scheduled' | 'outside' | 'expired' | 'used_up'
// A temporary access, made in the interface for someone outside the chats.
export type RemotePass = {
  id: number
  name: string
  // Full link when the public address is set; otherwise only the path.
  url: string
  path: string
  created_at: string
  starts_at?: string
  expires_at?: string
  last_used?: string
  max_uses: number // 0 = no limit
  uses: number
  uses_left: number // -1 = no limit
  timezone: string
  schedule: { windows: PassWindow[] }
  status: PassStatus
  status_text: string
}
export type PassInput = {
  name: string
  starts_at: string | null
  expires_at: string | null
  max_uses: number
  timezone: string
  schedule: { windows: PassWindow[] }
  reset_uses?: boolean
}
export type BotRemote = { links: RemoteLink[]; uses: RemoteUse[]; passes: RemotePass[] }

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
