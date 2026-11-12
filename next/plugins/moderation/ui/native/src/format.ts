import type { SelectOption, Tone } from '@sub2api/ui'
import { useModHost } from './host'
import type { CallCost, ModEvent, Spec } from './types'

// Display formatting shared by the moderation views (locale from the host).

/** Integer / decimal in the console locale; null and undefined are 0. */
export function num(v: number | null | undefined): string {
  return useModHost().i18n.formatNumber(v ?? 0)
}

/** A 0–1 ratio as a percentage with one decimal ("12.5%"); 0 is "0%". */
export function pct(ratio: number | null | undefined): string {
  const v = (ratio ?? 0) * 100
  return `${v === 0 ? 0 : Number(v.toFixed(1))}%`
}

const MIB = 1 << 20
/** Bytes as mebibytes with one decimal ("12.5 MiB"). */
export const mib = (bytes: number | null | undefined) => `${((bytes ?? 0) / MIB).toFixed(1)} MiB`

/** Milliseconds ("120 毫秒"). */
export function ms(v: number | null | undefined): string {
  return useModHost().t('ms', { n: num(Math.round(v ?? 0)) })
}

/** The last latency bucket is open-ended: quantiles above it read "> 10 秒". */
const LATENCY_BUCKET_MAX_MS = 10_000
export function latency(v: number | null | undefined): string {
  return (v ?? 0) > LATENCY_BUCKET_MAX_MS ? useModHost().t('stats.over10s') : ms(v)
}

/** Date and time in the console locale; '—' when empty. */
export function dt(v: string | null | undefined): string {
  return v ? useModHost().i18n.formatDateTime(v) : '—'
}

/** "#42" for an id, '—' for 0 / null. */
export const idText = (v: number | null | undefined) => (v ? `#${v}` : '—')

/** Formats a score as 0.00. */
export const score = (v: number | undefined | null) => (v === undefined || v === null ? '—' : v.toFixed(2))

/** USD amount of a decimal string; '—' when empty (not priced). */
export function money(v: string | number | null | undefined): string {
  return useModHost().i18n.formatMoney(v)
}

/** Items joined with the locale's list separator ("a、b" / "a, b"); clauseSep for whole sentences. */
export function joinList(items: readonly string[], sep: 'listSep' | 'clauseSep' = 'listSep'): string {
  return items.join(useModHost().t(sep))
}

/** Error rate above the alert threshold (percent) or flagged by the backend. */
export function overAlert(errorRate: number, alertPercent: number, flagged = false): boolean {
  return flagged || (alertPercent > 0 && errorRate * 100 > alertPercent)
}

/** Translates an enum value with a fallback to the raw value. */
export function enumLabel(group: string, v: string | undefined | null): string {
  if (!v) return '—'
  const h = useModHost()
  const key = `${group}.${v}`
  const s = h.t(key)
  return s === `plugin.${h.plugin.key}.${key}` || s === key ? v : s
}

/** Filter options with "全部" (value '') first. */
export const withAll = (options: SelectOption[]): SelectOption[] => [{ value: '', label: useModHost().t('events.all') }, ...options]

/** Select options of enum values labelled by enumLabel; `all` puts "全部" first. */
export function enumOptions(group: string, values: readonly string[], opts: { all?: boolean } = {}): SelectOption[] {
  const options = values.map((v) => ({ value: v, label: enumLabel(group, v) }))
  return opts.all ? withAll(options) : options
}

/** Category label: LLM categories and OpenAI categories are translated, Jev labels come with the record. */
export function categoryLabel(id: string, label?: string): string {
  if (label) return label
  const h = useModHost()
  const key = `cat.${id.replace(/\//g, '_')}`
  const s = h.t(key)
  return s === `plugin.${h.plugin.key}.${key}` || s === key ? id : s
}

/** "全部模型 · 采样率 100 · 总超时 0" of a flow spec. */
export function specText(s: Spec | undefined): string {
  if (!s) return ''
  const t = useModHost().t
  const mf = s.model_filter
  const models = mf?.type && mf.type !== 'all' ? `${enumLabel('modelFilter', mf.type)}: ${(mf.models || []).join(', ')}` : enumLabel('modelFilter', 'all')
  return `${models} · ${t('flows.sampleRate')} ${s.sample_rate} · ${t('flows.totalTimeout')} ${s.total_timeout_ms}`
}

/**
 * Display of a call cost: priced → "$…", free → 免费, unpriced → 未定价 with
 * the reason as hint. A bare string is an amount; null = not priced.
 */
export function callCostText(c: CallCost | string | null | undefined): { text: string; tone: Tone; hint: string } {
  const h = useModHost()
  if (c === undefined) return { text: '', tone: 'gray', hint: '' }
  if (typeof c === 'string') return c ? { text: money(c), tone: 'gray', hint: '' } : { text: h.t('cost.notPriced'), tone: 'warning', hint: '' }
  if (!c || c.mode === 'unpriced' || (c.mode !== 'free' && !c.cost)) {
    return { text: h.t('cost.notPriced'), tone: 'warning', hint: c?.reason ? enumLabel('priceReason', c.reason) : '' }
  }
  if (c.mode === 'free') return { text: h.t('channels.free'), tone: 'gray', hint: '' }
  return { text: money(c.cost), tone: 'gray', hint: c.billing_model ? h.t('channels.pricedAs', { model: c.billing_model }) : '' }
}

/** A suspicious record (hit or block): the only ones reviewed and counted. */
export const isSuspicious = (e: Pick<ModEvent, 'result'>) => e.result === 'hit' || e.result === 'block'

/**
 * Final verdict of a record, computed only in the UI (§72.9): the
 * moderator's review; a suspicious record nobody reviewed is pending. null =
 * nothing to review (a passing record).
 */
export interface FinalVerdict {
  key: 'violation' | 'false_positive' | 'none'
  tone: Tone
}
export function finalVerdict(e: Pick<ModEvent, 'review_status' | 'result'>): FinalVerdict | null {
  if (e.review_status === 'confirmed') return { key: 'violation', tone: 'danger' }
  if (e.review_status === 'false_positive') return { key: 'false_positive', tone: 'success' }
  return isSuspicious(e) ? { key: 'none', tone: 'gray' } : null
}

/**
 * Watch history reason: system codes are translated ("record:<id>",
 * "trend:<trend>", "<denied action>:<trend>"); administrator text is kept.
 */
export function watchReason(action: string, reason: string): string {
  if (!reason) return ''
  const h = useModHost()
  const i = reason.indexOf(':')
  if (i <= 0) return reason
  const head = reason.slice(0, i)
  const tail = reason.slice(i + 1)
  if (head === 'record' && /^\d+$/.test(tail)) return h.t('watchReason.record', { id: tail })
  const known = (group: string, v: string) => enumLabel(group, v) !== v
  if (head === 'trend' && known('trend', tail)) return h.t('watchReason.trend', { trend: enumLabel('trend', tail) })
  if (action === 'auto_not_allowed' && known('deniedAction', head)) {
    return h.t('watchReason.denied', { action: enumLabel('deniedAction', head), trend: enumLabel('trend', tail) })
  }
  return reason
}
