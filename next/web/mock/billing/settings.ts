// Mock handlers of the billing, sticky, gateway and scheduling settings.
import { fail, on } from '../router'
import type { FieldError } from './shared'

export const billingSettings = {
  pre_consume_tokens: 500,
  min_balance: '0',
  big_cost_warning_usd: '10',
  client_disconnect_policy: 'continue'
}
const stickySettings = { enabled: true, default_ttl_seconds: 3600, keep_on_account_disabled: false }
const DISCONNECT_POLICIES = ['precharge', 'continue', 'free']

/** Whether Go would bind the client disconnect policy of the body: a string. */
const disconnectBinds = (b: any) => ['string', 'undefined'].includes(typeof b.client_disconnect_policy)

/** The fields of the client disconnect policy (CONTRACTS §25.8) the body gets wrong; one it leaves out keeps its value. */
function disconnectFields(b: any) {
  const fields: FieldError[] = []
  if (b.client_disconnect_policy !== undefined && !DISCONNECT_POLICIES.includes(b.client_disconnect_policy)) {
    fields.push({ field: 'client_disconnect_policy', code: 'invalid', message: 'must be precharge, continue or free' })
  }
  return fields
}

on('GET', '/settings/billing', () => billingSettings)
on('PUT', '/settings/billing', (req) => {
  const b = { ...(req.body || {}) }
  // A null pointer field binds as left out, as in Go.
  if (b.client_disconnect_policy === null) delete b.client_disconnect_policy
  if (!disconnectBinds(b)) return fail(400, 'invalid_argument', 'invalid request body')
  if (!Number.isInteger(b.pre_consume_tokens) || b.pre_consume_tokens < 0 || b.pre_consume_tokens > 100000000) {
    return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'pre_consume_tokens', code: 'invalid', message: 'must be an integer from 0 to 100000000' }] })
  }
  const fields = disconnectFields(b)
  if (fields.length) return fail(400, 'invalid_argument', 'invalid', { fields })
  Object.assign(billingSettings, { pre_consume_tokens: b.pre_consume_tokens, min_balance: String(b.min_balance), big_cost_warning_usd: String(b.big_cost_warning_usd) })
  if (b.client_disconnect_policy !== undefined) billingSettings.client_disconnect_policy = b.client_disconnect_policy
  return billingSettings
})
on('GET', '/settings/sticky', () => stickySettings)
on('PUT', '/settings/sticky', (req) => {
  const b = req.body || {}
  Object.assign(stickySettings, { enabled: !!b.enabled, default_ttl_seconds: Number(b.default_ttl_seconds) || 3600, keep_on_account_disabled: !!b.keep_on_account_disabled })
  return stickySettings
})

// Gateway settings (CONTRACTS §8, §14.4): ranges 1–10, 100–30000, 50–2000;
// claude_max_tokens (CONVERT-MATRIX §6.1) is replaced as a whole.
const gatewaySettings = {
  max_attempts: 3,
  platform_call_timeout_ms: 2000,
  default_hook_timeout_ms: 300,
  platform_hotpath_timeout_ms: 300,
  max_request_body_mb: 128,
  claude_max_tokens: { enabled: false, models: { default: 8192 } as Record<string, number> }
}
const GATEWAY_RANGES: Record<Exclude<keyof typeof gatewaySettings, 'claude_max_tokens'>, [number, number]> = {
  max_attempts: [1, 10],
  platform_call_timeout_ms: [100, 30000],
  default_hook_timeout_ms: [50, 2000],
  platform_hotpath_timeout_ms: [50, 2000],
  max_request_body_mb: [1, 1024]
}

const CLAUDE_MAX_TOKENS = { models: 100, nameLength: 200, max: 1048576, overrideBytes: 64 << 10 }
const isObject = (v: unknown) => typeof v === 'object' && v !== null && !Array.isArray(v)

/** Whether Go would bind claude_max_tokens: left out or null, or an object with an optional boolean and whole numbers. */
const claudeMaxTokensBinds = (r: any) =>
  r == null ||
  (isObject(r) &&
    (r.enabled == null || typeof r.enabled === 'boolean') &&
    (r.models == null || (isObject(r.models) && Object.values(r.models).every(Number.isInteger))))

