// Price configs and the expressions generated from them (design
// 2026-11-21 §2). The generator matches the server's (billing/expr) byte for
// byte: the console shows what the server will store. Pure TS without imports.

export type PriceMode = 'per_request' | 'per_token' | 'expression'

/** The token variables a price table shows. */
export const TOKEN_VARS = ['p', 'c', 'cr', 'cc', 'cc1h'] as const
export type TokenVar = (typeof TOKEN_VARS)[number]

/** USD per 1M tokens; null = not priced. */
export type TokenPrices = Record<TokenVar, number | null>

/** Every price of a token tier, in the order the expression lists them; p and c are always emitted. */
export const PRICE_KEYS = ['p', 'c', 'cr', 'cc', 'cc1h', 'img', 'img_o'] as const
export type PriceKey = (typeof PRICE_KEYS)[number]

export type TierUnit = 'token' | 'request'

/** One tier of a per_token price. A key left out (or null) is not priced; a request tier reads `price` only. */
export interface TokenTier extends Partial<Record<PriceKey, number | null>> {
  name: string
  /** Upper bound of len, inclusive; null = the rest. */
  max_len: number | null
  unit?: TierUnit
  price?: number | null
}

export type CondSource = 'param' | 'header' | 'usage' | 'time'
export type CondOp = 'eq' | 'ne' | 'contains' | 'exists' | 'gt' | 'gte' | 'lt' | 'lte'
export type CondValue = string | number | boolean

export interface FieldCond {
  source: Exclude<CondSource, 'time'>
  key: string
  op: CondOp
  /** Absent for exists. */
  value?: CondValue
}

export interface TimeCond {
  source: 'time'
  tz: string
  from_hour: number
  to_hour: number
}

export type RuleCond = FieldCond | TimeCond

/** A surcharge: the multiplier applies when every condition holds. */
export interface MarkupRule {
  name: string
  multiplier: number | null
  conds: RuleCond[]
}

export interface TokenConfig {
  tiers: TokenTier[]
  rules: MarkupRule[]
}

// ------------------------------------------------------------------ helpers

export function num(v: unknown, fallback = 0): number {
  if (v === null || v === undefined || v === '') return fallback
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : fallback
}

export function isSet(v: unknown): boolean {
  return v !== null && v !== undefined && v !== '' && Number.isFinite(Number(v))
}

/** The shortest decimal without exponent, as Go's strconv.FormatFloat(f, 'f', -1, 64). */
export function fmt(n: number): string {
  const s = String(n)
  const m = /^(-?)(\d)(?:\.(\d+))?e([+-]\d+)$/.exec(s)
  if (!m) return s
  const [, sign, int, frac = '', e] = m
  const exp = Number(e)
  if (exp < 0) return `${sign}0.${'0'.repeat(-exp - 1)}${int}${frac}`
  return sign + int + frac + '0'.repeat(exp - frac.length)
}

const quote = (s: string) => JSON.stringify(s)

/** A per_token config of whatever came from the server, with both lists. */
export function asTokenConfig(raw: unknown): TokenConfig {
  const c = (raw ?? {}) as Partial<TokenConfig>
  return { tiers: Array.isArray(c.tiers) ? c.tiers : [], rules: Array.isArray(c.rules) ? c.rules : [] }
}

/** Tiers in the order they match: bounded ones by max_len, then the rest. */
export function sortedTiers(tiers: readonly TokenTier[]): TokenTier[] {
  const rank = (t: TokenTier) => (isSet(t.max_len) ? num(t.max_len) : Infinity)
  return [...tiers].sort((a, b) => (rank(a) === rank(b) ? 0 : rank(a) - rank(b)))
}

export function tokenPricesOf(t: TokenTier | undefined): TokenPrices {
  return Object.fromEntries(TOKEN_VARS.map(k => [k, isSet(t?.[k]) ? num(t?.[k]) : null])) as TokenPrices
}

