import type { UsageLog } from '@/api/types'
import { isConvertedGrade } from '@/utils/conversion'
import { formatNumber } from '@/utils/format'

/** Usage row/detail with the extra fields the billing module returns. */
export type UsageRow = UsageLog & {
  api_key_name?: string
  user_name?: string
  price?: { id: number; model: string; source: string } | null
  metrics?: Record<string, unknown> | null
}

export type UsageTokenFacts = Partial<Pick<UsageRow, 'input_tokens' | 'output_tokens' | 'cache_read_tokens' | 'cache_creation_tokens' | 'cache_creation_1h_tokens' | 'image_input_tokens' | 'image_output_tokens'>>

const replacementTokenFields = {
  input_tokens: 'Input', output_tokens: 'Output', cache_read_tokens: 'CacheRead',
  cache_creation_tokens: 'CacheCreation', cache_creation_1h_tokens: 'CacheCreation1h'
} as const

const pricedTokenFields = { ...replacementTokenFields, image_input_tokens: 'ImageInput', image_output_tokens: 'ImageOutput' } as const

/** The token counts of a priced usage item kept in billing_detail.inputs (Go field names, no JSON tags). */
function pricedTokens(facts: unknown): UsageTokenFacts {
  const tokens: UsageTokenFacts = {}
  if (!facts || typeof facts !== 'object') return tokens
  for (const [field, source] of Object.entries(pricedTokenFields)) {
    const value = (facts as Record<string, unknown>)[source]
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0) tokens[field as keyof UsageTokenFacts] = value
  }
  return tokens
}

export type UsageComponent = { kind: string; model: string; free: boolean; reason: string; tokens: UsageTokenFacts; detail: NonNullable<UsageRow['billing_detail']> }

/** The separately priced items of a settled record (billing_detail.additional / replacement, e.g. an
 *  advisor model at its own price), each with its tokens from inputs at the same index. */
export function usageComponents(row: UsageRow, list: 'additional' | 'replacement'): UsageComponent[] {
  const settled: unknown = row.billing_detail?.[list]
  if (!Array.isArray(settled)) return []
  const inputs: unknown = row.billing_detail?.inputs?.[list]
  return settled.map((part, index) => ({
    kind: String(part?.kind ?? ''), model: String(part?.model ?? ''), free: !!part?.free, reason: String(part?.billing_reason ?? ''), detail: part?.detail || {},
    tokens: pricedTokens(Array.isArray(inputs) ? inputs[index]?.Tokens : undefined)
  }))
}

/** Recorded rounds are partial evidence, never a replacement request total. */
export function knownUsageRounds(row: UsageRow): Array<{ round: number; tokens: UsageTokenFacts; metrics?: unknown }> {
  if (row.success || Object.keys(replacementTokenFields).some(key => Number(row[key as keyof UsageTokenFacts]) > 0)) return []
  const replacement: unknown = row.billing_detail?.inputs?.replacement
  if (!Array.isArray(replacement)) return []
  return replacement.flatMap((item, index) => {
    const tokens = pricedTokens(item?.Tokens)
    return Object.keys(tokens).length ? [{ round: index + 1, tokens, metrics: item.Metrics }] : []
  })
}

/** "#12 name" for the upstream account a request used ("#12" once the account is gone, "" without one). */
export function accountLabel(u: Pick<UsageLog, 'account_id' | 'account_name'>): string {
  if (!u.account_id) return u.account_name || ''
  return u.account_name ? `#${u.account_id} ${u.account_name}` : `#${u.account_id}`
}

/** Platform a request reached its upstream account on, read off the upstream protocol: an
 *  endpoint's protocol is "<platform id>.<name>" (CONTRACTS §13). '' when the record has no
 *  such protocol: none recorded, or "local" (a count_tokens the core answered itself), where
 *  the client endpoint's platform need not be the account's. */
export function accountPlatform(u: Pick<UsageLog, 'upstream_protocol'>): string {
  const protocol = u.upstream_protocol || ''
  const dot = protocol.indexOf('.')
  return dot > 0 && dot < protocol.length - 1 ? protocol.slice(0, dot) : ''
}

