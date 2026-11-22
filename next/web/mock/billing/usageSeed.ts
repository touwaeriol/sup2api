// The usage records of the mock: 90 seeded random calls of the last week and
// the samples of usageSamples.ts, newest first.
import { priceFor, type MockPrice } from './priceStore'
import { hashOf, money, rnd } from './shared'
import { ACCOUNTS, CLIENT_IPS, GROUPS, MODELS, NATIVE_ENDPOINT, OWN_MODELS, USERS, charge } from './usageData'
import { addTrails, advisorRow, availabilityRows, groupPricingRows } from './usageSamples'

/** What decides random record i: who called, where it went, what happened and the usage. */
function picksOf(i: number) {
  const r = (k: number) => rnd(i * 7 + k)
  const account = ACCOUNTS[Math.floor(r(3) * ACCOUNTS.length)]
  const own = OWN_MODELS[account.plugin_key]
  const blocked = r(6) < 0.06
  const failed = !blocked && r(7) < 0.08
  const success = !blocked && !failed
  return {
    i,
    r,
    user: USERS[Math.floor(r(1) * USERS.length)],
    group: GROUPS[Math.floor(r(2) * GROUPS.length)],
    account,
    model: own ? own[Math.floor(r(4) * own.length)] : MODELS[Math.floor(r(4) * MODELS.length)],
    at: new Date(Date.now() - Math.floor(r(5) * 7 * 86400000)),
    blocked,
    failed,
    success,
    usage: {
      p: success ? Math.floor(r(8) * 4000) + 200 : 0,
      c: success ? Math.floor(r(9) * 1500) + 50 : 0,
      cr: success && r(10) < 0.6 ? Math.floor(r(11) * 60000) : 0,
      cc: success && r(12) < 0.3 ? Math.floor(r(13) * 5000) : 0,
      cc1h: 0
    },
    // Client used the openai chat endpoint; anthropic-platform accounts serve it via conversion.
    viaOpenAI: account.plugin_key === 'anthropic' && rnd(i * 13 + 99) < 0.2,
    // The body's arrival (upload_ms) is part of the first token and the total.
    upload: 20 + Math.floor(r(22) * r(22) * 2500)
  }
}
type Picks = ReturnType<typeof picksOf>

/** Who sent a record. */
function callerOf({ i, r, user, group }: Picks) {
  return {
    request_id: 'req_' + hashOf('u' + i).slice(0, 16),
    // Client X-Request-Id (CONTRACTS §14.4): most SDK calls send one.
    client_request_id: r(20) < 0.7 ? `cli-${hashOf('c' + i).slice(0, 8)}-${hashOf('d' + i).slice(0, 4)}` : '',
    user_id: user.id,
    user_email: user.email,
    // The console user spreads over its first two keys (the API key list's usage column).
    api_key_id: user.id === 1 ? 1 + (i % 2) : user.id * 10,
    api_key_name: `${user.name}-key`,
    client_ip: CLIENT_IPS[user.id][r(21) < 0.75 ? 0 : 1],
    group_id: group.id,
    group_name: group.name
  }
}

/** The conversion record of a converted request (CONVERT-MATRIX §7); operators only. */
const conversionOf = (i: number) => ({
  v: 1,
  grade: i % 4 ? 'good' : 'fair',
  hops: ['openai.chat', 'anthropic.messages'],
  policy: 'allow',
  ...(i % 3 ? {} : { lost: { other: ['seed'], ...(i % 4 ? {} : { tool: ['tools[2]'] }) } })
})

/** Where a record went: plugin_key/account_type the account type, platform/protocol the client endpoint. */
function routeOf({ i, r, account, model, blocked, viaOpenAI }: Picks) {
  const native = NATIVE_ENDPOINT[account.upstream]
  return {
    account_id: blocked ? null : account.id,
    account_name: blocked ? null : account.name,
    plugin_key: blocked ? '' : account.plugin_key,
    account_type: blocked ? '' : account.type,
    platform: native?.platform ?? (viaOpenAI ? 'openai' : 'anthropic'),
    protocol: native?.protocol ?? (viaOpenAI ? 'openai.chat' : 'anthropic.messages'),
    upstream_protocol: blocked ? '' : account.upstream,
    conversion: viaOpenAI && !blocked ? conversionOf(i) : null,
    endpoint: native?.endpoint ?? (viaOpenAI ? '/v1/chat/completions' : '/v1/messages'),
    model,
    upstream_model: model,
    stream: r(16) < 0.7
  }
}

/** How a record ended, and the tokens it used. */
function outcomeOf({ blocked, failed, success, usage }: Picks) {
  return {
    status_code: success ? 200 : blocked ? 403 : 529,
    success,
    error_type: blocked ? 'blocked_by_hook' : failed ? 'upstream_overloaded' : '',
    error_message: blocked ? 'request rejected by guard' : failed ? 'Overloaded' : '',
    input_tokens: usage.p,
    output_tokens: usage.c,
    cache_read_tokens: usage.cr,
    cache_creation_tokens: usage.cc,
    cache_creation_1h_tokens: 0
  }
}

/** How a record was billed at its model's price; free when it failed or has no price. */
function billingOf({ r, group, success, usage, at }: Picks, price: MockPrice | null) {
  const fast = r(14) < 0.2
  const billing: any = (success && price && charge(price, { usage, headers: fast ? { 'anthropic-beta': 'fast-mode' } : {}, at }, group.rate)) || { status: 'free' }
  const billed = billing.status === 'billed'
  return {
    total_cost: money(billing.total || 0),
    rate_multiplier: group.rate.toFixed(2),
    billing_status: billing.status,
    billing_mode: price?.mode,
    matched_tier: billing.tier || '',
    price_id: billed ? price?.id : null,
    expr_hash: billed ? price?.expr_hash : '',
    _detail: billing.detail,
    _price: billed && price ? { id: price.id, model: price.model, source: price.source } : null
  }
}

/** The timing, stickiness and hook decisions of a record. */
function timingOf({ r, success, blocked, upload }: Picks) {
  return {
    latency_ms: success ? upload + 800 + Math.floor(r(17) * 4000) : 120,
    first_token_ms: success ? upload + 300 + Math.floor(r(18) * 900) : undefined,
    upload_ms: upload,
    sticky_rule: 'claude-code-session',
    sticky_hit: r(19) < 0.7,
    hook_decisions: [
      { plugin_key: 'guard', hook_id: 'content-filter', decision: blocked ? 'deny' : 'allow', latency_ms: 3 + Math.floor(r(15) * 20), note: blocked ? 'matched rule "no-secrets"' : undefined }
    ]
  }
}

function randomRow(i: number): any {
  const picks = picksOf(i)
  const price = priceFor(picks.model)
  return { id: 5000 + i, ...callerOf(picks), ...routeOf(picks), ...outcomeOf(picks), ...billingOf(picks, price), ...timingOf(picks), created_at: picks.at.toISOString() }
}

const newestFirst = (a: any, b: any) => b.created_at.localeCompare(a.created_at)

export const usageRows: any[] = [...Array.from({ length: 90 }, (_, i) => randomRow(i)), ...availabilityRows(), ...groupPricingRows(), advisorRow()].sort(newestFirst)
addTrails(usageRows)
usageRows.sort(newestFirst)
