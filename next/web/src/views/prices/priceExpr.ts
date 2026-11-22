// Price configs and the expressions generated from them (design
// 2026-11-21 §2; tier conditions, design 2026-11-22 §3). The generator
// matches the server's (billing/expr) byte for byte: the console shows what
// the server will store; the checks before saving are in priceCheck.ts. Pure
// TS without imports.

/** The billing modes, in the order the console lists them. */
export const PRICE_MODES = ['visual', 'expression'] as const
export type PriceMode = (typeof PRICE_MODES)[number]

/** The token variables a price table shows. */
export const TOKEN_VARS = ['p', 'c', 'cr', 'cc', 'cc1h'] as const
export type TokenVar = (typeof TOKEN_VARS)[number]

/** USD per 1M tokens; null = not priced. */
export type TokenPrices = Record<TokenVar, number | null>

/** Every price of a token tier, in the order the expression lists them; p and c are always emitted. */
export const PRICE_KEYS = ['p', 'c', 'cr', 'cc', 'cc1h', 'img', 'img_o'] as const
export type PriceKey = (typeof PRICE_KEYS)[number]

export type TierUnit = 'token' | 'request'

/** What a tier condition compares: the input length, input and output tokens, or a time in a zone (design 2026-11-22 §2). */
const TOKEN_COND_VARS = ['len', 'p', 'c'] as const
export const TIME_COND_VARS = ['hour', 'minute', 'weekday', 'month', 'day'] as const
export const COND_VARS = [...TOKEN_COND_VARS, ...TIME_COND_VARS] as const
export type CondVar = (typeof COND_VARS)[number]
export type TimeCondVar = (typeof TIME_COND_VARS)[number]
export const CMP_OPS = ['<', '<=', '>', '>=', '==', '!='] as const
export type CmpOp = (typeof CMP_OPS)[number]
/** The whole values a time compares with, inclusive (weekday 0 is Sunday). */
export const TIME_RANGES: Record<TimeCondVar, readonly [number, number]> = { hour: [0, 23], minute: [0, 59], weekday: [0, 6], month: [1, 12], day: [1, 31] }
/** The most levels and comparisons a tier's condition takes. */
export const MAX_COND_DEPTH = 5
export const MAX_COND_LEAVES = 20

/** A comparison: `var op value`, a time read in `tz` (UTC when blank). The value may be held as typed text. */
export interface CondLeaf {
  var: CondVar
  op: CmpOp
  value: number | string | null
  tz?: string
}
/** A tier condition: a comparison, all or any of at least one condition, or the negation of one. */
export type TierCond = CondLeaf | { and: TierCond[] } | { or: TierCond[] } | { not: TierCond }

/** One tier of a visual price. A key left out (or null) is not priced; a request tier reads `price` only. */
export interface VisualTier extends Partial<Record<PriceKey, number | null>> {
  name: string
  /** When the tier applies; every tier but the last has one, the last (the rest) has none. */
  when?: TierCond
  unit?: TierUnit
  price?: number | null
}

/** Where a condition reads, in the order the rule editor lists them. */
export const COND_SOURCES = ['param', 'header', 'usage', 'time'] as const
export type CondSource = (typeof COND_SOURCES)[number]
/** How a condition compares, in the order the rule editor lists them. */
export const COND_OPS = ['eq', 'ne', 'contains', 'exists', 'gt', 'gte', 'lt', 'lte'] as const
export type CondOp = (typeof COND_OPS)[number]
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

