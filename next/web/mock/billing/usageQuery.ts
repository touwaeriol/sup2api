// The queries over usage records the usage and account usage endpoints share:
// filters, summaries, overviews, the last minute and the CSV export.
import type { UsageView } from '../../src/views/usage/view'
import { money } from './shared'
import { baseCost } from './usageData'

/** The conversion= filter: by grade, any converted grade, or with losses. */
function conversionMatch(c: any, want: string) {
  if (want === 'lossy') return !!(c?.lost || c?.response_lost)
  if (want === 'native') return !c || c.grade === 'native'
  if (want === 'converted') return ['good', 'fair', 'discouraged'].includes(c?.grade)
  return c?.grade === want
}

/** The filters that match a record's field exactly. */
const EXACT_FILTERS = [
  'user_id', 'group_id', 'group_name', 'api_key_id', 'account_id', 'client_request_id', 'client_ip', 'stream', 'billing_status', 'model',
  'platform', 'protocol', 'endpoint', 'plugin_key', 'account_type', 'error_type', 'request_id', 'status_code'
]

export function usageFilter(rows: any[], q: Record<string, string>) {
  return rows.filter((u) => {
    if (q.from && u.created_at < q.from) return false
    if (q.to && u.created_at > q.to) return false
    if (EXACT_FILTERS.some((k) => q[k] && String(u[k]) !== q[k])) return false
    if (q.model_contains && !u.model.toLowerCase().includes(q.model_contains.toLowerCase())) return false
    if (q.api_key_name && !u.api_key_name.toLowerCase().includes(q.api_key_name.toLowerCase())) return false
    if (q.conversion && !conversionMatch(u.conversion, q.conversion)) return false
    if (q.retried === 'true' && !((u.attempts ?? 1) > 1)) return false
    // Like the server: a request that never reached an account (attempts 0) is in neither.
    if (q.retried === 'false' && (u.attempts ?? 1) !== 1) return false
    if (q.success === 'true' && !u.success) return false
    if (q.success === 'false' && u.success) return false
    return true
  })
}

/** Calendar parts of an instant in an IANA zone (the summary's tz=). */
export function zoned(iso: string, tz: string) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: tz || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(iso))
  const p = Object.fromEntries(parts.map((x) => [x.type, x.value]))
  return { day: `${p.year}-${p.month}-${p.day}`, hour: `${p.year}-${p.month}-${p.day} ${p.hour}:00` }
}

/** A summary row before any record. */
const emptySummary = (key: string) => ({ key, requests: 0, success: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, prompt_tokens: 0, total_cost: 0, standard_cost: 0, _lat: 0, _ft: 0, _n: 0 })

/** What a summary row is keyed by, per group_by; a time (day, the default, or hour) is read in the summary's zone. */
const SUMMARY_KEYS: Record<string, (u: any) => string> = {
  model: (u) => u.model,
  upstream_model: (u) => u.upstream_model,
  user: (u) => String(u.user_id),
  account: (u) => String(u.account_id ?? ''),
  ip: (u) => u.client_ip,
  api_key: (u) => String(u.api_key_id),
  group: (u) => u.group_name,
  endpoint: (u) => u.endpoint,
  platform: (u) => u.platform,
  protocol: (u) => u.protocol,
  none: () => ''
}
const summaryKey = (u: any, by: string, tz: string) => SUMMARY_KEYS[by]?.(u) ?? zoned(u.created_at, tz)[by === 'hour' ? 'hour' : 'day']

/** Adds a record to its summary row (grouped by `by`). */
function addToSummary(s: any, u: any, by: string) {
  if (by === 'user') s.user_email = u.user_email
  if (by === 'account') s.account_name = u.account_name
  if (by === 'ip' && (!s.last_at || u.created_at > s.last_at)) s.last_at = u.created_at
  s.requests++
  if (u.success) {
    s.success++
    s._n++
    s._lat += u.latency_ms || 0
    s._ft += u.first_token_ms || 0
  }
  s.input_tokens += u.input_tokens
  s.output_tokens += u.output_tokens
  s.cache_read_tokens += u.cache_read_tokens
  s.cache_creation_tokens += u.cache_creation_tokens + (u.cache_creation_1h_tokens || 0)
  // The mock generator's input_tokens are the exclusive pricing variable p.
  s.prompt_tokens += u.input_tokens + u.cache_read_tokens + u.cache_creation_tokens + (u.cache_creation_1h_tokens || 0)
  s.total_cost += Number(u.total_cost)
  s.standard_cost += baseCost(u)
  return s
}

