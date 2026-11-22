// Usage records the random ones leave out: model availability traces, an
// unreported execution, records priced by group pricing, an advisor call, and
// retried requests (attempt trails).
import { configPrice, priceFor, remember } from './priceStore'
import { money } from './shared'
import { singleTier } from '../../src/views/prices/priceExpr'
import { ACCOUNTS, CLIENT_IPS, GROUPS, MODELS, NATIVE_ENDPOINT, OWN_MODELS, USERS, charge, type PricingTerms } from './usageData'

const minutesAgo = (min: number) => new Date(Date.now() - min * 60000)
const unix = (d: Date, min: number) => Math.floor(d.getTime() / 1000) + min * 60
/** The console user calling with its first key. */
const CONSOLE_CALLER = { user_id: 1, user_email: USERS[0].email, api_key_id: 1, api_key_name: `${USERS[0].name}-key`, client_ip: CLIENT_IPS[1][0] }

/** What the availability and unreported records share: claude-fable-4 in the vip group, unbilled. */
const FABLE = {
  ...CONSOLE_CALLER,
  group_id: 2, group_name: 'vip', platform: 'anthropic', protocol: 'anthropic.messages', endpoint: '/v1/messages',
  model: 'claude-fable-4', upstream_model: 'claude-fable-4', stream: true, cache_read_tokens: 0, cache_creation_tokens: 0,
  cache_creation_1h_tokens: 0, rate_multiplier: '1.50', billing_mode: undefined, matched_tier: '', price_id: null, expr_hash: '',
  hook_decisions: [], upload_ms: 40, sticky_rule: 'claude-code-session', sticky_hit: false, _detail: null, _price: null
}
const FABLE_BLOCK = { model: 'claude-fable-4', decision: 'block', reason: 'unavailable:quota_rejected:7d_fable' }

/** Every Claude subscription candidate blocked by its 7d Fable window (429, no account). */
function blockedAllRow() {
  const at = minutesAgo(3)
  return {
    ...FABLE, id: 4990, request_id: 'req_availability0001', client_request_id: '', created_at: at.toISOString(),
    account_id: null, account_name: null, plugin_key: '', account_type: '', upstream_protocol: '',
    status_code: 429, success: false, error_type: 'model_availability',
    error_message: 'the requested model is unavailable for these accounts, please retry later',
    input_tokens: 0, output_tokens: 0, total_cost: money(0), billing_status: 'free', latency_ms: 4,
    _anomalies: { availability: JSON.stringify({
      v: 1, outcome: 'blocked_all', candidates: 3, admitted: 0, retry_at: unix(at, 134),
      counts: { 'unavailable:quota_rejected:7d_fable': 3 },
      samples: [
        { account_id: 21, account_name: 'claude-key-1', ...FABLE_BLOCK, retry_at: unix(at, 134) },
        { account_id: 22, account_name: 'claude-key-2', ...FABLE_BLOCK, retry_at: unix(at, 391) },
        { account_id: 24, ...FABLE_BLOCK, retry_at: unix(at, 1450) }
      ]
    }) }
  }
}

/** One candidate blocked while another served the request. */
function partialRow() {
  const at = minutesAgo(9)
  return {
    ...FABLE, id: 4991, request_id: 'req_availability0002', client_request_id: '', created_at: at.toISOString(),
    account_id: 22, account_name: 'claude-key-2', plugin_key: 'anthropic', account_type: 'apikey', upstream_protocol: 'anthropic.messages',
    status_code: 200, success: true, error_type: '', error_message: '',
    input_tokens: 1200, output_tokens: 300, total_cost: money(0), billing_status: 'free', latency_ms: 3200, first_token_ms: 900,
    _anomalies: { availability: JSON.stringify({
      v: 1, outcome: 'partial', candidates: 3, admitted: 2, retry_at: unix(at, 72),
      counts: { 'unavailable:quota_rejected:7d_fable': 1, snapshot_unavailable: 1 },
      samples: [
        { account_id: 21, account_name: 'claude-key-1', ...FABLE_BLOCK, retry_at: unix(at, 72) },
        { account_id: 24, model: 'claude-fable-4', decision: 'fail_open', reason: 'snapshot_unavailable' }
      ],
      guard: [{ account_id: 24, model: 'claude-fable-4', decision: 'block', reason: 'guard_models_changed', attempt: 1 }]
    }) }
  }
}

/** Two records with a model availability trace (anomalies.availability, detail only) and an unreported execution (design §8.5): the plugin's Execute returned no usable usage, so the core wrote this free record and released the pre-charge. */
export function availabilityRows() {
  const unreported = {
    ...FABLE, id: 4989, request_id: 'req_unreported0001', client_request_id: '', created_at: minutesAgo(15).toISOString(),
    account_id: 16, account_name: 'relay-1', plugin_key: 'relay', account_type: 'relay_key', upstream_protocol: 'anthropic.messages',
    status_code: 200, success: false, error_type: 'usage_unreported', error_message: 'plugin relay returned no usage for this execution',
    input_tokens: 0, output_tokens: 0, total_cost: money(0), billing_status: 'free', latency_ms: 2100,
    _anomalies: { usage_extract: 'unreported' }
  }
  return [blockedAllRow(), partialRow(), unreported]
}

