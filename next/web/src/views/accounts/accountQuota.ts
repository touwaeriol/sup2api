// Subscription quota of accounts (5h / 7d / 7d Sonnet / 7d Fable windows):
// API calls, display helpers and manual refresh. The list
// endpoint already carries every row's snapshot, so the page never polls this
// per row — only the "refresh" action asks the upstream (force=true).
import { reactive, type Ref } from 'vue'
import { api } from '@sub2api/host'
import { useNow } from '@sub2api/ui'
import type { Account, AccountBalance, CredentialRefreshOutcome, QuotaSnapshot, QuotaWindow } from '@/api/types'
import { todayQuery } from './accountUsage'
import { usageLevel, type UsageLevel } from './usageLevel'

/** GET /accounts/:id/quota — `force` makes the server query the upstream now. */
export function fetchAccountQuota(id: number, force = true): Promise<QuotaSnapshot> {
  return api.get<QuotaSnapshot>(`/accounts/${id}/quota`, force ? { force: true } : undefined)
}

/** POST /accounts/:id/reset-status — restores active status and clears the reason and cooldown; usage "today" is the browser's. */
export function resetAccountStatus(id: number): Promise<Account> {
  return api.post<Account>(`/accounts/${id}/reset-status`, undefined, { query: todayQuery() })
}

/** Whether a snapshot is worth a cell: null (API keys) and unsupported types show nothing. */
export function hasQuota(q: QuotaSnapshot | null | undefined): q is QuotaSnapshot {
  return !!q && q.supported
}

export function sortWindows(windows: QuotaWindow[] | null | undefined): QuotaWindow[] {
  return [...(windows || [])]
}

export type QuotaLevel = UsageLevel

/**
 * Colour level of a window (thresholds as sub2api's usage bars): rejected or
 * ≥ 90 % is danger, an upstream warning or ≥ 75 % is warning.
 */
export function quotaLevel(w: Pick<QuotaWindow, 'utilization' | 'status'>): QuotaLevel {
  if (w.status === 'rejected') return 'danger'
  const level = usageLevel(w.utilization / 100, 0.75, 0.9)
  return level === 'ok' && w.status === 'allowed_warning' ? 'warning' : level
}

/** "42%", rounded; ">999%" beyond. */
export function formatPercent(utilization: number): string {
  if (!Number.isFinite(utilization)) return '—'
  const p = Math.round(Math.max(0, utilization))
  return p > 999 ? '>999%' : `${p}%`
}

export type Countdown = { kind: 'none' } | { kind: 'pending' } | { kind: 'in'; text: string }

export function quotaCountdown(resetsAt: string | null | undefined, nowMs: number, expired = false): Countdown {
  if (expired) return { kind: 'pending' }
  if (!resetsAt) return { kind: 'none' }
  const at = Date.parse(resetsAt)
  if (Number.isNaN(at)) return { kind: 'none' }
  const diff = at - nowMs
  if (diff <= 0) return { kind: 'pending' }
  const mins = Math.floor(diff / 60_000)
  const hours = Math.floor(mins / 60)
  if (hours >= 24) return { kind: 'in', text: `${Math.floor(hours / 24)}d ${hours % 24}h` }
  if (hours > 0) return { kind: 'in', text: `${hours}h ${mins % 60}m` }
  return { kind: 'in', text: mins > 0 ? `${mins}m` : '<1m' }
}

// ---------------------------------------------------------------- shared clock

/**
 * One 30 s ticker shared by every quota cell on the page (instead of a timer
 * per row), so countdowns move even with the list's auto refresh off.
 */
export function useQuotaClock(): Readonly<Ref<number>> {
  return useNow(30_000)
}

// ---------------------------------------------------------------- manual refresh

export type RefreshOutcome = 'ok' | 'busy' | 'throttled' | 'error'

export interface RefreshOptions<T> {
  /** A fresh snapshot for the account. */
  onSnapshot: (id: number, snapshot: T) => void
  onError?: (id: number, e: unknown) => void
  /** Injected in tests. */
  fetcher?: (id: number) => Promise<T>
}

export type QuotaRefreshOptions = RefreshOptions<QuotaSnapshot>

/**
 * Manual refresh of one account's snapshot without a cooldown after
 * completion. While a request runs the account is in `refreshing` and
 * duplicate clicks are ignored.
 */
function useRefresh<T>(fetcher: (id: number) => Promise<T>, opts: RefreshOptions<T>) {
  const refreshing = reactive(new Set<number>())
  async function refresh(id: number): Promise<RefreshOutcome> {
    if (refreshing.has(id)) return 'busy'
    refreshing.add(id)
    try {
      opts.onSnapshot(id, await fetcher(id))
      return 'ok'
    } catch (e) {
      opts.onError?.(id, e)
      return 'error'
    } finally {
      refreshing.delete(id)
    }
  }

  return { refreshing, refresh }
}

/** Manual "refresh usage" (GET /accounts/:id/quota?force=true). */
export function useQuotaRefresh(opts: QuotaRefreshOptions) {
  return useRefresh(opts.fetcher ?? ((id: number) => fetchAccountQuota(id, true)), opts)
}

/** POST /accounts/:id/refresh-credentials — renews the credentials now (CONTRACTS §48). */
export function refreshAccountCredentials(id: number): Promise<CredentialRefreshOutcome> {
  return api.post<CredentialRefreshOutcome>(`/accounts/${id}/refresh-credentials`)
}

// ---------------------------------------------------------------- balance (CONTRACTS §52)

/** Whether the account type has a balance (the view carries one). */
export function hasBalance(b: AccountBalance | null | undefined): b is AccountBalance {
  return !!b
}

/** GET /accounts/:id/balance?force=true — reads the balance now. */
export function refreshAccountBalance(id: number): Promise<AccountBalance> {
  return api.get<AccountBalance>(`/accounts/${id}/balance`, { force: true })
}

export type BalanceRefreshOptions = RefreshOptions<AccountBalance>

/** Manual "refresh balance", like the quota's: no cooldown, in-flight clicks coalesced. */
export function useBalanceRefresh(opts: BalanceRefreshOptions) {
  return useRefresh(opts.fetcher ?? refreshAccountBalance, opts)
}