/** One account (or one failed plugin batch) of a model availability trace (anomalies.availability). */
export interface AvailabilitySample {
  /** Missing when the caller lacks account:read, and on a whole-batch fail-open sample. */
  account_id?: number
  /** Filled by the API for callers with account:read. */
  account_name?: string
  plugin?: string
  model?: string
  /** block | fail_open */
  decision: string
  /** Reason code; a plugin reason follows the first ':' ("unavailable:quota_rejected:7d_fable"). */
  reason: string
  /** Unix seconds. */
  retry_at?: number
  /** Guard samples only. */
  attempt?: number
  /** The one sample of a whole plugin batch that failed open (no account). */
  batch?: boolean
}

/** The model availability precheck of one request (MODEL-AVAILABILITY-PRECHECK-DESIGN.md §6.1). */
export interface AvailabilityTrace {
  v: number
  /** blocked_all | partial | fail_open | pass (precheck admitted all, only the guard blocked) */
  outcome: string
  candidates: number
  admitted: number
  /** Reason code -> accounts. */
  counts: Record<string, number>
  /** Unix seconds: the earliest retry among the blocked accounts. */
  retry_at?: number
  samples: AvailabilitySample[]
  omitted?: number
  guard?: AvailabilitySample[]
}

const count = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? Math.floor(v) : 0)
const text = (v: unknown, max: number) => (typeof v === 'string' ? v.slice(0, max) : '')

/** A trace the core records as JSON: anomalies hold it as a string, the API may send it parsed.
 *  null for anything but a JSON object, so a malformed record shows nothing rather than failing. */
function recordedObject(raw: unknown): Record<string, unknown> | null {
  let value = raw
  if (typeof raw === 'string') {
    try { value = JSON.parse(raw) } catch { return null }
  }
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : null
}

function availabilitySamples(value: unknown, max: number): AvailabilitySample[] {
  if (!Array.isArray(value)) return []
  const out: AvailabilitySample[] = []
  for (const item of value.slice(0, max)) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) continue
    const s = item as Record<string, unknown>
    const reason = text(s.reason, 200)
    if (!reason) continue
    const sample: AvailabilitySample = { decision: text(s.decision, 32), reason }
    if (count(s.account_id) > 0) sample.account_id = count(s.account_id)
    if (text(s.account_name, 200)) sample.account_name = text(s.account_name, 200)
    if (text(s.plugin, 100)) sample.plugin = text(s.plugin, 100)
    if (text(s.model, 200)) sample.model = text(s.model, 200)
    if (count(s.retry_at) > 0) sample.retry_at = count(s.retry_at)
    if (count(s.attempt) > 0) sample.attempt = count(s.attempt)
    if (s.batch === true) sample.batch = true
    out.push(sample)
  }
  return out
}

/** The recorded availability trace of a request, or null when absent or malformed.
 *  Defensive: the record is free-form JSON, so every field is type-checked and lists are capped. */
export function availabilityTrace(u: Pick<UsageLog, 'anomalies'>): AvailabilityTrace | null {
  const o = recordedObject(u.anomalies?.availability)
  if (!o) return null
  const outcome = text(o.outcome, 32)
  if (!outcome) return null
  const counts: Record<string, number> = {}
  if (o.counts && typeof o.counts === 'object' && !Array.isArray(o.counts)) {
    for (const [k, v] of Object.entries(o.counts).slice(0, 32)) if (count(v) > 0) counts[k.slice(0, 200)] = count(v)
  }
  const trace: AvailabilityTrace = {
    v: count(o.v),
    outcome,
    candidates: count(o.candidates),
    admitted: count(o.admitted),
    counts,
    samples: availabilitySamples(o.samples, 50)
  }
  if (count(o.retry_at) > 0) trace.retry_at = count(o.retry_at)
  if (count(o.omitted) > 0) trace.omitted = count(o.omitted)
  const guard = availabilitySamples(o.guard, 10)
  if (guard.length) trace.guard = guard
  return trace
}

/** Badge tone of a trace outcome. */
export function availabilityOutcomeTone(outcome: string): 'danger' | 'warning' | 'info' | 'gray' {
  return outcome === 'blocked_all' ? 'danger' : outcome === 'partial' ? 'warning' : outcome === 'fail_open' ? 'info' : 'gray'
}

type OptionalT = (key: string, params?: Record<string, unknown>, fallback?: string) => string