// Four records priced by group pricing, as mock/pricingStore.ts sets it up
// for the console user (user group VIP: ×0.85 for gpt-4o and ×0.9 for
// claude-sonnet-4-5 in the default group, ×0.7 in the vip group; the vip
// group sells claude-haiku-4-5 at a price of its own). One rate applies,
// the most specific set: the user group's for the model, then its own in the
// account group.
const VIP = 2
const GROUP_PRICING: Array<{ group: (typeof GROUPS)[number]; model: string; rate: number; terms: PricingTerms }> = [
  { group: GROUPS[0], model: 'gpt-4o', rate: 0.85, terms: { segment_id: VIP, rate_source: 'segment_model' } },
  {
    group: GROUPS[1],
    model: 'claude-haiku-4-5',
    rate: 0.7,
    terms: { segment_id: VIP, rate_source: 'segment', rule: { source: 'group', custom: configPrice('visual', singleTier({ p: 0.8, c: 4, cr: 0.08, cc: 1 })) } }
  },
  { group: GROUPS[1], model: 'claude-sonnet-4-5', rate: 0.7, terms: { segment_id: VIP, rate_source: 'segment' } },
  { group: GROUPS[0], model: 'claude-sonnet-4-5', rate: 0.9, terms: { segment_id: VIP, rate_source: 'segment_model' } }
]

function groupPricingRow({ group, model, rate, terms }: (typeof GROUP_PRICING)[number], i: number) {
  const price = priceFor(model)!
  const at = minutesAgo(15 + i * 10)
  const billing = charge(price, { usage: { p: 12000, c: 1800 }, at }, rate, terms)!
  const account = ACCOUNTS.find((a) => (OWN_MODELS[a.plugin_key] ?? MODELS).includes(model))!
  const native = NATIVE_ENDPOINT[account.upstream]
  const custom = terms.rule?.custom
  return {
    id: 4992 + i, request_id: `req_grouppricing000${i + 1}`, client_request_id: '', created_at: at.toISOString(), ...CONSOLE_CALLER,
    group_id: group.id, group_name: group.name, account_id: account.id, account_name: account.name, plugin_key: account.plugin_key, account_type: account.type,
    platform: native?.platform ?? 'anthropic', protocol: native?.protocol ?? 'anthropic.messages', upstream_protocol: account.upstream, conversion: null,
    endpoint: native?.endpoint ?? '/v1/messages', model, upstream_model: model, stream: true, status_code: 200, success: true, error_type: '', error_message: '',
    input_tokens: 12000, output_tokens: 1800, cache_read_tokens: 0, cache_creation_tokens: 0, cache_creation_1h_tokens: 0,
    total_cost: money(billing.total), rate_multiplier: rate.toFixed(2), billing_status: billing.status, billing_mode: price.mode, matched_tier: billing.tier,
    // The price is the global one whatever rule applied; the expression is the one that was evaluated.
    price_id: price.id, expr_hash: custom ? remember(custom.expression) : price.expr_hash,
    latency_ms: 2600, first_token_ms: 700, upload_ms: 40, sticky_rule: 'claude-code-session', sticky_hit: true, hook_decisions: [],
    _detail: billing.detail, _price: { id: price.id, model: price.model, source: price.source }
  }
}

export const groupPricingRows = () => GROUP_PRICING.map(groupPricingRow)

const term = (label: string, quantity: number, rate: number, cost: string) => ({ tier: '', label, quantity, rate, expr: `${label}*${rate}`, cost })

/** The billing detail of the advisor call: the conversation's price, and the advisor's tokens as additional usage. */
function advisorDetail(advisor: { model: string; expression: string }) {
  const advisorTokens = { Input: 3000, Output: 400, CacheRead: 0, CacheCreation: 0, CacheCreation1h: 0 }
  const advisorBreakdown = { vars: { p: 3000, c: 400, len: 3000 }, items: [term('p', 3000, 15, '0.045'), term('c', 400, 75, '0.03')], subtotal: '0.075', base: '0.075', rules_multiplier: '1' }
  return {
    expr_version: 1, tier: '', rules: [], breakdown: { vars: { p: 12000, c: 1800, len: 12000 }, items: [term('p', 12000, 1, '0.012'), term('c', 1800, 5, '0.009')], subtotal: '0.021', base: '0.021', rules_multiplier: '1' }, cost: '0.096', rate_multiplier: '1', total_cost: '0.096', primary_cost: '0.021', primary_total_cost: '0.021',
    additional: [{ kind: 'advisor', model: advisor.model, price_id: 5, expression_hash: remember(advisor.expression),
      detail: { expr_version: 1, tier: '', rules: [], breakdown: advisorBreakdown, cost: '0.075', rate_multiplier: '1', total_cost: '0.075' } }],
    inputs: { semantics: 'inclusive', additional: [{ Kind: 'advisor', Model: advisor.model, UsageSemantics: 'inclusive', Tokens: advisorTokens, RateMultiplier: '1' }] }
  }
}