/** A summary row as the API sends it: the total tokens, the averages, money as text. */
function summaryOut(row: any) {
  const { _lat, _ft, _n, ...s } = row || emptySummary('')
  return {
    ...s,
    total_tokens: s.input_tokens + s.output_tokens + s.cache_read_tokens + s.cache_creation_tokens,
    latency_ms_avg: _n ? Math.round(_lat / _n) : 0,
    first_token_ms_avg: _n ? Math.round(_ft / _n) : 0,
    total_cost: money(s.total_cost),
    standard_cost: money(s.standard_cost)
  }
}

/** GET …/usage/summary (CONTRACTS §66): the records of the last 30 days by default, grouped by group_by. */
export function usageSummary(rows: any[], query: Record<string, string>) {
  const q = { ...query }
  if (!q.from && !q.to) q.from = new Date(Date.now() - 30 * 86400000).toISOString()
  const by = q.group_by || 'day'
  const map = new Map<string, any>()
  if (by === 'none') map.set('', null)
  for (const u of usageFilter(rows, q)) {
    const key = summaryKey(u, by, q.tz || 'UTC')
    map.set(key, addToSummary(map.get(key) || emptySummary(key), u, by))
  }
  const out = [...map.values()].map(summaryOut)
  if (by === 'ip') return out.sort((a, b) => b.requests - a.requests || a.key.localeCompare(b.key)).slice(0, 100)
  return q.order ? ranked(out, q.order, Number(q.limit) || 0) : out.sort((a, b) => a.key.localeCompare(b.key))
}

/** order=cost|requests|tokens&limit=n (CONTRACTS §66.6): the top n, the rest folded into key __other__. */
function ranked(rows: any[], order: string, limit: number) {
  const metric = (r: any) => (order === 'requests' ? r.requests : order === 'tokens' ? r.total_tokens : Number(r.total_cost))
  const sorted = [...rows].sort((a, b) => metric(b) - metric(a) || a.key.localeCompare(b.key))
  if (!limit || sorted.length <= limit) return sorted
  const rest = sorted.slice(limit)
  const other: any = { key: '__other__', requests: 0, success: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, prompt_tokens: 0, total_tokens: 0, total_cost: 0, standard_cost: 0 }
  for (const r of rest) {
    for (const k of ['requests', 'success', 'input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_creation_tokens', 'prompt_tokens', 'total_tokens']) other[k] += r[k]
    other.total_cost += Number(r.total_cost)
    other.standard_cost += Number(r.standard_cost)
  }
  return [...sorted.slice(0, limit), { ...other, total_cost: money(other.total_cost), standard_cost: money(other.standard_cost) }]
}

export function overviewOf(rows: any[], tz: string) {
  const today = zoned(new Date().toISOString(), tz).day
  const yesterday = zoned(new Date(Date.now() - 86400000).toISOString(), tz).day
  const totals = (part: any[]) => {
    const { key, total_tokens, ...s } = usageSummary(part, { group_by: 'none', from: '1970-01-01T00:00:00Z' })[0]
    void key
    void total_tokens
    return s
  }
  const recent = rows.filter((u) => Date.now() - Date.parse(u.created_at) < 5 * 60000)
  return {
    tz,
    today: totals(rows.filter((u) => zoned(u.created_at, tz).day === today)),
    yesterday: totals(rows.filter((u) => zoned(u.created_at, tz).day === yesterday)),
    last_30d: totals(rows),
    last_5m: { requests: recent.length, tokens: recent.reduce((a, u) => a + u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_creation_tokens, 0) }
  }
}