/** A plugin's own reason: "quota_rejected:7d_fable" -> "Quota window 7d_fable exhausted"; unknown ones as they are. */
function pluginReasonLabel(tr: OptionalT, reason: string): string {
  const i = reason.indexOf(':')
  const code = i < 0 ? reason : reason.slice(0, i)
  const arg = i < 0 ? '' : reason.slice(i + 1)
  // quota_rejected names its window; the others carry no argument.
  if (code === 'quota_rejected') return arg ? tr('usage.availability.pluginReasons.quota_rejected', { window: arg }, reason) : reason
  return arg ? reason : tr(`usage.availability.pluginReasons.${code}`, undefined, reason)
}

/** Text of an availability reason code. The part after the first ':' is the plugin's reason
 *  ("unavailable:quota_rejected:7d_fable"); codes the console does not know show as recorded.
 *  tr is useOptionalT(). */
export function availabilityReasonLabel(tr: OptionalT, reason: string): string {
  const i = reason.indexOf(':')
  const code = i < 0 ? reason : reason.slice(0, i)
  const detail = i < 0 ? '' : reason.slice(i + 1)
  const label = tr(`usage.availability.reasons.${code}`, undefined, '')
  if (!label) return pluginReasonLabel(tr, reason)
  return detail ? tr('usage.availability.reasonWithDetail', { reason: label, detail: pluginReasonLabel(tr, detail) }) : label
}

/** One account the scheduler did not use for a request (anomalies.scheduling). */
export interface SchedulingSkip {
  /** Missing when the caller lacks account:read. */
  account_id?: number
  /** Filled by the API for callers with account:read. */
  account_name?: string
  /** Reason code (CONTRACTS §24.6). */
  reason: string
  /** The model, usage kind or account status the reason is about. */
  detail?: string
  /** Unix seconds: when a timed disable or cooldown ends. */
  until?: number
}

/** Why accounts of the group were not used for one request (CONTRACTS §24.6). */
export interface SchedulingTrace {
  v: number
  /** none (no account could serve) | busy (all rate limited or full) | picked (one served after others were passed over) */
  outcome: string
  /** Schedulable accounts of the group whose type serves the endpoint. */
  candidates: number
  /** Those of them that can serve this request. */
  eligible: number
  /** Reason code -> accounts. */
  counts: Record<string, number>
  skipped: SchedulingSkip[]
  omitted?: number
}

/** The recorded scheduling trace of a request, or null when absent or malformed.
 *  Defensive like availabilityTrace: every field is type-checked and lists are capped. */
export function schedulingTrace(u: Pick<UsageLog, 'anomalies'>): SchedulingTrace | null {
  const o = recordedObject(u.anomalies?.scheduling)
  if (!o) return null
  const outcome = text(o.outcome, 32)
  if (!outcome) return null
  const counts: Record<string, number> = {}
  if (o.counts && typeof o.counts === 'object' && !Array.isArray(o.counts)) {
    for (const [k, v] of Object.entries(o.counts).slice(0, 32)) if (count(v) > 0) counts[k.slice(0, 100)] = count(v)
  }
  const skipped: SchedulingSkip[] = []
  for (const item of Array.isArray(o.skipped) ? o.skipped.slice(0, 60) : []) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) continue
    const s = item as Record<string, unknown>
    const reason = text(s.reason, 100)
    if (!reason) continue
    const skip: SchedulingSkip = { reason }
    if (count(s.account_id) > 0) skip.account_id = count(s.account_id)
    if (text(s.account_name, 200)) skip.account_name = text(s.account_name, 200)
    if (text(s.detail, 200)) skip.detail = text(s.detail, 200)
    if (count(s.until) > 0) skip.until = count(s.until)
    skipped.push(skip)
  }
  const trace: SchedulingTrace = { v: count(o.v), outcome, candidates: count(o.candidates), eligible: count(o.eligible), counts, skipped }
  if (count(o.omitted) > 0) trace.omitted = count(o.omitted)
  return trace
}

/** Badge tone of a scheduling outcome. */
export function schedulingOutcomeTone(outcome: string): 'danger' | 'warning' | 'gray' {
  return outcome === 'none' ? 'danger' : outcome === 'busy' ? 'warning' : 'gray'
}

/** Text of a scheduling reason, with what it is about: "Model not in the account's list: claude-x".
 *  not_schedulable's detail is an account status, translated; codes the console does not know show as recorded. */