/** The size of the override the server compiles the models to (gateway/claude_max_tokens.go), in UTF-8 bytes. */
function claudeMaxTokensOverrideBytes(models: Record<string, number>) {
  const set = (value: number, conditions?: object[]) => ({
    mode: 'set',
    path: 'max_tokens',
    value,
    keep_origin: true,
    ...(conditions && { conditions })
  })
  const ops = Object.keys(models)
    .filter(m => m !== 'default')
    .sort()
    .map(m => set(models[m], [{ path: 'original_model', mode: 'full', value: m }]))
  if ('default' in models) ops.push(set(models.default))
  return new TextEncoder().encode(JSON.stringify({ operations: ops })).length
}

/** The first problem of the rule's models, as the server's field error on claude_max_tokens. */
function claudeMaxTokensError(models: Record<string, number>) {
  const { models: most, nameLength, max, overrideBytes } = CLAUDE_MAX_TOKENS
  const err = (code: string, message: string) => ({ field: 'claude_max_tokens', code, message })
  const entries = Object.entries(models)
  if (entries.length > most) return err('too_many', `at most ${most} models`)
  for (const [k, v] of entries) {
    if (!k || k.trim() !== k || [...k].length > nameLength)
      return err('invalid_model', `model names must be 1-${nameLength} characters without surrounding spaces`)
    if (v < 1 || v > max) return err('out_of_range', `values must be between 1 and ${max}`)
  }
  if (claudeMaxTokensOverrideBytes(models) > overrideBytes) return err('too_large', 'the model names are too long in total')
}
on('GET', '/settings/gateway', () => gatewaySettings)
on('PUT', '/settings/gateway', (req) => {
  const b = req.body || {}
  if (!claudeMaxTokensBinds(b.claude_max_tokens)) return fail(400, 'invalid_argument', 'invalid request body')
  const fields: FieldError[] = []
  for (const [k, [min, max]] of Object.entries(GATEWAY_RANGES)) {
    const v = b[k]
    if (typeof v !== 'number' || !Number.isInteger(v) || v < min || v > max) {
      fields.push({ field: k, code: 'out_of_range', message: `${k} must be an integer between ${min} and ${max}` })
    }
  }
  // Replaced as a whole when given; models left out read back as the suggested default, {} stays empty.
  const rule = b.claude_max_tokens && {
    enabled: !!b.claude_max_tokens.enabled,
    models: { ...(b.claude_max_tokens.models ?? { default: 8192 }) }
  }
  const ruleError = rule && claudeMaxTokensError(rule.models)
  if (ruleError) fields.push(ruleError)
  if (fields.length) return fail(400, 'invalid_argument', 'invalid gateway settings', { fields })
  Object.assign(gatewaySettings, {
    max_attempts: b.max_attempts,
    platform_call_timeout_ms: b.platform_call_timeout_ms,
    default_hook_timeout_ms: b.default_hook_timeout_ms,
    platform_hotpath_timeout_ms: b.platform_hotpath_timeout_ms,
    max_request_body_mb: b.max_request_body_mb
  })
  if (rule) gatewaySettings.claude_max_tokens = rule
  return gatewaySettings
})

// Core load balancing (CONTRACTS §89): the row is replaced, absent fields take the defaults; mode is always traffic.
const SCHEDULING_DEFAULTS = { metric: 'sessions', limit_load: false, model_limits: false }
const schedulingSettings = { ...SCHEDULING_DEFAULTS }
on('GET', '/settings/scheduling', () => schedulingSettings)
on('PUT', '/settings/scheduling', req => {
  // Like Go's BindJSON into the defaults: null keeps the default, a non-boolean flag is a bind error.
  const body = Object.fromEntries(Object.entries(req.body || {}).filter(([, v]) => v != null))
  if (['limit_load', 'model_limits'].some(k => k in body && typeof body[k] !== 'boolean')) return fail(400, 'invalid_argument', 'invalid request body')
  const b = { ...SCHEDULING_DEFAULTS, ...body }
  const fields: FieldError[] = []
  if (!['rpm', 'concurrency', 'tpm', 'sessions'].includes(b.metric)) fields.push({ field: 'metric', code: 'invalid', message: 'invalid value' })
  if (fields.length) return fail(400, 'invalid_argument', 'invalid scheduling settings', { fields })
  return Object.assign(schedulingSettings, { metric: b.metric, limit_load: b.limit_load, model_limits: b.model_limits })
})
