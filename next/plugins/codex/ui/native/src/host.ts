import { isApiError, type PluginHost } from '@sub2api/host'
import type { FormAgent } from './flow'

// Plugin-scoped host handed to register(); shared by the codex components.
// All plugin route shapes (docs/PLUGIN-CODEX.md §4) live in this file.
//
// The routes answer CONTRACTS §3.1: a success is {"data": ...}, which
// pluginApi unwraps (the shapes below are the data member); a failure is
// {"error": {code, message, details}}, thrown as an ApiError. A malformed
// request is 400 invalid_argument with details.fields; a refusal of the flow
// keeps its own code (device_auth_disabled, state_mismatch, ...). Every
// message is "English / 中文", so the form shows it as given.
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useCodexHost(): PluginHost {
  if (!host) throw new Error('codex UI used before register(host)')
  return host
}

const api = () => useCodexHost().pluginApi

// ------------------------------------------------------------------ credentials

/** The credential object both flows return (the account's credentials; Go: codex.Credentials). */
export interface CodexCredentials {
  access_token: string
  /** Left out when the issuer sent none. */
  refresh_token?: string
  id_token?: string
  /** Unix seconds. */
  expires_at?: number
  email?: string
  /** ChatGPT plan, read from the tokens. */
  plan_type?: string
  chatgpt_account_id?: string
}

// ------------------------------------------------------------------ device code (codex_device)

export interface DeviceStart {
  session_id: string
  verification_url: string
  user_code: string
  /** Seconds between polls. */
  interval: number
  /** Seconds the code stays valid. */
  expires_in: number
}

export type PollStatus = 'pending' | 'done' | 'expired' | 'error'

export interface DevicePoll {
  status: PollStatus
  credentials?: CodexCredentials
  message?: string
  /** The poll interval (seconds) on pending answers; it grows when OpenAI asks to slow down. */
  interval?: number
}

/**
 * Every call of both logins carries a fresh proxy ticket of the account
 * form's current proxy choice (CONTRACTS §117) and the form's current client
 * headers: the requests of that call leave through the proxy the form names
 * at that moment and send its User-Agent and originator ("" for the plugin
 * settings' or built-in ones).
 */
export const startDevice = (proxyTicket: string, agent: FormAgent) => api().post<DeviceStart>('/auth/device/start', { proxy_ticket: proxyTicket, ...agent })
export const pollDevice = (sessionId: string, proxyTicket: string, agent: FormAgent) =>
  api().post<DevicePoll>('/auth/device/poll', { session_id: sessionId, proxy_ticket: proxyTicket, ...agent })

// ------------------------------------------------------------------ Sign in with Codex (codex_oauth)

export interface OAuthStart {
  session_id: string
  authorize_url: string
}

export const startOAuth = (proxyTicket: string, agent: FormAgent) => api().post<OAuthStart>('/auth/codex/start', { proxy_ticket: proxyTicket, ...agent })
export const exchangeOAuth = (sessionId: string, callbackUrl: string, proxyTicket: string, agent: FormAgent) =>
  api().post<{ credentials: CodexCredentials }>('/auth/codex/exchange', { session_id: sessionId, callback_url: callbackUrl, proxy_ticket: proxyTicket, ...agent })

// ------------------------------------------------------------------ official usage resets

/** One reset credit of the account (POST /reset-credits/list); times are Unix seconds. */
export interface ResetCredit {
  id: string
  /** OpenAI's own wording; empty when it sent none. */
  title: string
  description: string
  /** "available", "redeeming", "redeemed" or a newer state. */
  status: string
  granted_at: number
  /** null: does not expire. */
  expires_at: number | null
}

export interface ResetCredits {
  available_count: number
  /** Sorted by expiry, the soonest first; a credit that does not expire last. */
  credits: ResetCredit[]
}

/** A used reset (POST /reset-credits/consume); a refusal (nothing_to_reset, no_credit, ...) is an ApiError. */
export interface ResetOutcome {
  outcome: 'reset' | 'already_redeemed'
  windows_reset: number
  /** false: the reset was used but the account's automatic disable could not be lifted. */
  account_restored: boolean
}

/** Cached counts (POST /reset-credits/summary): written by /reset-credits/list, never read from OpenAI on the call; accounts without one are left out. */
export interface ResetSummary {
  accounts: Record<string, { available_count: number; updated_at: number }>
}

export const listResetCredits = (accountId: number) => api().post<ResetCredits>('/reset-credits/list', { account_id: accountId })
export const summarizeResetCredits = (accountIds: number[]) => api().post<ResetSummary>('/reset-credits/summary', { account_ids: accountIds })
export const redeemResetCredit = (accountId: number, creditId?: string) =>
  api().post<ResetOutcome>('/reset-credits/consume', { account_ids: [accountId], ...(creditId ? { credit_id: creditId } : {}) })

// ------------------------------------------------------------------ helpers

/**
 * Human-readable message of a failed call, else `fallback`. An
 * invalid_argument error says which field is wrong in details.fields: its
 * messages are shown rather than the generic "invalid request".
 */
export function errorMessage(e: unknown, fallback: string): string {
  if (isApiError(e)) {
    const fields = Object.values(e.fields).filter(Boolean)
    if (fields.length) return fields.join('; ')
  }
  return e instanceof Error && e.message ? e.message : fallback
}