/** A config of one token tier and no rules: what a custom price rule holds. */
export function singleTier(prices: Partial<Record<PriceKey, number | null>>): TokenConfig {
  const set = PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(prices[k]))
  return { tiers: [{ name: 'base', max_len: null, unit: 'token', ...Object.fromEntries(set.map(k => [k, prices[k] ?? null])) }], rules: [] }
}

/** The tier of a config with one token tier and no rules; null for any other. */
export function simpleTier(config: unknown): TokenTier | null {
  const c = asTokenConfig(config)
  const t = c.tiers[0]
  return c.tiers.length === 1 && !c.rules.length && t.unit !== 'request' ? t : null
}

/** Whether a price takes a custom price of a group or user group: a per-request one, or a per-token one of one token tier and no rules. */
export function takesCustomPrice(mode: PriceMode, config: unknown): boolean {
  return mode === 'per_request' || (mode === 'per_token' && simpleTier(config) !== null)
}

// ------------------------------------------------------------------ config as saved

function compactTier(t: TokenTier): TokenTier {
  const head = { name: t.name, max_len: isSet(t.max_len) ? num(t.max_len) : null }
  if (t.unit === 'request') return { ...head, unit: 'request', price: num(t.price) }
  const prices = PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(t[k])).map(k => [k, num(t[k])])
  return { ...head, unit: 'token', ...Object.fromEntries(prices) }
}

function compactCond(c: RuleCond): RuleCond {
  if (c.source === 'time') return { source: 'time', tz: c.tz, from_hour: c.from_hour, to_hour: c.to_hour }
  const { source, key, op, value } = c
  return op === 'exists' ? { source, key, op } : { source, key, op, value: value ?? '' }
}

/** The config the editor sends: unset prices and editor leftovers dropped. */
export function compactTokenConfig(cfg: TokenConfig): TokenConfig {
  return {
    tiers: cfg.tiers.map(compactTier),
    rules: cfg.rules.map(r => ({ name: r.name, multiplier: num(r.multiplier), conds: r.conds.map(compactCond) }))
  }
}

// ------------------------------------------------------------------ generation

const tierCall = (name: string, body: string) => `tier(${quote(name)}, ${body})`

function tierBody(t: TokenTier): string {
  if (t.unit === 'request') return `flat(${fmt(num(t.price))})`
  return PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(t[k]))
    .map(k => `${k}*${fmt(num(t[k]))}`)
    .join(' + ')
}

const READ: Record<FieldCond['source'], string> = { param: 'param', header: 'header', usage: 'u' }
/** What a source reads when the value is not there: an absent usage field reads 0. */
const ABSENT: Record<FieldCond['source'], string> = { param: 'nil', header: '""', usage: '0' }
const CMP: Record<string, string> = { eq: '==', ne: '!=', gt: '>', gte: '>=', lt: '<', lte: '<=' }
export const NUMERIC_OPS: readonly CondOp[] = ['gt', 'gte', 'lt', 'lte']

function literal(v: CondValue | undefined): string {
  if (typeof v === 'number') return fmt(v)
  if (typeof v === 'boolean') return String(v)
  return quote(String(v ?? ''))
}

export function condExpr(c: RuleCond): string {
  if (c.source === 'time') {
    const h = `hour(${quote(c.tz.trim() || 'UTC')})`
    const [a, b] = [Math.trunc(num(c.from_hour)), Math.trunc(num(c.to_hour))]
    return a < b ? `${h} >= ${a} && ${h} < ${b}` : `(${h} >= ${a} || ${h} < ${b})`
  }
  const header = c.source === 'header'
  const read = `${READ[c.source]}(${quote(header ? c.key.trim().toLowerCase() : c.key.trim())})`
  if (c.op === 'exists') return `${read} != ${ABSENT[c.source]}`
  if (c.op === 'contains') return `has(${read}, ${quote(String(c.value ?? ''))})`
  // A header is text and compares with a string; an absent parameter compares with a number as 0.
  if (header) return `${read} ${CMP[c.op]} ${quote(String(c.value ?? ''))}`
  const lhs = c.source === 'param' && NUMERIC_OPS.includes(c.op) ? `(${read} ?? 0)` : read
  return `${lhs} ${CMP[c.op]} ${literal(c.value)}`
}

