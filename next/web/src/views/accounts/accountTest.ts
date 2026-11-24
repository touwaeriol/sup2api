import type { Account, AccountLastTest, AccountTestResult, AccountType } from '@/api/types'

// Account / model tests (POST /accounts/:id/test): candidate models, result
// rows of the model test dialog, latency colouring. A test is a diagnosis
// only: it never cools down or disables the account (the effect is shown).

/** Concurrent tests of the console (per dialog and for the batch test of the list). */
export const TEST_CONCURRENCY = 3

export type CandidateSource = 'account' | 'mapping' | 'default' | 'upstream' | 'custom'
export interface Candidate {
  model: string
  source: CandidateSource
}

/**
 * Models offered for testing: the account's model list; when the account
 * serves every model (empty list), the request models of its mapping and the
 * plugin's default models.
 */
export function candidateModels(account: Pick<Account, 'models' | 'model_mapping'>, at?: Pick<AccountType, 'default_models'> | null): Candidate[] {
  const out: Candidate[] = []
  const seen = new Set<string>()
  const add = (model: string, source: CandidateSource) => {
    if (!model || seen.has(model)) return
    seen.add(model)
    out.push({ model, source })
  }
  if (account.models?.length) {
    for (const m of account.models) add(m, 'account')
    return out
  }
  for (const m of Object.keys(account.model_mapping || {})) add(m, 'mapping')
  for (const m of at?.default_models || []) add(m, 'default')
  return out
}

/** Appends models not listed yet; returns the new list and how many were added. */
export function mergeCandidates(list: Candidate[], models: string[], source: CandidateSource): { list: Candidate[]; added: number } {
  const seen = new Set(list.map((c) => c.model))
  const next = [...list]
  for (const m of models) {
    if (!m || seen.has(m)) continue
    seen.add(m)
    next.push({ model: m, source })
  }
  return { list: next, added: next.length - list.length }
}

export type LatencyLevel = 'fast' | 'ok' | 'slow' | 'bad'
/** Colour band of a latency (as new-api: ≤1 s, ≤3 s, ≤5 s, slower). */
export function latencyLevel(ms: number): LatencyLevel {
  if (ms <= 1000) return 'fast'
  if (ms <= 3000) return 'ok'
  if (ms <= 5000) return 'slow'
  return 'bad'
}
export const LATENCY_CLASS: Record<LatencyLevel, string> = {
  fast: 'text-success-600 dark:text-success-400',
  ok: 'text-lime-600 dark:text-lime-400',
  slow: 'text-warning-600 dark:text-warning-400',
  bad: 'text-danger-600 dark:text-danger-400'
}

/** "480 ms" / "1.24 s". */
export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms)) return '—'
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(2)} s`
}

/** `canceled`: the user stopped the test (a test has no time limit of its own). */
export type RowState = 'idle' | 'testing' | 'ok' | 'fail' | 'canceled'
export interface TestRow extends Candidate {
  state: RowState
  result?: AccountTestResult
  /** Transport / API error of the console request itself (no result). */
  error?: string
  /** Stops the test in flight (set while testing). */
  stop?: () => void
}

export function summarize(rows: Pick<TestRow, 'state'>[]): Record<RowState | 'total', number> {
  const s = { total: rows.length, ok: 0, fail: 0, testing: 0, idle: 0, canceled: 0 }
  for (const r of rows) s[r.state]++
  return s
}

/** The account's model list without `failed`; null when nothing would be removed. */
export function withoutModels(models: string[], failed: string[]): string[] | null {
  const drop = new Set(failed)
  const next = models.filter((m) => !drop.has(m))
  return next.length === models.length ? null : next
}

/** Whether a result carries warnings (CONTRACTS §122.5). */
export const hasWarnings = (r?: AccountTestResult): boolean => !!r?.warnings?.length

/**
 * The last_test the server records for a result (used to update the list at
 * once): a success keeps its warnings as the message.
 */
export function lastTestOf(r: AccountTestResult, requested: string, at = new Date()): AccountLastTest {
  const message = r.ok ? (r.warnings || []).map((w) => w.message).join('\n') : r.message || (r.error_source === 'response' ? '' : r.reason) || ''
  return { at: at.toISOString(), ok: r.ok, status: r.status, error_code: r.error_code, error_source: r.error_source, latency_ms: r.latency_ms, model: r.model || r.requested_model || requested, protocol: r.protocol, message }
}

/**
 * The upstream response snippet, re-indented when it parses as JSON. A snippet
 * is truncated at ~4 KiB, so parsing often fails: then it is shown verbatim.
 */
export function prettyBody(raw: string | undefined): string {
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}
