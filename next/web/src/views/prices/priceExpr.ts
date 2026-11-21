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

/** Go's printable runes (unicode.IsPrint): letters, marks, numbers, punctuation, symbols and the ASCII space. */
const PRINTABLE = /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]$/u
const ESCAPES: Record<string, string> = {
  '"': '\\"',
  '\\': '\\\\',
  '\x07': '\\a',
  '\b': '\\b',
  '\f': '\\f',
  '\n': '\\n',
  '\r': '\\r',
  '\t': '\\t',
  '\v': '\\v'
}
const hex = (n: number, width: number) => n.toString(16).padStart(width, '0')

/** A string literal as Go's strconv.Quote writes it; a lone surrogate reads U+FFFD, as Go's JSON decoding makes it. */
export function goQuote(s: string): string {
  let out = ''
  for (const ch of s) {
    const r = ch.codePointAt(0)!
    if (ESCAPES[ch]) out += ESCAPES[ch]
    else if (r >= 0xd800 && r <= 0xdfff) out += '\ufffd'
    else if (PRINTABLE.test(ch)) out += ch
    else if (r < 0x20 || r === 0x7f) out += `\\x${hex(r, 2)}`
    else out += r < 0x10000 ? `\\u${hex(r, 4)}` : `\\U${hex(r, 8)}`
  }
  return `"${out}"`
}

/** Go's strings.TrimSpace: Unicode White_Space, which JS's trim() is not (it trims U+FEFF, keeps U+0085). */
const goTrim = (s: string) =>
  s.replace(
    /^[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g,
    ''
  )

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

/** The prices a token tier lists: p and c always, the others when set. */
export function pricedKeys(t: Partial<Record<PriceKey, unknown>>): PriceKey[] {
  return PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(t[k]))
}

export function tokenPricesOf(t: TokenTier | undefined): TokenPrices {
  return Object.fromEntries(TOKEN_VARS.map(k => [k, isSet(t?.[k]) ? num(t?.[k]) : null])) as TokenPrices
}

/** A config of one token tier and no rules: what a custom price rule holds. */
export function singleTier(prices: Partial<Record<PriceKey, number | null>>): TokenConfig {
  return {
    tiers: [{ name: 'base', max_len: null, unit: 'token', ...Object.fromEntries(pricedKeys(prices).map(k => [k, prices[k] ?? null])) }],
    rules: []
  }
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

const orNull = (v: unknown) => (isSet(v) ? num(v) : null)

function compactTier(t: TokenTier): TokenTier {
  const head = { name: t.name, max_len: orNull(t.max_len) }
  // A blank per-request price stays blank: checkTokenConfig and the server refuse it.
  if (t.unit === 'request') return { ...head, unit: 'request', price: orNull(t.price) }
  return { ...head, unit: 'token', ...Object.fromEntries(pricedKeys(t).map(k => [k, num(t[k])])) }
}

const NUMBER = /^-?\d+(\.\d+)?$/

/**
 * A condition value as typed, as the config holds it: a header and `contains`
 * compare text; any other compares a number, true / false or text, as it
 * reads ("…" in double quotes is text however it reads).
 */
export function typedValue(c: Pick<FieldCond, 'source' | 'op'>, text: string): CondValue {
  const s = text.trim()
  if (c.source === 'header' || c.op === 'contains') return text
  if (NUMBER.test(s)) return Number(s)
  if (s === 'true' || s === 'false') return s === 'true'
  return /^".*"$/.test(s) ? s.slice(1, -1) : text
}

/** The text that types back to a saved value (typedValue). */
export function valueText(c: Pick<FieldCond, 'source' | 'op'>, v: CondValue | undefined): string {
  const text = String(v ?? '')
  return typeof v === 'string' && typedValue(c, text) !== v ? `"${v}"` : text
}

function compactCond(c: RuleCond): RuleCond {
  if (c.source === 'time') return { source: 'time', tz: c.tz, from_hour: c.from_hour, to_hour: c.to_hour }
  const { source, key, op, value } = c
  if (op === 'exists') return { source, key, op }
  return { source, key, op, value: typeof value === 'string' ? typedValue(c, value) : (value ?? '') }
}

/** The config the editor sends: unset prices and editor leftovers dropped, condition values typed (typedValue). */
export function compactTokenConfig(cfg: TokenConfig): TokenConfig {
  return {
    tiers: cfg.tiers.map(compactTier),
    rules: cfg.rules.map(r => ({ name: r.name, multiplier: orNull(r.multiplier), conds: r.conds.map(compactCond) }))
  }
}

// ------------------------------------------------------------------ generation

const tierCall = (name: string, body: string) => `tier(${goQuote(name)}, ${body})`

function tierBody(t: TokenTier): string {
  if (t.unit === 'request') return `flat(${fmt(num(t.price))})`
  return pricedKeys(t)
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
  return goQuote(String(v ?? ''))
}

export function condExpr(c: RuleCond): string {
  if (c.source === 'time') {
    const h = `hour(${goQuote(goTrim(c.tz ?? '') || 'UTC')})`
    const [a, b] = [Math.trunc(num(c.from_hour)), Math.trunc(num(c.to_hour))]
    return a < b ? `${h} >= ${a} && ${h} < ${b}` : `(${h} >= ${a} || ${h} < ${b})`
  }
  const header = c.source === 'header'
  const read = `${READ[c.source]}(${goQuote(header ? goTrim(c.key).toLowerCase() : goTrim(c.key))})`
  if (c.op === 'exists') return `${read} != ${ABSENT[c.source]}`
  if (c.op === 'contains') return `has(${read}, ${goQuote(String(c.value ?? ''))})`
  // A header is text and compares with a string (checkTokenConfig refuses any other); an absent parameter compares with a number as 0.
  if (header) return `${read} ${CMP[c.op]} ${goQuote(String(c.value ?? ''))}`
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

export interface LocalIssue {
  key: string
  params?: Record<string, unknown>
}

const TIER_NAME = /^[A-Za-z0-9_.-]{1,64}$/
/** As the server's headerNameRe, of the trimmed lower-case key. */
const HEADER_NAME = /^[A-Za-z0-9!#$%&'*+.^_|~-]{1,100}$/

/** Whether a price is set and not negative. */
const isPrice = (v: unknown) => isSet(v) && num(v) >= 0
const isText = (v: unknown) => typeof v === 'string' && v !== ''

function validTz(tz: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz })
    return true
  } catch {
    return false
  }
}