export function schedulingReasonLabel(tr: OptionalT, skip: Pick<SchedulingSkip, 'reason' | 'detail'>): string {
  const label = tr(`usage.scheduling.reasons.${skip.reason}`, undefined, skip.reason)
  if (!skip.detail) return label
  const detail = tr(`usage.scheduling.details.${skip.detail}`, undefined, skip.detail)
  return tr('usage.scheduling.reasonWithDetail', { reason: label, detail })
}

/** True when the core converted the request to another upstream protocol. */
export function isConverted(u: Pick<UsageLog, 'protocol' | 'upstream_protocol'>): boolean {
  return !!u.upstream_protocol && !!u.protocol && u.upstream_protocol !== u.protocol
}

/** The recorded grade of a converted or locally estimated request; '' when native or not recorded. */
export function convertedGrade(u: Pick<UsageLog, 'conversion'>): string {
  const g = u.conversion?.grade
  return isConvertedGrade(g) ? g : ''
}

/** A duration of a request or an attempt as the usage views show it: "2.95 s". */
export const seconds = (ms: number) => `${formatNumber(ms / 1000, 2)} s`

export type SpeedTone = 'success' | 'warning' | 'danger'

/** Colour of a first-token latency: under 5 s fine, under 10 s slow. */
export function firstTokenTone(ms: number): SpeedTone {
  return ms < 5000 ? 'success' : ms < 10000 ? 'warning' : 'danger'
}

/** The upload time worth showing (from 0.1 s): the body's arrival, part of the first token. */
export const shownUploadMs = (u: Pick<UsageLog, 'upload_ms'>) => ((u.upload_ms || 0) >= 100 ? u.upload_ms! : 0)

/** Output tokens per second, timed from the first token on a stream; null without output. */
export function tokensPerSecond(u: Pick<UsageLog, 'output_tokens' | 'latency_ms' | 'first_token_ms' | 'stream'>): number | null {
  const out = u.output_tokens || 0
  let ms = u.latency_ms || 0
  // A stream's first token waits on the prompt; the rest is generation.
  if (u.stream && u.first_token_ms && ms - u.first_token_ms >= 1000) ms -= u.first_token_ms
  return out > 0 && ms > 0 ? (out * 1000) / ms : null
}

/** Colour of a total latency: by throughput once there is enough output, else by duration. */
export function latencyTone(u: Pick<UsageLog, 'output_tokens' | 'latency_ms'>): SpeedTone {
  const s = (u.latency_ms || 0) / 1000
  if ((u.output_tokens || 0) < 100 || s <= 0) return s < 10 ? 'success' : s < 30 ? 'warning' : 'danger'
  const tps = (u.output_tokens || 0) / s
  return tps >= 30 ? 'success' : tps >= 15 ? 'warning' : 'danger'
}

const HASH_COLORS = ['bg-sky-500', 'bg-violet-500', 'bg-emerald-500', 'bg-amber-500', 'bg-rose-500', 'bg-indigo-500', 'bg-teal-500', 'bg-orange-500', 'bg-fuchsia-500', 'bg-lime-600']

/** A fill colour picked by the text, stable across renders (user avatars). */
export function hashColor(text: string): string {
  let h = 0
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) | 0
  return HASH_COLORS[Math.abs(h) % HASH_COLORS.length]
}

export function billingTone(s: string): 'success' | 'warning' | 'danger' | 'gray' {
  if (s === 'billed') return 'success'
  if (s === 'pending') return 'warning'
  if (s === 'failed') return 'danger'
  return 'gray'
}

const BLOCK_DECISIONS = ['deny', 'reject', 'block', 'blocked']

