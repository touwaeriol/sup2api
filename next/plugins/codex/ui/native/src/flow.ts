// Pure helpers of the codex account form (no host, no DOM): which flows an
// account type shows, how fresh credentials replace the old ones, and the
// checks on what the operator pastes. Covered by flow.spec.ts.
import type { CodexCredentials } from './host'

export type Flow = 'device' | 'oauth'

/** The flow of each account type (manifest accountTypes[].id). */
const FLOW_OF: Record<string, Flow> = { codex_device: 'device', codex_oauth: 'oauth' }

/** Flows of an account type; both when the type is not known. */
export function flowsFor(type: string | null | undefined): Flow[] {
  const f = type ? FLOW_OF[type] : undefined
  return f ? [f] : ['device', 'oauth']
}

/** The account type the form is for: the editor's accountType prop, else the account's own. */
export function typeOf(accountType: { type?: string } | null | undefined, account: { type?: string } | null | undefined): string {
  return accountType?.type || account?.type || ''
}

/**
 * Credential keys the flows write. A fresh authorization replaces all of
 * them, so a key the new login does not return (an id_token the issuer left
 * out) does not survive as a stale or masked value; any other key is kept.
 */
export const CODEX_FIELDS = ['access_token', 'refresh_token', 'id_token', 'expires_at', 'email', 'chatgpt_account_id', 'plan_type'] as const

/**
 * Credential keys of the account's own client headers (User-Agent,
 * originator); "" or absent means the plugin settings' value, else the
 * built-in one. A re-authorization keeps them.
 */
export const AGENT_FIELDS = ['user_agent', 'originator'] as const
export type AgentField = (typeof AGENT_FIELDS)[number]
/** The account's own client headers, "" for none. */
export type FormAgent = Record<AgentField, string>

/** The client headers of creds ("" where it has none or not a string). */
export function agentOf(creds: Record<string, unknown> | null | undefined): FormAgent {
  const v = (k: AgentField) => (typeof creds?.[k] === 'string' ? (creds[k] as string) : '')
  return { user_agent: v('user_agent'), originator: v('originator') }
}

/** base with the client headers of from (where from has them). */
export function withAgent(base: Record<string, unknown>, from: Record<string, unknown> | null | undefined): Record<string, unknown> {
  const out: Record<string, unknown> = { ...base }
  for (const k of AGENT_FIELDS) if (from && k in from) out[k] = from[k]
  return out
}

export function mergeCredentials(current: Record<string, unknown> | null | undefined, fresh: CodexCredentials): Record<string, unknown> {
  const out: Record<string, unknown> = { ...(current || {}) }
  for (const k of CODEX_FIELDS) delete out[k]
  for (const [k, v] of Object.entries(fresh)) if (v !== undefined && v !== null) out[k] = v
  return out
}

/** Whether the credentials hold an access token (the core's mask "******" counts: it keeps the stored one). */
export function hasAccessToken(creds: Record<string, unknown> | null | undefined): boolean {
  const v = creds?.access_token
  return typeof v === 'string' && v.trim() !== ''
}

export interface CredentialSummary {
  email: string
  plan: string
  /** ms since the epoch, or null when unknown. */
  expiresAt: number | null
  accountId: string
}

const str = (v: unknown) => (typeof v === 'string' ? v.trim() : '')

export function summarize(creds: Record<string, unknown> | null | undefined): CredentialSummary {
  const c = creds || {}
  const exp = Number(c.expires_at)
  return {
    email: str(c.email),
    plan: str(c.plan_type),
    expiresAt: Number.isFinite(exp) && exp > 0 ? exp * 1000 : null,
    accountId: str(c.chatgpt_account_id)
  }
}

/** Seconds as m:ss (or h:mm:ss); negative counts as 0. */
export function formatCountdown(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

/**
 * Shape of the callback address pasted back after Sign in with Codex (only
 * a hint: the plugin parses and checks it). It reads what the plugin's
 * parseCallback reads: the whole address, the address without its scheme, or
 * only the query ("?code=..." or "code=..."), with parameters in the fragment
 * when the query has none; surrounding quotes or angle brackets and
 * HTML-escaped ampersands are tolerated.
 *   empty    nothing pasted yet
 *   invalid  the plugin cannot read it (a malformed %-escape or a ";")
 *   denied   it carries error= (consent refused or failed)
 *   no_code  no code= parameter
 *   ok       looks like the callback
 */
export type CallbackShape = 'empty' | 'invalid' | 'denied' | 'no_code' | 'ok'

/** Characters parseCallback trims around the pasted text. */
const CALLBACK_TRIM = /^["'<>`\s]+|["'<>`\s]+$/g

/** The query parameters of a pasted callback, or null when the plugin could not read them (Go url.ParseQuery). */
function callbackParams(query: string): URLSearchParams | null {
  if (query.includes(';') || /%(?![0-9a-fA-F]{2})/.test(query)) return null
  return new URLSearchParams(query)
}

export function callbackShape(raw: string): CallbackShape {
  let text = raw.trim().replace(CALLBACK_TRIM, '').replace(/&amp;/g, '&')
  if (!text) return 'empty'
  let fragment = ''
  const hash = text.indexOf('#')
  if (hash >= 0) {
    fragment = text.slice(hash + 1)
    text = text.slice(0, hash)
  }
  const q = text.indexOf('?')
  let query: string
  if (q >= 0) query = text.slice(q + 1)
  else if (text.includes('://') || (!text.includes('=') && text.includes('/'))) query = '' // an address without a query
  else query = text
  let params = callbackParams(query)
  if (!params) return 'invalid'
  if (!params.get('code') && !params.get('error') && fragment) params = callbackParams(fragment) ?? params
  if (params.get('error')?.trim()) return 'denied'
  return params.get('code')?.trim() ? 'ok' : 'no_code'
}

/** ChatGPT's security settings, where the account owner turns on device code sign-in (Security and login → App security) before a device code works. */
export const CHATGPT_SECURITY_URL = 'https://chatgpt.com/settings/security'

/** Device code polling: the delay when the plugin gives no usable interval, and the shortest one. */
export const DEFAULT_POLL_MS = 5000
export const MIN_POLL_SECONDS = 2
/** Lifetime of a device code when the start answer has none (Codex: 15 minutes). */
export const DEFAULT_CODE_TTL_SECONDS = 900
/** Failed poll requests in a row (a dropped request, a restarting node) before the flow gives up. */
export const MAX_POLL_FAILURES = 3
/** How often the countdown ticks. */
export const COUNTDOWN_TICK_MS = 1000

/** Poll delay in ms for an interval in seconds: at least MIN_POLL_SECONDS, DEFAULT_POLL_MS when unusable. */
export function pollDelay(interval: unknown): number {
  const n = Number(interval)
  return Number.isFinite(n) && n > 0 ? Math.max(MIN_POLL_SECONDS, n) * 1000 : DEFAULT_POLL_MS
}

/** Plans with a message (plan.<id>); others are shown as given. */
export const KNOWN_PLANS = ['free', 'go', 'plus', 'pro', 'team', 'business', 'enterprise', 'edu'] as const
