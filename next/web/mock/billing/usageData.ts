// What the usage records of the mock are made of: the users, groups and
// accounts they name, how a call is billed at a price (charge) and the base
// cost a record keeps.
import { breakdownOf, tryEvaluate, type EvalInput } from './engine'
import type { MockPrice, PriceDef } from './priceStore'
import { money } from './shared'

export const USERS = [
  { id: 1, email: 'admin@example.com', name: 'Admin' },
  { id: 2, email: 'zhangsan@example.com', name: '张三' },
  { id: 3, email: 'lisi@example.com', name: '李四' }
]
export const GROUPS = [
  { id: 1, name: 'default', rate: 1 },
  { id: 2, name: 'vip', rate: 1.5 }
]
// Same accounts as mock/accounts.ts. Anthropic-platform accounts sometimes
// serve the openai chat endpoint through conversion to anthropic.messages;
// the built-in openai / gemini accounts serve their own platforms.
export const ACCOUNTS = [
  { id: 12, name: 'claude-main', plugin_key: 'anthropic', type: 'apikey', upstream: 'anthropic.messages' },
  { id: 13, name: 'claude-bak', plugin_key: 'anthropic', type: 'apikey', upstream: 'anthropic.messages' },
  { id: 16, name: 'relay-1', plugin_key: 'relay', type: 'relay_key', upstream: 'anthropic.messages' },
  { id: 19, name: 'openai-main', plugin_key: 'openai', type: 'apikey', upstream: 'openai.chat' },
  { id: 20, name: 'gemini-main', plugin_key: 'gemini', type: 'apikey', upstream: 'gemini.generate' }
]
// Client addresses per user: most calls come from one machine, some from a second.
export const CLIENT_IPS: Record<number, string[]> = { 1: ['203.0.113.10', '203.0.113.11'], 2: ['198.51.100.23', '2001:db8::5'], 3: ['192.0.2.44', '192.0.2.45'] }
export const MODELS = ['claude-sonnet-x', 'claude-sonnet-4-5', 'claude-haiku-4-5']
export const OWN_MODELS: Record<string, string[]> = { openai: ['gpt-4o', 'gpt-4o-mini'], gemini: ['gemini-2.5-flash'] }
/** Client endpoint per upstream protocol of the native accounts. */
export const NATIVE_ENDPOINT: Record<string, { platform: string; protocol: string; endpoint: string }> = {
  'openai.chat': { platform: 'openai', protocol: 'openai.chat', endpoint: '/v1/chat/completions' },
  'gemini.generate': { platform: 'gemini', protocol: 'gemini.generate', endpoint: '/v1beta/models/gemini-2.5-flash:generateContent' }
}

/**
 * How group pricing priced a record (billing_detail.inputs.pricing): the
 * segment of the user, where the rate comes from and the rule that gave the
 * list price, if one did. A record at the global price and the group's
 * default rate has none.
 */
export interface PricingTerms {
  segment_id: number
  rate_source: 'segment_model' | 'segment' | 'group_model' | 'group'
  /** A rule of the group: a multiplier of the global price, or a price of its own. */
  rule?: { source: 'group'; scale?: number; custom?: PriceDef }
}

/** The pricing input of a record priced by group pricing. */
function pricingInput(price: MockPrice, terms: PricingTerms) {
  const { source, scale, custom } = terms.rule ?? {}
  return {
    segment_id: terms.segment_id,
    rate_source: terms.rate_source,
    ...(source ? { price_source: source } : {}),
    ...(scale === undefined ? {} : { scale: String(scale) }),
    ...(custom ? { base: { expression: price.expression, expr_hash: price.expr_hash } } : {})
  }
}

/**
 * Bills one call at a global price, as usage_logs keeps it; null when the
 * price cannot be evaluated.
 *
 *   paid = list cost × rate
 *
 * A multiplier rule scales the cost at the global price and leaves the
 * breakdown alone; a custom rule is evaluated in its place, and `cost` stays
 * what the global price would have charged.
 */
export function charge(price: MockPrice, input: EvalInput, rate: number, terms?: PricingTerms) {
  const { source, scale, custom } = terms?.rule ?? {}
  const global = tryEvaluate(price.expression, input)
  const ev = custom ? tryEvaluate(custom.expression, input) : global
  if (!ev || !global) return null
  const listCost = ev.cost * (scale ?? 1)
  const total = listCost * rate
  const detail = {
    tier: ev.tier,
    rules: ev.rules,
    breakdown: breakdownOf(ev),
    cost: money(global.cost),
    ...(source ? { list_cost: money(listCost) } : {}),
    rate_multiplier: rate.toFixed(2),
    total_cost: money(total),
    expr_version: 1,
    ...(terms ? { inputs: { pricing: pricingInput(price, terms) } } : {})
  }
  return { status: 'billed', total, tier: ev.tier, detail }
}

/**
 * The base cost of a record (usage_logs.base_cost, CONTRACTS §97.5): the
 * global price times the usage, before any rule and any rate. It is the
 * billing_detail.cost a record keeps; one without a detail falls back to
 * total_cost / rate_multiplier, as rows from before 0096 do (0 at a rate of 0).
 * A record priced by a rule shows why it matters: there the fallback would be
 * its list cost.
 */
export function baseCost(u: any): number {
  if (u._detail?.cost !== undefined) return Number(u._detail.cost)
  return Number(u.rate_multiplier) > 0 ? Number(u.total_cost) / Number(u.rate_multiplier) : 0
}