// GET /usage/realtime, /me/usage/realtime (CONTRACTS §66.7): the last minute under the filters.
export function realtimeOf(rows: any[], q: Record<string, string>) {
  const since = Date.now() - 60_000
  const recent = usageFilter(rows, q).filter((u) => new Date(u.created_at).getTime() >= since)
  const ok = recent.filter((u) => u.success)
  return { rpm: ok.length, tpm: ok.reduce((n, u) => n + u.input_tokens + u.output_tokens, 0), recent_failures: recent.length - ok.length }
}
/** The export's call chain (CONTRACTS §98): "#16×2 → #12" for an operator, the count for the
 *  caller's own records and for a row without a trail; empty for a request sent once. */
function callChain(u: any, self: boolean, zh: boolean): string {
  if (!(u.attempts > 1)) return ''
  if (self || !u.attempt_trail) return zh ? `${u.attempts} 次` : `${u.attempts} attempts`
  const runs: Array<{ id: number; times: number }> = []
  for (const s of u.attempt_trail.steps) {
    const last = runs[runs.length - 1]
    if (last?.id === s.account_id) last.times++
    else runs.push({ id: s.account_id, times: 1 })
  }
  const omitted = u.attempt_trail.omitted ? [`+${u.attempt_trail.omitted}`] : []
  return [...runs.map((r) => `#${r.id}${r.times > 1 ? `×${r.times}` : ''}`), ...omitted].join(' → ')
}

type CsvColumn = [zh: string, en: string, value: (u: any) => unknown, views?: UsageView[]]

/** The columns of an export in a view; `zh` is the language of the call chain. */
function csvColumns(view: UsageView, zh: boolean): CsvColumn[] {
  const cols: CsvColumn[] = [
    ['时间', 'Time', (u) => u.created_at],
    ['请求 ID', 'Request ID', (u) => u.request_id],
    ['用户', 'User', (u) => u.user_email, ['all']],
    ['API 密钥', 'API key', (u) => u.api_key_name, ['all', 'self']],
    ['账号', 'Account', (u) => u.account_name ?? '', ['all', 'account']],
    ['模型', 'Model', (u) => u.model],
    ['上游模型', 'Upstream model', (u) => u.upstream_model, ['all', 'account']],
    ['状态', 'Status', (u) => u.status_code],
    ['输入', 'Input', (u) => u.input_tokens],
    ['输出', 'Output', (u) => u.output_tokens],
    ['缓存读', 'Cache read', (u) => u.cache_read_tokens],
    ['缓存写', 'Cache write', (u) => u.cache_creation_tokens],
    // The account view bills nothing: the list price.
    ['费用', 'Cost', (u) => (view === 'account' ? money(baseCost(u)) : u.total_cost)],
    ['调用链路', 'Call chain', (u) => callChain(u, view !== 'all', zh)]
  ]
  return cols.filter((c) => !c[3] || c[3].includes(view))
}

/** A CSV cell: a formula-like text quoted with ', a cell with a separator in double quotes. */
function csvCell(v: unknown) {
  let s = String(v ?? '')
  if (/^[=+\-@\t\r]/.test(s)) s = "'" + s
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

/** GET …/usage/export (CONTRACTS §66.5): a short CSV with the BOM, like the server. */
export function usageCSV(rows: any[], q: Record<string, string>, view: UsageView) {
  const zh = q.lang === 'zh'
  const shown = csvColumns(view, zh)
  const lines = [shown.map((c) => (zh ? c[0] : c[1])).join(',')]
  for (const u of usageFilter(rows, q).slice(0, 1000)) lines.push(shown.map((c) => csvCell(c[2](u))).join(','))
  const body = Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from(lines.join('\r\n') + '\r\n', 'utf8')])
  const stamp = new Date().toISOString().replace(/[-:]/g, '').replace('T', '_').slice(0, 15)
  return {
    __status: 200,
    __headers: { 'Content-Type': 'text/csv; charset=utf-8', 'Content-Disposition': `attachment; filename="usage_${stamp}.csv"` },
    __bytes: body,
    body: undefined
  }
}