/** Why a condition of rule n (from 1) fails the server's generator, as the server checks it; null when it passes. */
function condIssue(c: RuleCond, n: number): LocalIssue | null {
  const field = { key: 'prices.local.ruleField', params: { n } }
  if (c.source === 'time') {
    const [a, b] = [c.from_hour, c.to_hour]
    if (!validTz(goTrim(c.tz ?? '') || 'UTC')) return { key: 'prices.local.ruleTz', params: { n } }
    const hours = Number.isInteger(a) && Number.isInteger(b) && a >= 0 && a <= 23 && b >= 0 && b <= 24 && a !== b
    return hours ? null : field
  }
  const key = goTrim(c.key)
  if (c.source === 'header' && !HEADER_NAME.test(key.toLowerCase())) return { key: 'prices.local.ruleHeader', params: { n } }
  if (c.source !== 'header' && (!key || new TextEncoder().encode(key).length > 200)) return field
  if (c.op === 'exists') return null
  if (NUMERIC_OPS.includes(c.op)) return c.source !== 'header' && typeof c.value === 'number' ? null : field
  if (c.op === 'contains' || c.source === 'header') return isText(c.value) ? null : field
  return c.value === '' || c.value === undefined || c.value === null ? field : null
}

/** Problems of a per_request price that block saving. */
export function checkPerRequest(price: unknown): LocalIssue[] {
  return isPrice(price) ? [] : [{ key: 'prices.local.price' }]
}

/**
 * Problems of a per_token config as saved (compactTokenConfig), with its tiers
 * in the order they match, that block saving (i18n keys under prices.local).
 * They mirror the server's generator, and refuse more: a tier's max is above
 * the one before it, and a value a condition compares is never blank.
 */
export function checkTokenConfig(cfg: TokenConfig): LocalIssue[] {
  const out: LocalIssue[] = []
  if (!cfg.tiers.length) out.push({ key: 'prices.local.noTier' })
  const names = new Set<string>()
  let prev = 0
  cfg.tiers.forEach((t, i) => {
    const name = t.name || i + 1
    if (!TIER_NAME.test(t.name)) out.push({ key: 'prices.local.tierName', params: { n: i + 1 } })
    else if (names.has(t.name)) out.push({ key: 'prices.local.tierDup', params: { name } })
    names.add(t.name)
    if (t.unit === 'request' && !isSet(t.price)) out.push({ key: 'prices.local.tierPrice', params: { name } })
    else if ([t.price, ...PRICE_KEYS.map(k => t[k])].some(v => isSet(v) && num(v) < 0))
      out.push({ key: 'prices.local.negative', params: { name } })
    if (i === cfg.tiers.length - 1) {
      if (isSet(t.max_len)) out.push({ key: 'prices.local.tierRest' })
      return
    }
    if (!(num(t.max_len) > 0)) out.push({ key: 'prices.local.tierMax', params: { name } })
    else if (num(t.max_len) <= prev) out.push({ key: 'prices.local.tierOrder', params: { name } })
    prev = num(t.max_len)
  })
  cfg.rules.forEach((r, i) => {
    const n = i + 1
    const issue =
      !isPrice(r.multiplier) || !r.conds.length
        ? { key: 'prices.local.ruleField', params: { n } }
        : r.conds.map(c => condIssue(c, n)).find(Boolean)
    if (issue) out.push(issue)
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
