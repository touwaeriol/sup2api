// Price configs and the expressions generated from them (design
// 2026-11-21 §2). The generator matches the server's (billing/expr) byte for
// byte: the console shows what the server will store. Pure TS without imports.

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
export const TOKEN_COND_VARS = ['len', 'p', 'c'] as const
export const TIME_COND_VARS = ['hour', 'minute', 'weekday', 'month', 'day'] as const
export const COND_VARS = [...TOKEN_COND_VARS, ...TIME_COND_VARS] as const
export type CondVar = (typeof COND_VARS)[number]
export type TimeCondVar = (typeof TIME_COND_VARS)[number]
export const CMP_OPS = ['<', '<=', '>', '>=', '==', '!='] as const
export type CmpOp = (typeof CMP_OPS)[number]
/** The whole values a time compares with, inclusive (weekday 0 is Sunday). */
export const TIME_RANGES: Record<TimeCondVar, readonly [number, number]> = { hour: [0, 23], minute: [0, 59], weekday: [0, 6], month: [1, 12], day: [1, 31] }
/** The most levels and comparisons a tier's condition takes, and the most tiers a price takes. */
export const MAX_COND_DEPTH = 5
export const MAX_COND_LEAVES = 20
export const MAX_TIERS = 20

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
export interface TokenTier extends Partial<Record<PriceKey, number | null>> {
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

/** A visual config of whatever came from the server, with both lists. */
export function asTokenConfig(raw: unknown): VisualConfig {
  const c = (raw ?? {}) as Partial<VisualConfig>
  return { tiers: Array.isArray(c.tiers) ? c.tiers : [], rules: Array.isArray(c.rules) ? c.rules : [] }
}

export const isTimeVar = (v: unknown): v is TimeCondVar => TIME_COND_VARS.includes(v as TimeCondVar)

export type CondKind = 'leaf' | 'and' | 'or' | 'not'
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

export function tokenPricesOf(t: TokenTier | undefined): TokenPrices {
  return Object.fromEntries(TOKEN_VARS.map(k => [k, isSet(t?.[k]) ? num(t?.[k]) : null])) as TokenPrices
}

/** A config of one token tier and no rules: a custom price rule priced by tokens. */
export function singleTier(prices: Partial<Record<PriceKey, number | null>>): VisualConfig {
  return {
    tiers: [{ name: 'base', unit: 'token', ...Object.fromEntries(pricedKeys(prices).map(k => [k, prices[k] ?? null])) }],
    rules: []
  }
}

/** A config of one request tier and no rules: a custom price rule priced by the request. */
export function requestTier(price: number | null): VisualConfig {
  return { tiers: [{ name: 'base', unit: 'request', price }], rules: [] }
}

/** The tier of a config with one tier (by tokens or by the request) and no rules; null for any other. */
export function simpleTier(config: unknown): TokenTier | null {
  const c = asTokenConfig(config)
  return c.tiers.length === 1 && !c.rules.length ? c.tiers[0] : null
}

/** Whether a price takes a custom price of a group or user group: a visual one of one tier and no rules. */
export function takesCustomPrice(mode: PriceMode, config: unknown): boolean {
  return mode === 'visual' && simpleTier(config) !== null
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

function compactTier(t: TokenTier): TokenTier {
  const head: TokenTier = t.when ? { name: t.name, when: compactWhen(t.when) } : { name: t.name }
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
export function compactTokenConfig(cfg: VisualConfig): VisualConfig {
  return { tiers: cfg.tiers.map(compactTier), rules: compactRules(cfg.rules) }
}

export function compactRules(rules: readonly MarkupRule[]): MarkupRule[] {
  return rules.map(r => ({ name: r.name, multiplier: orNull(r.multiplier), conds: r.conds.map(compactCond) }))
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
export function tokenExpr(cfg: VisualConfig): string {
  const [last, ...rest] = [...cfg.tiers].reverse()
  if (!last) return ''
  const base = rest.reduce(
    (acc, t, i) => `${t.when ? whenExpr(t.when) : ''} ? ${tierCall(t.name, tierBody(t))} : ${i > 0 ? `(${acc})` : acc}`,
    tierCall(last.name, tierBody(last))
  )
  return withRules(base, cfg.rules)
}

/** The generated expression of a visual price; '' for an expression price, which has none. */
export function priceExpression(mode: PriceMode, config: unknown): string {
  return mode === 'visual' ? tokenExpr(asTokenConfig(config)) : ''
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

/**
 * Whether Go's time.LoadLocation takes a zone, as far as Intl can tell: an
 * IANA name, spelt with its case (Intl takes "asia/shanghai" and offsets
 * such as "+08:00", Go takes neither), and not "Local".
 */
function validTz(tz: string): boolean {
  if (tz === 'Local' || /^[+-]/.test(tz)) return false
  try {
    const resolved = new Intl.DateTimeFormat('en-US', { timeZone: tz }).resolvedOptions().timeZone
    return resolved === tz || resolved.toLowerCase() !== tz.toLowerCase()
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

function checkRules(rules: readonly MarkupRule[]): LocalIssue[] {
  const out: LocalIssue[] = []
  rules.forEach((r, i) => {
    const n = i + 1
    const issue =
      !isPrice(r.multiplier) || !r.conds.length
        ? { key: 'prices.local.ruleField', params: { n } }
        : r.conds.map(c => condIssue(c, n)).find(Boolean)
    if (issue) out.push(issue)
  })
  return out
}

const isCmpOp = (op: unknown): op is CmpOp => CMP_OPS.includes(op as CmpOp)

/** Why a comparison fails the server's generator; null when it passes. */
function leafIssue(n: CondLeaf, name: string | number): LocalIssue | null {
  if (!COND_VARS.includes(n.var) || !isCmpOp(n.op)) return { key: 'prices.local.condShape', params: { name } }
  if (!isTimeVar(n.var)) {
    if (n.tz !== undefined) return { key: 'prices.local.condShape', params: { name } }
    return isSet(n.value) ? null : { key: 'prices.local.condValue', params: { name } }
  }
  const tz = n.tz ?? ''
  if (typeof tz !== 'string' || !validTz(tz || 'UTC')) return { key: 'prices.local.condTz', params: { name } }
  const [lo, hi] = TIME_RANGES[n.var]
  const v = num(n.value, NaN)
  return isSet(n.value) && Number.isInteger(v) && v >= lo && v <= hi ? null : { key: 'prices.local.condTime', params: { name, lo, hi } }
}

/** The first reason a condition fails the server's generator, of every node depth first; null when it passes. */
function whenIssue(n: TierCond, name: string | number): LocalIssue | null {
  const kind = condKind(n)
  if (!kind) return { key: 'prices.local.condShape', params: { name } }
  if (kind === 'leaf') return leafIssue(n as CondLeaf, name)
  const kids = condChildren(n)
  if (!kids.length) return { key: 'prices.local.condEmpty', params: { name } }
  return kids.reduce<LocalIssue | null>((found, k) => found ?? whenIssue(k, name), null)
}

/** Why a tier's condition is refused: every tier but the last has one, the last has none; within the limits. */
function tierWhenIssue(t: TokenTier, last: boolean, name: string | number): LocalIssue | null {
  if (last) return t.when ? { key: 'prices.local.tierRest' } : null
  if (!t.when) return { key: 'prices.local.tierWhen', params: { name } }
  const issue = whenIssue(t.when, name)
  if (issue) return issue
  if (condDepth(t.when) > MAX_COND_DEPTH) return { key: 'prices.local.condDepth', params: { name, n: MAX_COND_DEPTH } }
  return condLeaves(t.when) > MAX_COND_LEAVES ? { key: 'prices.local.condLeaves', params: { name, n: MAX_COND_LEAVES } } : null
}

/**
 * Problems of a visual config as saved (compactTokenConfig) that block
 * saving (i18n keys under prices.local). They mirror the server's generator,
 * and refuse more: a value a rule condition compares is never blank.
 */
export function checkTokenConfig(cfg: VisualConfig): LocalIssue[] {
  const out: LocalIssue[] = []
  if (!cfg.tiers.length) out.push({ key: 'prices.local.noTier' })
  if (cfg.tiers.length > MAX_TIERS) out.push({ key: 'prices.local.tooManyTiers', params: { n: MAX_TIERS } })
  const names = new Set<string>()
  cfg.tiers.forEach((t, i) => {
    const name = t.name || i + 1
    if (!TIER_NAME.test(t.name)) out.push({ key: 'prices.local.tierName', params: { n: i + 1 } })
    else if (names.has(t.name)) out.push({ key: 'prices.local.tierDup', params: { name } })
    names.add(t.name)
    if (t.unit === 'request' && !isSet(t.price)) out.push({ key: 'prices.local.tierPrice', params: { name } })
    else if ([t.price, ...PRICE_KEYS.map(k => t[k])].some(v => isSet(v) && num(v) < 0))
      out.push({ key: 'prices.local.negative', params: { name } })
    const issue = tierWhenIssue(t, i === cfg.tiers.length - 1, name)
    if (issue) out.push(issue)
  })
  return [...out, ...checkRules(cfg.rules)]
}

/** Problems of a price as it is sent (its config as saved) that block saving. */
export function checkPrice(p: { mode: PriceMode; config: unknown; expression: string }): LocalIssue[] {
  if (p.mode === 'expression') return p.expression.trim() ? [] : [{ key: 'prices.local.emptyExpr' }]
  return checkTokenConfig(asTokenConfig(p.config))
}

// ------------------------------------------------------------------ list summary

export type PriceSummary =
  /** The first tier (the first to match), the number of tiers and of rules. */
  | { kind: 'visual'; first: TokenTier; tiers: number; rules: number }
  | { kind: 'expression' }

export function summarize(mode: PriceMode, config: Record<string, any> | null | undefined): PriceSummary {
  const cfg = asTokenConfig(config)
  const [first] = cfg.tiers
  return mode === 'visual' && first ? { kind: 'visual', first, tiers: cfg.tiers.length, rules: cfg.rules.length } : { kind: 'expression' }
}