/** The config of a visual price: tiers matched in order (token or request priced) and surcharge rules. */
export interface VisualConfig {
  tiers: VisualTier[]
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
export const goTrim = (s: string) =>
  s.replace(
    /^[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g,
    ''
  )

/** A visual config of whatever came from the server, with both lists. */
export function asVisualConfig(raw: unknown): VisualConfig {
  const c = (raw ?? {}) as Partial<VisualConfig>
  return { tiers: Array.isArray(c.tiers) ? c.tiers : [], rules: Array.isArray(c.rules) ? c.rules : [] }
}

export const isTimeVar = (v: unknown): v is TimeCondVar => TIME_COND_VARS.includes(v as TimeCondVar)

/** Whether a time compares with a value: a whole number within its range (TIME_RANGES); a blank value is none. */
export function timeValueFits(v: TimeCondVar, value: unknown): boolean {
  const [lo, hi] = TIME_RANGES[v]
  const n = num(value, NaN)
  return Number.isInteger(n) && n >= lo && n <= hi
}

type CondKind = 'leaf' | 'and' | 'or' | 'not'
const LEAF_KEYS = new Set(['var', 'op', 'value', 'tz'])

/** The kind of a condition node, by its keys exactly; null for any other shape. */
export function condKind(n: unknown): CondKind | null {
  if (!n || typeof n !== 'object' || Array.isArray(n)) return null
  const keys = Object.keys(n)
  if (keys.length === 1 && (keys[0] === 'and' || keys[0] === 'or')) return Array.isArray((n as Record<string, unknown>)[keys[0]]) ? keys[0] : null
  if (keys.length === 1 && keys[0] === 'not') return 'not'
  const leaf = keys.every(k => LEAF_KEYS.has(k)) && ['var', 'op', 'value'].every(k => keys.includes(k))
  return leaf ? 'leaf' : null
}

/** The children of a condition node: a group's, a negation's one, none of a comparison. */
export function condChildren(n: TierCond): TierCond[] {
  if ('and' in n) return n.and
  if ('or' in n) return n.or
  return 'not' in n ? [n.not] : []
}

/** The levels of a condition: 1 for a comparison. */
export const condDepth = (n: TierCond): number => 1 + Math.max(0, ...condChildren(n).map(condDepth))
/** The comparisons of a condition. */
export const condLeaves = (n: TierCond): number => (condKind(n) === 'leaf' ? 1 : condChildren(n).reduce((sum, c) => sum + condLeaves(c), 0))

/** The prices a token tier lists: p and c always, the others when set. */
export function pricedKeys(t: Partial<Record<PriceKey, unknown>>): PriceKey[] {
  return PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(t[k]))
}

export function tokenPricesOf(t: VisualTier | undefined): TokenPrices {
  return Object.fromEntries(TOKEN_VARS.map(k => [k, isSet(t?.[k]) ? num(t?.[k]) : null])) as TokenPrices
}

/** A config of one token tier and no rules. */
export function singleTier(prices: Partial<Record<PriceKey, number | null>>): VisualConfig {
  return {
    tiers: [{ name: 'base', unit: 'token', ...Object.fromEntries(pricedKeys(prices).map(k => [k, prices[k] ?? null])) }],
    rules: []
  }
}

/** A config of one request tier and no rules. */
export function requestTier(price: number | null): VisualConfig {
  return { tiers: [{ name: 'base', unit: 'request', price }], rules: [] }
}

// ------------------------------------------------------------------ config as saved

const orNull = (v: unknown) => (isSet(v) ? num(v) : null)

/** A condition as saved: a comparison's value a number (blank stays null), a zone only on a time; a node of no known shape as it is. */
function compactWhen(n: TierCond): TierCond {
  const kind = condKind(n)
  if (kind === 'and' || kind === 'or') return { [kind]: condChildren(n).map(compactWhen) } as TierCond
  if (kind === 'not') return { not: compactWhen((n as { not: TierCond }).not) }
  if (kind !== 'leaf') return n
  const { var: v, op, value, tz } = n as CondLeaf
  const leaf: CondLeaf = { var: v, op, value: orNull(value) }
  return isTimeVar(v) && tz !== undefined ? { ...leaf, tz } : leaf
}

function compactTier(t: VisualTier): VisualTier {
  const head: VisualTier = t.when ? { name: t.name, when: compactWhen(t.when) } : { name: t.name }
  // A blank per-request price stays blank: checkVisualConfig and the server refuse it.
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
export function compactVisualConfig(cfg: VisualConfig): VisualConfig {
  const rules = cfg.rules.map(r => ({ name: r.name, multiplier: orNull(r.multiplier), conds: r.conds.map(compactCond) }))
  return { tiers: cfg.tiers.map(compactTier), rules }
}

// ------------------------------------------------------------------ generation

const tierCall = (name: string, body: string) => `tier(${goQuote(name)}, ${body})`

function tierBody(t: VisualTier): string {
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
  // A header is text and compares with a string (checkVisualConfig refuses any other); an absent parameter compares with a number as 0.
  if (header) return `${read} ${CMP[c.op]} ${goQuote(String(c.value ?? ''))}`
  const lhs = c.source === 'param' && NUMERIC_OPS.includes(c.op) ? `(${read} ?? 0)` : read
  return `${lhs} ${CMP[c.op]} ${literal(c.value)}`
}

/** A tier condition: `len > 200000`, `hour("UTC") >= 22`; a group of one is its child, a group of several in a group in parentheses, a negation `!(…)`. */
export function whenExpr(n: TierCond): string {
  if ('not' in n) return `!(${whenExpr(n.not)})`
  if ('and' in n || 'or' in n) {
    const kids = condChildren(n)
    if (kids.length === 1) return whenExpr(kids[0])
    return kids.map(k => (isCompound(k) ? `(${whenExpr(k)})` : whenExpr(k))).join('and' in n ? ' && ' : ' || ')
  }
  const lhs = isTimeVar(n.var) ? `${n.var}(${goQuote(n.tz || 'UTC')})` : n.var
  return `${lhs} ${n.op} ${fmt(num(n.value))}`
}

/** Whether a condition is written as a group of several (a group of one is written as its child). */
export function isCompound(n: TierCond): boolean {
  if (!('and' in n || 'or' in n)) return false
  const kids = condChildren(n)
  return kids.length > 1 || (kids.length === 1 && isCompound(kids[0]))
}

const ruleExpr = (r: MarkupRule) => `(${r.conds.map(condExpr).join(' && ')} ? ${fmt(num(r.multiplier))} : 1)`
/** base without rules, `(base) * (rule) * …` with them. */
const withRules = (base: string, rules: readonly MarkupRule[]) => (rules.length ? [`(${base})`, ...rules.map(ruleExpr)].join(' * ') : base)

/**
 * The expression of a visual config, its tiers in the order listed:
 * `a ? A : (b ? B : C)`, and with rules `(base) * (cond ? m : 1) * …`. ''
 * without tiers.
 */
export function visualExpr(cfg: VisualConfig): string {
  const [last, ...rest] = [...cfg.tiers].reverse()
  if (!last) return ''
  const base = rest.reduce(
    (acc, t, i) => `${t.when ? whenExpr(t.when) : ''} ? ${tierCall(t.name, tierBody(t))} : ${i > 0 ? `(${acc})` : acc}`,
    tierCall(last.name, tierBody(last))
  )
  return withRules(base, cfg.rules)
}

/** Whether a condition compares a token count (len, p, c) anywhere. */
const readsTokens = (n: TierCond): boolean =>
  condKind(n) === 'leaf' ? TOKEN_COND_VARS.includes((n as CondLeaf).var as (typeof TOKEN_COND_VARS)[number]) : condChildren(n).some(readsTokens)

/**
 * Whether a visual config is due per request, as the server tells it (its
 * expression reads no token variable, §131.1): every tier a request tier, no
 * tier condition comparing a token count. Rules read no token variable.
 */
export function perRequestOnly(cfg: VisualConfig): boolean {
  return cfg.tiers.length > 0 && cfg.tiers.every(t => t.unit === 'request' && !(t.when && readsTokens(t.when)))
}

/** The generated expression of a visual price; '' for an expression price, which has none. */
export function priceExpression(mode: PriceMode, config: unknown): string {
  return mode === 'visual' ? visualExpr(asVisualConfig(config)) : ''
}

// ------------------------------------------------------------------ list summary

export type PriceSummary =
  /**
   * The rest (the last tier, which applies when no tier before it matches:
   * the price a list shows), the number of tiers and of rules.
   */
  | { kind: 'visual'; rest: VisualTier; tiers: number; rules: number }
  | { kind: 'expression' }

export function summarize(mode: PriceMode, config: Record<string, any> | null | undefined): PriceSummary {
  const cfg = asVisualConfig(config)
  const rest = cfg.tiers.at(-1)
  return mode === 'visual' && rest ? { kind: 'visual', rest, tiers: cfg.tiers.length, rules: cfg.rules.length } : { kind: 'expression' }
}