const ruleExpr = (r: MarkupRule) => `(${r.conds.map(condExpr).join(' && ')} ? ${fmt(num(r.multiplier))} : 1)`

/**
 * The expression of a per_token config: `len <= a ? A : (len <= b ? B : C)`,
 * and with rules `(base) * (cond ? m : 1) * …`. '' without tiers.
 */
export function tokenExpr(cfg: TokenConfig): string {
  const [last, ...rest] = sortedTiers(cfg.tiers).reverse()
  if (!last) return ''
  const base = rest.reduce(
    (acc, t, i) => `len <= ${fmt(num(t.max_len))} ? ${tierCall(t.name, tierBody(t))} : ${i > 0 ? `(${acc})` : acc}`,
    tierCall(last.name, tierBody(last))
  )
  return cfg.rules.length ? [`(${base})`, ...cfg.rules.map(ruleExpr)].join(' * ') : base
}

export function perRequestExpr(price: unknown): string {
  return tierCall('base', `flat(${fmt(num(price))})`)
}

/** The generated expression of a per_request or per_token price; '' for an expression price, which has none. */
export function priceExpression(mode: PriceMode, config: unknown): string {
  if (mode === 'per_request') return perRequestExpr((config as { price?: unknown } | null)?.price)
  return mode === 'per_token' ? tokenExpr(asTokenConfig(config)) : ''
}

// ------------------------------------------------------------------ local checks

const TIER_NAME = /^[A-Za-z0-9_.-]{1,64}$/

function condComplete(c: RuleCond): boolean {
  if (c.source === 'time') return num(c.from_hour) !== num(c.to_hour)
  if (!c.key.trim()) return false
  if (c.op === 'contains') return String(c.value ?? '') !== ''
  if (!NUMERIC_OPS.includes(c.op)) return true
  return c.source !== 'header' && typeof c.value === 'number'
}

export interface LocalIssue {
  key: string
  params?: Record<string, unknown>
}

/** Problems of a per_token config (i18n keys under prices.local) that block saving. */
export function checkTokenConfig(cfg: TokenConfig): LocalIssue[] {
  const out: LocalIssue[] = []
  if (!cfg.tiers.length) out.push({ key: 'prices.local.noTier' })
  const names = new Set<string>()
  let prev = 0
  cfg.tiers.forEach((t, i) => {
    if (!TIER_NAME.test(t.name)) out.push({ key: 'prices.local.tierName', params: { n: i + 1 } })
    else if (names.has(t.name)) out.push({ key: 'prices.local.tierDup', params: { name: t.name } })
    names.add(t.name)
    if (i === cfg.tiers.length - 1) return
    if (!(num(t.max_len) > 0)) out.push({ key: 'prices.local.tierMax', params: { name: t.name || i + 1 } })
    else if (num(t.max_len) <= prev) out.push({ key: 'prices.local.tierOrder', params: { name: t.name || i + 1 } })
    prev = num(t.max_len)
  })
  cfg.rules.forEach((r, i) => {
    if (!isSet(r.multiplier) || !r.conds.length || !r.conds.every(condComplete))
      out.push({ key: 'prices.local.ruleField', params: { n: i + 1 } })
  })
  return out
}

// ------------------------------------------------------------------ list summary

export type PriceSummary =
  | { kind: 'per_request'; price: number }
  /** The first tier, the number of tiers and of rules. */
  | { kind: 'per_token'; first: TokenTier; tiers: number; rules: number }
  | { kind: 'expression' }

export function summarize(mode: PriceMode, config: Record<string, any> | null | undefined): PriceSummary {
  if (mode === 'per_request') return { kind: 'per_request', price: num(config?.price) }
  const cfg = asTokenConfig(config)
  const [first] = sortedTiers(cfg.tiers)
  return mode === 'per_token' && first
    ? { kind: 'per_token', first, tiers: cfg.tiers.length, rules: cfg.rules.length }
    : { kind: 'expression' }
}