/**
 * A Messages request that declared an advisor model: the conversation at its
 * model's price, the advisor's tokens as additional usage at the advisor
 * model's (12000*1 + 1800*5 = 21000 and 3000*15 + 400*75 = 75000 micro-USD).
 */
export function advisorRow() {
  const chat = { model: 'claude-haiku-4-5', expression: 'p*1 + c*5' }
  return {
    id: 4996, request_id: 'req_advisor0001', client_request_id: '', created_at: minutesAgo(6).toISOString(), ...CONSOLE_CALLER,
    group_id: GROUPS[0].id, group_name: GROUPS[0].name, account_id: 12, account_name: 'claude-main', plugin_key: 'anthropic', account_type: 'apikey',
    platform: 'anthropic', protocol: 'anthropic.messages', upstream_protocol: 'anthropic.messages', conversion: null, endpoint: '/v1/messages',
    model: chat.model, upstream_model: chat.model, stream: true, status_code: 200, success: true, error_type: '', error_message: '',
    input_tokens: 12000, output_tokens: 1800, cache_read_tokens: 0, cache_creation_tokens: 0, cache_creation_1h_tokens: 0,
    total_cost: money(0.096), rate_multiplier: '1.00', billing_status: 'billed', billing_mode: 'expression', matched_tier: '',
    price_id: 2, expr_hash: remember(chat.expression), latency_ms: 18400, first_token_ms: 900, upload_ms: 40,
    sticky_rule: '', sticky_hit: false, hook_decisions: [],
    _detail: advisorDetail({ model: 'claude-opus-4-1', expression: 'p*15 + c*75' }),
    _price: null
  }
}

// Retried requests (attempt_trail, CONTRACTS §98), among the console user's
// recent anthropic / relay requests: switched once, the same account twice
// and then another, failed on every account, a chain long enough to fold,
// and a row from before the trail (only the count). The trail is stored as
// the list sends it, by account ID; the detail adds the names.
type Step = Record<string, any>
const upstream = (account_id: number, status: number, ms: number, message: string, next?: string): Step =>
  ({ account_id, status, error_type: 'upstream_error', ms, message, ...(next ? { next } : {}) })
const OVERLOADED = 'overloaded_error: Overloaded'
const RESET = 'upstream connect error or disconnect/reset before headers'
const TRAILS: Array<Step[] | null> = [
  [upstream(12, 529, 1180, OVERLOADED, 'switch'), { account_id: 13, status: 200, ms: 3040 }],
  [upstream(16, 502, 640, RESET, 'same'), upstream(16, 502, 702, RESET, 'switch'), { account_id: 12, status: 200, ms: 2950 }],
  [upstream(12, 529, 1210, OVERLOADED, 'switch'), upstream(13, 529, 980, OVERLOADED, 'switch'), upstream(16, 503, 1530, 'service unavailable')],
  [12, 13, 16, 13].map((id, i) => upstream(id, 529, 900 + i * 110, OVERLOADED, 'switch')).concat({ account_id: 12, status: 200, ms: 2710 }),
  null
]

/** A record that took the attempts of a trail: on the account of the last, failed when the last failed. */
function withTrail(u: any, steps: Step[]) {
  const last = steps[steps.length - 1]
  const account = ACCOUNTS.find((a) => a.id === last.account_id)!
  Object.assign(u, { attempts: steps.length, attempt_trail: { v: 1, steps, omitted: 0 }, account_id: account.id, account_name: account.name, plugin_key: account.plugin_key, account_type: account.type })
  if (last.status !== 200) {
    Object.assign(u, {
      success: false, status_code: last.status, error_type: 'upstream_error', error_message: last.message, input_tokens: 0, output_tokens: 0,
      cache_read_tokens: 0, cache_creation_tokens: 0, total_cost: '0', billing_status: 'free', matched_tier: '', first_token_ms: undefined, _detail: undefined, _price: null
    })
  }
  u.latency_ms = steps.reduce((n, x) => n + x.ms, 0)
}

/** Gives TRAILS to the console user's newest random anthropic / relay records (rows newest first). */
export function addTrails(rows: any[]) {
  const picked = rows.filter((u) => u.user_id === 1 && u.id >= 5000 && ['anthropic', 'relay'].includes(u.plugin_key)).slice(0, TRAILS.length)
  picked.forEach((u, k) => {
    const steps = TRAILS[k]
    // Within the last hour, so they open on the request log's default range.
    u.created_at = minutesAgo(3 + k * 9).toISOString()
    if (steps) withTrail(u, steps)
    else u.attempts = 2
  })
}
