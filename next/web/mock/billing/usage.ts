// Mock handlers of the usage records: GET /usage, /me/usage, their details,
// summaries, overviews, the last minute and the CSV export.
import { fail, on, paginate } from '../router'
import { ACCOUNTS } from './usageData'
import { overviewOf, realtimeOf, usageCSV, usageFilter, usageSummary, zoned } from './usageQuery'
import { usageRows } from './usageSeed'

function publicUsage(u: any, self = false) {
  const { _detail, _price, _anomalies, ...rest } = u
  void _detail
  void _price
  void _anomalies
  if (self) {
    delete rest.account_id
    delete rest.account_name
    rest.conversion = null
    // Self-service: each attempt's outcome and time, nothing about the account.
    if (rest.attempt_trail) rest.attempt_trail = { ...rest.attempt_trail, steps: rest.attempt_trail.steps.map((s: any) => ({ status: s.status, error_type: s.error_type, ms: s.ms })) }
  }
  return rest
}

/**
 * A detail's metrics. Explicit synthetic TTL evidence lets the detail preview
 * exercise both the compact summary and its expanded counters without using
 * production data.
 */
export function usageMetrics(u: any) {
  const cacheWrite = u.cache_creation_tokens + (u.cache_creation_1h_tokens || 0)
  return cacheWrite ? { cache_write_evidence: {
    version: 1, total: { state: 'value', value: cacheWrite },
    explicit_5m: { state: 'value', value: u.cache_creation_tokens },
    explicit_1h: { state: 'value', value: u.cache_creation_1h_tokens || 0 },
    unclassified_tokens: 0, completeness: 'complete', source: 'json',
    pricing_policy: 'platform_default_cache_write_compat'
  } } : {}
}

function usageDetail(u: any, self = false) {
  const row = publicUsage(u, self)
  // The detail names the accounts of the trail; the list has their IDs only.
  if (row.attempt_trail && !self) row.attempt_trail = { ...row.attempt_trail, steps: row.attempt_trail.steps.map((s: any) => ({ ...s, account_name: ACCOUNTS.find((a) => a.id === s.account_id)?.name })) }
  return {
    ...row,
    billing_detail: u._detail ? { ...u._detail, ledger_id: u.ledger_id ?? null } : null,
    price: u._price, metrics: usageMetrics(u), attempts: u.attempts ?? 1,
    // Self-service details carry no anomalies, like the server.
    ...(u._anomalies && !self ? { anomalies: u._anomalies } : {}),
    user_agent: 'claude-cli/mock (external, cli; usage-detail-preview)'
  }
}

on('GET', '/usage/summary', (req) => usageSummary(usageRows, req.query))
on('GET', '/me/usage/summary', (req) =>
  req.query.group_by === 'user' || req.query.group_by === 'upstream_model'
    ? fail(400, 'invalid_argument', 'group_by must be day, hour, none, model, api_key, group, endpoint, platform, protocol or ip')
    : usageSummary(usageRows.filter((u) => u.user_id === 1), req.query)
)
// GET /me/prices lives in pricing.ts: it depends on the caller's segment and groups.
// GET /me/usage/overview (CONTRACTS §66): the windows from the same rows.
on('GET', '/me/usage/overview', (req) => ({ ...overviewOf(usageRows.filter((u) => u.user_id === 1), req.query.tz || 'UTC'), api_keys: { total: 2, active: 2 } }))
// GET /usage/overview (CONTRACTS §66.6): all users, plus users and accounts.
on('GET', '/usage/overview', (req) => {
  const tz = req.query.tz || 'UTC'
  const o = overviewOf(usageRows, tz)
  const today = zoned(new Date().toISOString(), tz).day
  const active = new Set(usageRows.filter((u) => zoned(u.created_at, tz).day === today).map((u) => u.user_id)).size
  return { ...o, users: { total: 4, new_today: 1, active_today: active }, accounts: { total: 6, available: 4, auto_disabled: 1, disabled: 1 } }
})
on('GET', '/usage/realtime', (req) => realtimeOf(usageRows, req.query))
on('GET', '/me/usage/realtime', (req) => realtimeOf(usageRows.filter((u) => u.user_id === 1), req.query))
on('GET', '/usage/export', (req) => usageCSV(usageRows, req.query, 'all'))
on('GET', '/me/usage/export', (req) => usageCSV(usageRows.filter((u) => u.user_id === 1), req.query, 'self'))
on('GET', '/usage', (req) => paginate(usageFilter(usageRows, req.query).map((u) => publicUsage(u)), req.query))
on('GET', '/usage/:id', (req) => {
  const u = usageRows.find((x) => x.id === Number(req.params.id))
  return u ? usageDetail(u) : fail(404, 'not_found', 'usage record not found')
})
on('GET', '/me/usage', (req) => paginate(usageFilter(usageRows.filter((u) => u.user_id === 1), req.query).map((u) => publicUsage(u, true)), req.query))
on('GET', '/me/usage/:id', (req) => {
  const u = usageRows.find((x) => x.id === Number(req.params.id) && x.user_id === 1)
  return u ? usageDetail(u, true) : fail(404, 'not_found', 'usage record not found')
})