/** A free-form fact value as text: '—' when missing, JSON for objects. */
export function factText(value: unknown): string {
  if (value === null || value === undefined) return '—'
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

/** Badge tone of a hook decision. */
export function hookTone(decision: string): 'danger' | 'success' | 'gray' {
  if (BLOCK_DECISIONS.includes(decision)) return 'danger'
  return decision === 'allow' ? 'success' : 'gray'
}

/** True when a gateway hook rejected the request. */
export function isBlocked(u: UsageRow): boolean {
  if (/hook|blocked/i.test(u.error_type || '')) return true
  return (u.hook_decisions || []).some((h) => BLOCK_DECISIONS.includes(h.decision))
}

/** The blocking hook decision (plugin + note), if any. */
export function blockingHook(u: UsageRow) {
  return (u.hook_decisions || []).find((h) => BLOCK_DECISIONS.includes(h.decision)) || null
}

/** A failed request's error as one string, like new-api's log content:
 *  "status_code=503, <message>"; the hook note or error type when there is no
 *  message. Empty for successes. */
export function errorText(u: UsageRow): string {
  if (u.success) return ''
  const message = u.error_message || blockingHook(u)?.note || u.error_type || ''
  if (!u.status_code) return message
  return message ? `status_code=${u.status_code}, ${message}` : `status_code=${u.status_code}`
}

/** One upstream attempt of a retried request (attempt_trail.steps, CONTRACTS §98). */
export interface AttemptStep {
  /** Operators with account:read only. */
  account_id?: number
  /** The detail only: the list names accounts by ID. */
  account_name?: string
  /** In place of the account for an operator without account:read: the same number is the same account. */
  account_ref?: number
  /** Upstream HTTP status. */
  status: number
  /** Why the attempt failed; absent on success. */
  error_type?: string
  ms: number
  /** Operators only: the upstream's error, cut short. */
  message?: string
  /** What the core did next: "same" (the same account again) or "switch"; absent on the last step and for the caller's own records. */
  next?: string
}

/** The attempts of a request sent upstream more than once, in order. */
export interface AttemptTrail {
  steps: AttemptStep[]
  /** Attempts past the recorded ones. */
  omitted?: number
}

/** The core records at most this many attempts (failure policy max_retries ≤ 9). */
const MAX_ATTEMPT_STEPS = 10

/** Retries of a request: its attempts but the first. */
export const retryCount = (u: Pick<UsageLog, 'attempts'>) => Math.max(0, (u.attempts || 1) - 1)

/** The recorded attempts of a request, or null for one sent once, one recorded
 *  before the trail existed, or a malformed trail. Defensive like schedulingTrace;
 *  the API sends an object, a JSON string is read too. */
export function attemptTrail(u: Pick<UsageLog, 'attempt_trail'>): AttemptTrail | null {
  const o = recordedObject(u.attempt_trail)
  if (!o) return null
  const steps: AttemptStep[] = []
  for (const item of Array.isArray(o.steps) ? o.steps.slice(0, MAX_ATTEMPT_STEPS) : []) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) continue
    const s = item as Record<string, unknown>
    const step: AttemptStep = { status: count(s.status), ms: count(s.ms) }
    if (count(s.account_id) > 0) step.account_id = count(s.account_id)
    if (text(s.account_name, 200)) step.account_name = text(s.account_name, 200)
    if (count(s.account_ref) > 0) step.account_ref = count(s.account_ref)
    if (text(s.error_type, 64)) step.error_type = text(s.error_type, 64)
    if (text(s.message, 200)) step.message = text(s.message, 200)
    if (s.next === 'same' || s.next === 'switch') step.next = s.next
    steps.push(step)
  }
  if (!steps.length) return null
  const trail: AttemptTrail = { steps }
  if (count(o.omitted) > 0) trail.omitted = count(o.omitted)
  return trail
}

/** An attempt succeeded when nothing failed it and the upstream answered 2xx. */
export const attemptOk = (s: AttemptStep) => !s.error_type && s.status >= 200 && s.status < 300

/** Consecutive attempts on one account, as the chain shows them ("#16 ×2"). */
export interface AttemptRun {
  step: AttemptStep
  times: number
}

/** Folds consecutive attempts on the same account (by ID, or by account_ref where the ID is hidden) into runs. */
export function attemptRuns(steps: AttemptStep[]): AttemptRun[] {
  const runs: AttemptRun[] = []
  for (const step of steps) {
    const last = runs.at(-1)
    if (last && (step.account_id ? step.account_id === last.step.account_id : !!step.account_ref && step.account_ref === last.step.account_ref)) last.times++
    else runs.push({ step, times: 1 })
  }
  return runs
}

/** "#16" ("#16 name" in the detail, which carries names), or "Account 1" where the account is hidden;
 *  '' without either (the caller's own records). tr is useOptionalT(). */
export function attemptAccountLabel(tr: OptionalT, s: AttemptStep): string {
  if (s.account_id) return accountLabel({ account_id: s.account_id, account_name: s.account_name })
  return s.account_ref ? tr('usage.attempts.accountRef', { n: s.account_ref }) : ''
}
