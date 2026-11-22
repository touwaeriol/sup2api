// Mock handlers of GET /account-usage/*: the usage records of the accounts
// in the caller's reach, without the charge.
import { fail, on, paginate, type MockRequest } from '../router'
import { accounts } from '../accounts'
import { caller, inScope, ownerScope } from '../core'
import { fieldFail, money } from './shared'
import { usageMetrics } from './usage'
import { baseCost } from './usageData'
import { overviewOf, realtimeOf, usageCSV, usageFilter, usageSummary } from './usageQuery'
import { usageRows } from './usageSeed'

// GET /account-usage/* (CONTRACTS §103.5): the records of the accounts in the
// caller's reach of usage:account:read (deleted ones' records stay out of the
// mock: it forgets a deleted account), whitelisted fields only.
const ACCOUNT_USAGE_FILTERS = [
  'account_id', 'model', 'platform', 'plugin_key', 'account_type', 'protocol', 'endpoint', 'error_type', 'request_id',
  'success', 'stream', 'status_code', 'retried', 'from', 'to', 'tz'
]
const ACCOUNT_RECORD_FIELDS = [
  'id', 'request_id', 'created_at', 'account_id', 'account_name', 'plugin_key', 'platform', 'account_type', 'protocol', 'endpoint',
  'model', 'upstream_model', 'stream', 'status_code', 'success', 'error_type', 'input_tokens', 'output_tokens', 'cache_read_tokens',
  'cache_creation_tokens', 'cache_creation_1h_tokens', 'latency_ms', 'first_token_ms'
]
const ACCOUNT_GROUPS = ['day', 'hour', 'none', 'model', 'account', 'platform', 'endpoint', 'protocol']

/** A window without the charge (AccountTotals): the account view bills nothing. */
function unbilled({ total_cost, estimated_cost, ...rest }: any) {
  void total_cost
  void estimated_cost
  return rest
}
/** An AccountSummaryRow: a summary row without the user, the charge and last_at. */
function accountSummaryRow({ user_email, last_at, ...rest }: any) {
  void user_email
  void last_at
  return unbilled(rest)
}

/** Whether the caller reaches an account, by its id. */
type AccountReach = (id: number | null | undefined) => boolean

/** An AccountRecord: in the trail, an account outside the reach is only its account_ref. */
function accountRecord(u: any, reaches: AccountReach) {
  const row: any = { ...Object.fromEntries(ACCOUNT_RECORD_FIELDS.map((k) => [k, u[k]])), attempts: u.attempts ?? 1, standard_cost: money(baseCost(u)) }
  if (!u.attempt_trail) return row
  const refs: number[] = []
  const ref = (id: number) => refs.indexOf(id) + 1 || refs.push(id)
  const steps = u.attempt_trail.steps.map(({ account_id, status, error_type, ms, next }: any) => ({
    status, error_type, ms, next,
    ...(reaches(account_id) ? { account_id, account_name: accounts.find((a) => a.id === account_id)?.name } : { account_ref: ref(account_id) })
  }))
  return { ...row, attempt_trail: { ...u.attempt_trail, steps } }
}

/**
 * Registers GET /account-usage<path>, as the server checks it: 403 without
 * usage:account:read, then a parameter beyond `params` is 400 (by name; a
 * route without `params` reads none and ignores them), and `handler` gets the
 * records in reach.
 */
function accountUsageRoute(path: string, params: string[] | null, handler: (rows: any[], reaches: AccountReach, req: MockRequest) => unknown) {
  on('GET', `/account-usage${path}`, (req) => {
    const who = caller(req)
    const scope = ownerScope(who, 'usage:account:read')
    if (!scope) return fail(403, 'permission_denied', 'Permission denied')
    const extra = params ? Object.keys(req.query).filter((k) => !params.includes(k)).sort() : []
    if (extra.length) return fieldFail(extra.map((field) => ({ field, code: 'not_allowed', message: 'not accepted here' })))
    const reaches: AccountReach = (id) => accounts.some((a) => a.id === id && inScope(scope, who, a))
    return handler(usageRows.filter((u) => reaches(u.account_id)), reaches, req)
  })
}

accountUsageRoute('', [...ACCOUNT_USAGE_FILTERS, 'page', 'page_size', 'cursor'], (rows, reaches, req) =>
  paginate(usageFilter(rows, req.query).map((u) => accountRecord(u, reaches)), req.query)
)
accountUsageRoute('/summary', [...ACCOUNT_USAGE_FILTERS, 'group_by', 'order', 'limit'], (rows, _, req) => {
  const { group_by: by, order } = req.query
  // Like the server: invalid_argument with a message, no fields.
  if (by && !ACCOUNT_GROUPS.includes(by)) return fail(400, 'invalid_argument', `group_by must be ${ACCOUNT_GROUPS.slice(0, -1).join(', ')} or ${ACCOUNT_GROUPS.at(-1)}`)
  if (order && !['requests', 'tokens'].includes(order)) return fail(400, 'invalid_argument', 'order must be requests or tokens')
  return usageSummary(rows, req.query).map(accountSummaryRow)
})
accountUsageRoute('/overview', ['tz'], (rows, reaches, req) => {
  const { today, yesterday, last_30d, ...o } = overviewOf(rows, req.query.tz || 'UTC')
  const live = accounts.filter((a) => reaches(a.id))
  const count = (status: string) => live.filter((a) => a.status === status).length
  return { ...o, today: unbilled(today), yesterday: unbilled(yesterday), last_30d: unbilled(last_30d), accounts: { total: live.length, available: count('active'), auto_disabled: count('auto_disabled'), disabled: count('disabled') } }
})
accountUsageRoute('/realtime', ACCOUNT_USAGE_FILTERS, (rows, _, req) => realtimeOf(rows, req.query))
accountUsageRoute('/export', [...ACCOUNT_USAGE_FILTERS, 'lang'], (rows, _, req) => usageCSV(rows, req.query, 'account'))
accountUsageRoute('/:id', null, (rows, reaches, req) => {
  const u = rows.find((x) => x.id === Number(req.params.id))
  return u ? { ...accountRecord(u, reaches), metrics: usageMetrics(u) } : fail(404, 'not_found', 'usage record not found')
})
