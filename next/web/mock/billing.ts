// Mock handlers: prices, price sync sources, usage, ledger, billing/sticky settings, sticky rules.
// (POST /users/:id/balance/adjust and GET /me/balance live in resources.ts / core.ts.)
import { createHash } from 'node:crypto'
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { accounts } from './accounts'
import { caller, inScope, ownerScope } from './core'
import type { UsageView } from '../src/views/usage/view'
import { PRICE_KEYS, checkPerRequest, checkTokenConfig, priceExpression, singleTier, sortedTiers, asTokenConfig } from '../src/views/prices/priceExpr'

type PriceMode = 'per_request' | 'per_token' | 'expression'
type LText = { en: string; zh: string }
const msg = (en: string, zh: string): LText => ({ en, zh })
export const money = (n: number) => n.toFixed(8)
const hashOf = (s: string) => createHash('sha256').update(s).digest('hex')

// ------------------------------------------------------------------ tiny expression engine (mock only)

type TokenVar = (typeof PRICE_KEYS)[number]
type Usage = Record<TokenVar | 'len', number>

const fmtN = (n: number) => String(Number(n.toPrecision(12)))

// The server generates the expression of a per_request / per_token price from
// its config; the console's generator matches it byte for byte, so the mock uses it.
const genExpr = priceExpression

const ALLOWED_IDENTS = new Set(
  'p c cr cc cc1h len img img_o ai ao tier flat param header has hour weekday day month max min abs ceil floor u true false nil v1'.split(' ')
)

/** An expression with its string literals blanked out, same length. */
const blankStrings = (src: string) => src.replace(/"(?:[^"\\]|\\.)*"/g, (m) => '_'.repeat(m.length))

/** The parenthesised groups of an expression, in the order they open. */
function groups(src: string): Array<{ at: number; text: string }> {
  const bare = blankStrings(src)
  const out: Array<{ at: number; text: string }> = []
  const open: number[] = []
  for (let i = 0; i < bare.length; i++) {
    if (bare[i] === '(') open.push(i)
    else if (bare[i] === ')' && open.length) {
      const at = open.pop()!
      out.push({ at, text: src.slice(at + 1, i) })
    }
  }
  return out.sort((x, y) => x.at - y.at)
}

/** The functions whose result depends on the request or its usage: only a condition that calls one is a traced rule. */
const PROBE = /\b(?:param|header|u|hour|minute|weekday|day|month)\s*\(/

/** `cond ? <number> : 1` at the top level of a group, the condition reading a probe; null for anything else. `q` is where the `?` is. */
function ternary(text: string): { cond: string; multiplier: number; q: number } | null {
  const bare = blankStrings(text)
  let depth = 0
  let q = -1
  for (let i = 0; i < bare.length; i++) {
    const ch = bare[i]
    if (ch === '(') depth++
    else if (ch === ')') depth--
    else if (depth === 0 && ch === '?' && bare[i + 1] !== '?' && bare[i - 1] !== '?' && q < 0) q = i
    else if (depth === 0 && ch === ':' && q >= 0) {
      const m = /^\s*(\d+(?:\.\d+)?)\s*$/.exec(text.slice(q + 1, i))
      const cond = text.slice(0, q).trim()
      return m && text.slice(i + 1).trim() === '1' && PROBE.test(blankStrings(cond)) ? { cond, multiplier: Number(m[1]), q } : null
    }
  }
  return null
}

/** The surcharges of an expression as the server traces them: every `cond ? <number> : 1`, in parentheses or the whole of it; `from`..`to` is the condition in src. */
function surcharges(src: string) {
  return [{ at: -1, text: src }, ...groups(src)].flatMap((g) => {
    const r = ternary(g.text)
    return r ? [{ cond: r.cond, multiplier: r.multiplier, text: g.text, from: g.at + 1, to: g.at + 1 + r.q }] : []
  })
}

/** src with the condition of every traced rule i wrapped in `__rule(i, …)`, as the server's compiler wraps it: a rule not reached records nothing. */
function withTraces(src: string, traced: Array<{ from: number; to: number }>): string {
  const marks = traced.flatMap((r, i) => [{ at: r.from, text: `__rule(${i}, ` }, { at: r.to, text: ')' }])
  return marks.sort((a, b) => b.at - a.at).reduce((s, m) => s.slice(0, m.at) + m.text + s.slice(m.at), src)
}

/** Go string escapes JS has not: \a, and \U with eight hex digits. */
const jsStrings = (src: string) =>
  src.replace(/"(?:[^"\\]|\\.)*"/g, (lit) => lit.replace(/\\(?:(a)|U([0-9a-f]{8})|.)/g, (m, a, u) => (a ? '\\x07' : u ? `\\u{${u}}` : m)))

interface EvalInput {
  usage: Partial<Usage>
  headers?: Record<string, string>
  params?: Record<string, unknown>
  metrics?: Record<string, unknown>
  at?: Date
}

function run(part: string, input: EvalInput, tokens: Usage, hits: boolean[] = []): { value: number; tier: string } {
  let tier = ''
  const headers = Object.fromEntries(Object.entries(input.headers || {}).map(([k, v]) => [k.toLowerCase(), String(v)]))
  const at = input.at || new Date()
  const inTz = (tz: string, opt: Intl.DateTimeFormatOptions) => {
    try {
      return new Intl.DateTimeFormat('en-US', { ...opt, timeZone: tz }).format(at)
    } catch {
      return new Intl.DateTimeFormat('en-US', { ...opt, timeZone: 'UTC' }).format(at)
    }
  }
  const lib: Record<string, unknown> = {
    tier: (name: string, v: number) => ((tier = name), v),
    flat: (x: number) => x * 1e6, // everything is scaled back by 1e-6 below
    param: (path: string) => String(path).split('.').reduce<any>((o, k) => (o == null ? o : o[k]), input.params || {}) ?? null,
    header: (name: string) => headers[String(name).toLowerCase()] ?? '',
    has: (s: unknown, sub: unknown) => String(s ?? '').includes(String(sub)),
    hour: (tz: string) => Number(inTz(tz, { hour: 'numeric', hourCycle: 'h23' })),
    weekday: (tz: string) => ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(inTz(tz, { weekday: 'short' })),
    day: (tz: string) => Number(inTz(tz, { day: 'numeric' })),
    month: (tz: string) => Number(inTz(tz, { month: 'numeric' })),
    max: Math.max,
    min: Math.min,
    abs: Math.abs,
    ceil: Math.ceil,
    floor: Math.floor,
    u: (k: string) => (input.metrics || {})[k] ?? 0, // an absent usage field reads 0
    nil: null,
    __rule: (i: number, v: unknown) => ((hits[i] = !!v), v)
  }
  const names = [...PRICE_KEYS, 'len', ...Object.keys(lib)]
  const values = [...PRICE_KEYS.map((k) => tokens[k]), tokens.len, ...Object.values(lib)]

  const fn = new Function(...names, `"use strict"; return (${jsStrings(part)});`)
  const v = fn(...values)
  return { value: typeof v === 'number' ? v : Number(v), tier }
}

/**
 * Evaluates an expression: token variables are raw token counts and
 * coefficients are USD per 1M, so the result is divided by 1e6 (flat()
 * pre-multiplies to compensate). Every `cond ? <number> : 1` is a surcharge
 * rule; the subtotal and its items are those of the expression with the
 * rules taken out.
 */
function evaluate(expression: string, input: EvalInput) {
  const src = expression.trim().replace(/^v1\s*:/, '')
  const bare = src.replace(/"(?:[^"\\]|\\.)*"/g, '""')
  for (const m of bare.matchAll(/[A-Za-z_][A-Za-z0-9_]*/g)) if (!ALLOWED_IDENTS.has(m[0])) throw new Error(`unknown identifier ${m[0]}`)
  const u = input.usage
  const tokens = Object.fromEntries(PRICE_KEYS.map((k) => [k, +u[k]! || 0])) as Usage
  tokens.len = u.len !== undefined ? +u.len || 0 : tokens.p + tokens.cr + tokens.cc + tokens.cc1h + tokens.img
  const traced = surcharges(src)
  const base = traced.reduce((s, r) => s.replace(r.text, '1'), src)
  const b = run(base, input, tokens)
  const subtotal = b.value / 1e6
  // Per-variable items: evaluate with only that variable set (same len, same tier).
  const zero = { ...(Object.fromEntries(PRICE_KEYS.map((k) => [k, 0])) as Usage), len: tokens.len }
  const flat = run(base, input, zero).value / 1e6
  const items: Array<{ tier: string; label: string; quantity?: number; rate?: number; expr: string; cost: string }> = []
  if (flat) items.push({ tier: b.tier, label: 'flat', expr: `flat(${fmtN(flat)})`, cost: money(flat) })
  for (const k of PRICE_KEYS) {
    if (!tokens[k]) continue
    const cost = run(base, input, { ...zero, [k]: tokens[k] }).value / 1e6 - flat
    if (!cost) continue
    const rate = Number(((cost / tokens[k]) * 1e6).toPrecision(10))
    items.push({ tier: b.tier, label: k, quantity: tokens[k], rate, expr: `${k}*${fmtN(rate)}`, cost: money(cost) })
  }
  // A rule the evaluation did not reach (a branch not taken, a short-circuited operand) is not matched.
  const hits: boolean[] = []
  const cost = run(withTraces(src, traced), input, tokens, hits).value / 1e6
  const rules = traced.map((r, i) => ({ cond: r.cond, multiplier: r.multiplier, matched: !!hits[i] }))
  const mult = rules.reduce((m, r) => (r.matched ? m * r.multiplier : m), 1)
  return { tier: b.tier, tokens, subtotal, flat, items, rules, multiplier: mult, cost }
}

function tryEvaluate(expression: string, input: EvalInput) {
  try {
    return evaluate(expression, input)
  } catch {
    return null
  }
}

// ------------------------------------------------------------------ prices

// Prices are keyed by complete model ids; wildcards are rejected (same rule as the server).
const MODEL_ID = /^[A-Za-z0-9._:/@+-]{1,200}$/

interface MockPrice {
  id: number
  model: string
  mode: PriceMode
  config: Record<string, any>
  expression: string
  expr_version: number
  expr_hash: string
  /** manual: entered by an admin; sync: imported from a price source. */
  source: 'manual' | 'sync'
  sync_source_id: number | null
  synced_at: string | null
  enabled: boolean
  note: string
  updated_by: number | null
  updated_at: string
}

const history = new Map<string, { expr_hash: string; expression: string; expr_version: number; created_at: string }>()

function remember(expression: string) {
  const h = hashOf(expression)
  if (!history.has(h)) history.set(h, { expr_hash: h, expression, expr_version: 1, created_at: now() })
  return h
}

type PriceSeed = Omit<MockPrice, 'expr_hash' | 'expr_version' | 'updated_at' | 'expression' | 'sync_source_id' | 'synced_at' | 'updated_by'> & {
  expression?: string
  sync_source_id?: number | null
}

function mkPrice(p: PriceSeed): MockPrice {
  const expression = p.mode === 'expression' ? p.expression ?? '' : genExpr(p.mode, p.config)
  const synced = p.source === 'sync'
  return {
    ...p,
    expression,
    expr_version: 1,
    expr_hash: remember(expression),
    sync_source_id: synced ? p.sync_source_id ?? null : null,
    synced_at: synced ? now(-2 * 86400) : null,
    updated_by: 1,
    updated_at: now(-86400)
  }
}

// ------------------------------------------------------------------ price sync catalogs (mock upstream data)

type Family = 'anthropic' | 'openai' | 'google' | 'mistral' | 'deepseek' | 'xai'
interface CatalogEntry {
  model: string
  family: Family
  mode: PriceMode
  config: Record<string, any>
}

const round = (n: number) => Number(n.toPrecision(4))

/** Two tiers at 200K, as LiteLLM prices Claude Sonnet. */
const SONNET = {
  tiers: [
    { name: 'standard', max_len: 200000, unit: 'token', p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 },
    { name: 'long_context', max_len: null, unit: 'token', p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 }
  ],
  rules: []
}

const KNOWN: CatalogEntry[] = [
  {
    model: 'claude-sonnet-4-5',
    family: 'anthropic',
    mode: 'per_token',
    config: SONNET
  },
  { model: 'claude-haiku-4-5', family: 'anthropic', mode: 'per_token', config: singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }) },
  { model: 'claude-opus-4-1', family: 'anthropic', mode: 'per_token', config: singleTier({ p: 15, c: 75, cr: 1.5, cc: 18.75, cc1h: 30 }) },
  { model: 'gpt-4o', family: 'openai', mode: 'per_token', config: singleTier({ p: 2.5, c: 10, cr: 1.25 }) },
  { model: 'gpt-4o-mini', family: 'openai', mode: 'per_token', config: singleTier({ p: 0.15, c: 0.6, cr: 0.075 }) },
  { model: 'gpt-5', family: 'openai', mode: 'per_token', config: singleTier({ p: 1.25, c: 10, cr: 0.125 }) },
  { model: 'gemini-2.5-flash', family: 'google', mode: 'per_token', config: singleTier({ p: 0.3, c: 2.5, cr: 0.075 }) },
  {
    model: 'gemini-2.5-pro',
    family: 'google',
    mode: 'per_token',
    config: {
      tiers: [
        { name: 'standard', max_len: 200000, unit: 'token', p: 1.25, c: 10, cr: 0.31 },
        { name: 'long_context', max_len: null, unit: 'token', p: 2.5, c: 15, cr: 0.625 }
      ],
      rules: []
    }
  },
  { model: 'mistral-large-latest', family: 'mistral', mode: 'per_token', config: singleTier({ p: 2, c: 6 }) },
  { model: 'deepseek-chat', family: 'deepseek', mode: 'per_token', config: singleTier({ p: 0.27, c: 1.1, cr: 0.07 }) },
  { model: 'grok-4', family: 'xai', mode: 'per_token', config: singleTier({ p: 3, c: 15, cr: 0.75 }) }
]

const PREFIX: Record<Family, string> = { anthropic: 'claude-legacy', openai: 'gpt-variant', google: 'gemini-exp', mistral: 'mistral', deepseek: 'deepseek', xai: 'grok' }
const SUFFIX = ['preview', 'latest', '2024-08-06', 'mini', 'turbo', 'instruct']
const FAMILIES = Object.keys(PREFIX) as Family[]

/** Deterministic synthetic models of a family (to exercise large previews). */
function synthetic(family: Family, count: number): CatalogEntry[] {
  const out: CatalogEntry[] = []
  for (let i = 1; i <= count; i++) {
    const r = (k: number) => rnd(FAMILIES.indexOf(family) * 10007 + i * 31 + k)
    const p = round(0.05 + r(1) * 10)
    const cfg: Record<string, number> = { p, c: round(p * (3 + Math.floor(r(2) * 3))) }
    if (r(3) > 0.4) cfg.cr = round(p / 10)
    out.push({ model: `${PREFIX[family]}-${String(i).padStart(4, '0')}-${SUFFIX[i % SUFFIX.length]}`, family, mode: 'per_token', config: singleTier(cfg) })
  }
  return out
}

/** Vendor id of a family in each catalog (LiteLLM calls Google "gemini"). */
const vendorOf = (kind: 'litellm' | 'models_dev', f: Family) => (kind === 'litellm' && f === 'google' ? 'gemini' : f)

const CATALOG = {
  litellm: [...KNOWN, ...FAMILIES.flatMap((f) => synthetic(f, 800))],
  models_dev: [
    // models.dev lists the newer GPT-4o price than the one we synced from LiteLLM.
    ...KNOWN.map((e) => (e.model === 'gpt-4o' ? { ...e, config: singleTier({ p: 2.5, c: 10, cr: 1.25, cc: 0 }) } : e)),
    ...FAMILIES.flatMap((f) => synthetic(f, 12))
  ],
  // What the upstream sup2api instance exposes to our key (base prices; its group rate is 1.5).
  sup2api: KNOWN.filter((e) => e.family === 'anthropic' || e.family === 'openai')
}
const UPSTREAM_GROUP_RATE = 1.5
const SKIPPED = { litellm: 37, models_dev: 4, sup2api: 0 }

/** A per_token config with every price times `m`. */
function scaleConfig(cfg: Record<string, any>, m: number): Record<string, any> {
  const scale = (t: Record<string, any>) => Object.fromEntries(Object.entries(t).map(([k, v]) => [k, typeof v === 'number' && k !== 'max_len' ? round(v * m) : v]))
  return { ...cfg, tiers: cfg.tiers.map(scale) }
}

const litellmSynth = CATALOG.litellm.filter((e) => e.model.startsWith('claude-legacy-'))

const prices: MockPrice[] = [
  // Manual, identical to the synced definition -> "unchanged" in previews.
  mkPrice({ id: 1, model: 'claude-sonnet-4-5', mode: 'per_token', config: SONNET, source: 'manual', enabled: true, note: '' }),
  // Synced from LiteLLM and still current -> "unchanged".
  mkPrice({ id: 2, model: 'claude-haiku-4-5', mode: 'per_token', config: singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  mkPrice({
    id: 3,
    model: 'claude-sonnet-x',
    mode: 'per_token',
    config: {
      tiers: SONNET.tiers,
      rules: [
        { name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] },
        {
          name: 'priority',
          multiplier: 2,
          conds: [
            { source: 'param', key: 'service_tier', op: 'eq', value: 'priority' },
            { source: 'usage', key: 'service_tier', op: 'eq', value: 'priority' }
          ]
        },
        { name: 'night', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] }
      ]
    },
    source: 'manual',
    enabled: true,
    note: 'fast mode costs double'
  }),
  mkPrice({ id: 4, model: 'web-search', mode: 'per_request', config: { price: 0.01 }, source: 'manual', enabled: true, note: '' }),
  // Manual and different from every source -> "manual" (unticked by default).
  mkPrice({
    id: 5,
    model: 'claude-opus-4-1',
    mode: 'expression',
    config: {},
    expression: 'tier("base", flat(0.01) + p*15 + c*75) * (hour("Asia/Shanghai") < 8 ? 0.8 : 1)',
    source: 'manual',
    enabled: false,
    note: 'night discount'
  }),
  // Synced from LiteLLM, source price changed since -> "update".
  mkPrice({ id: 6, model: 'gpt-4o', mode: 'per_token', config: singleTier({ p: 5, c: 15, cr: 2.5 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  // Synced from models.dev and current.
  mkPrice({ id: 7, model: 'gemini-2.5-flash', mode: 'per_token', config: singleTier({ p: 0.3, c: 2.5, cr: 0.075 }), source: 'sync', sync_source_id: 2, enabled: true, note: '' }),
  mkPrice({ id: 8, model: 'gpt-4o-mini', mode: 'per_token', config: singleTier({ p: 0.3, c: 1.2 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  // A few synthetic LiteLLM models: current, outdated, and manually edited.
  ...litellmSynth.slice(0, 6).map((e, i) => mkPrice({ id: 20 + i, model: e.model, mode: e.mode, config: e.config, source: 'sync', sync_source_id: 1, enabled: true, note: '' })),
  ...litellmSynth
    .slice(6, 10)
    .map((e, i) => mkPrice({ id: 30 + i, model: e.model, mode: e.mode, config: scaleConfig(e.config, 0.8), source: 'sync', sync_source_id: 1, enabled: true, note: '' })),
  ...litellmSynth
    .slice(10, 13)
    .map((e, i) => mkPrice({ id: 40 + i, model: e.model, mode: e.mode, config: scaleConfig(e.config, 1.2), source: 'manual', enabled: true, note: 'negotiated' }))
]

/** Why the server's generator refuses a config: the console's own checks, the tiers in the order they match as the server sorts them; null when it takes it. */
function configIssue(mode: PriceMode, config: Record<string, any>): string | null {
  if (mode === 'expression') return null
  const cfg = asTokenConfig(config)
  const issues = mode === 'per_request' ? checkPerRequest(config.price) : checkTokenConfig({ ...cfg, tiers: sortedTiers(cfg.tiers) })
  return issues.length ? `config: ${issues.map((x) => x.key.replace('prices.local.', '')).join(', ')}` : null
}

function exprOf(body: any): { mode: PriceMode; config: Record<string, any>; expression: string; issue: string | null } {
  const mode: PriceMode = ['per_request', 'per_token', 'expression'].includes(body?.mode) ? body.mode : 'expression'
  // A per_request / per_token expression is generated from the config, whatever the client sent; an expression price has no config.
  const config = mode !== 'expression' && body?.config && typeof body.config === 'object' ? body.config : {}
  const issue = configIssue(mode, config)
  const expression = mode === 'expression' ? String(body?.expression || '') : issue ? '' : genExpr(mode, config)
  return { mode, config, expression, issue }
}

/** 1-based line and column of a 0-based offset. */
function lineCol(src: string, at: number) {
  const before = src.slice(0, at).split('\n')
  return { line: before.length, column: before[before.length - 1].length + 1 }
}

const STR = /"(?:[^"\\]|\\.)*"/g

function editDistance(a: string, b: string): number {
  const d = Array.from({ length: b.length + 1 }, (_, j) => j)
  for (let i = 1; i <= a.length; i++) {
    let prev = d[0]
    d[0] = i
    for (let j = 1; j <= b.length; j++) {
      const t = d[j]
      d[j] = Math.min(d[j] + 1, d[j - 1] + 1, prev + (a[i - 1] === b[j - 1] ? 0 : 1))
      prev = t
    }
  }
  return d[b.length]
}

/** First syntax problem the mock can place: an unbalanced bracket or an unknown name. */
function syntaxIssue(src: string) {
  const bare = src.replace(STR, (m) => '"' + ' '.repeat(m.length - 2) + '"')
  const syntax = (at: number, detail: string) => ({ ...lineCol(src, at), code: 'syntax_error', message: msg('syntax error', '语法错误'), detail })
  const open: number[] = []
  for (let i = 0; i < bare.length; i++) {
    if (bare[i] === '(') open.push(i)
    else if (bare[i] === ')' && open.pop() === undefined) return syntax(i, '多了一个右括号')
  }
  if (open.length) return syntax(open[open.length - 1], '这个左括号没有配对的右括号')
  for (const m of bare.matchAll(/[A-Za-z_][A-Za-z0-9_]*/g)) {
    if (!ALLOWED_IDENTS.has(m[0])) {
      const near = [...ALLOWED_IDENTS].filter((x) => !x.startsWith('_') && editDistance(x, m[0]) <= 2).sort((a, b) => editDistance(a, m[0]) - editDistance(b, m[0]))[0]
      return syntax(m.index!, `不认识的名字 ${m[0]}${near ? `，是不是 ${near}？` : ''}`)
    }
  }
  return null
}

/** What an expression reads (expr.Analysis), by pattern. */
function analyze(src: string) {
  const all = (re: RegExp) => [...new Set([...src.matchAll(re)].map((m) => m[1]))]
  const bare = src.replace(STR, '""')
  const idents = new Set(bare.match(/[A-Za-z_][A-Za-z0-9_]*/g) || [])
  return {
    version: 1,
    vars: [...PRICE_KEYS, 'len'].filter((k) => idents.has(k)),
    params: all(/param\("([^"]+)"\)/g),
    headers: all(/header\("([^"]+)"\)/g).map((h) => h.toLowerCase()),
    facts: all(/\bu\("([^"]+)"\)/g),
    tiers: all(/tier\("([^"]+)"/g),
    rules: surcharges(src).map((r) => r.cond),
    len_boundaries: [...new Set([...bare.matchAll(/len\s*(?:<=|<|>=|>)\s*(\d+)/g)].map((m) => Number(m[1])))].sort((a, b) => a - b),
    zones: all(/(?:hour|minute|weekday|day|month)\("([^"]+)"\)/g)
  }
}

/** Usage fields the mock plugins declare. */
const DECLARED_FACTS = new Set(['service_tier', 'speed', 'video_seconds'])

const SMOKE: Array<[string, Record<string, number>]> = [
  ['全部为 0', {}],
  ['只有输入 1,000,000 token', { p: 1e6, len: 1e6 }],
  ['只有输出 1,000 token', { c: 1000 }],
  ['缓存读写各 1,000 token', { cr: 1000, cc: 1000, cc1h: 1000 }],
  ['len = 1,000,000,000', { p: 1e6, len: 1e9 }]
]

function validate(body: any) {
  const { mode, config, expression, issue } = exprOf(body)
  const errors: Array<{ code: string; message: LText; detail?: unknown; line?: number; column?: number }> = []
  const warnings: Array<{ code: string; message: LText; detail?: unknown }> = []
  let perMillion = 0
  let analysis: ReturnType<typeof analyze> | undefined
  const src = expression.trim().replace(/^v1\s*:/, '')
  const syn = issue || !src ? null : syntaxIssue(src)
  if (issue) errors.push({ code: 'invalid_config', message: msg(issue, `价格配置无效：${issue}`) })
  else if (!src) errors.push({ code: 'empty_expression', message: msg('expression is empty', '表达式为空') })
  else if (syn) errors.push(syn)
  else {
    analysis = analyze(src)
    for (const f of analysis.facts) {
      if (!DECLARED_FACTS.has(f)) errors.push({ code: 'unknown_usage_fact', message: msg('usage field not declared by any plugin', '没有插件声明这个用量字段'), detail: f })
    }
    for (const [name, usage] of errors.length ? [] : SMOKE) {
      try {
        const r = evaluate(expression, { usage })
        if (!Number.isFinite(r.cost) || r.cost < 0) throw new Error(`result ${r.cost} is not a finite non-negative number`)
        if (usage.p === 1e6 && usage.len === 1e6) perMillion = r.cost
      } catch (e) {
        const text = e instanceof Error ? e.message : String(e)
        errors.push({ code: 'smoke_test_failed', message: msg('trial run failed', '试跑失败'), detail: `样例「${name}」：${text}` })
        break
      }
    }
  }
  const threshold = Number(billingSettings.big_cost_warning_usd) || 10
  if (!errors.length && perMillion > threshold) {
    warnings.push({
      code: 'big_cost',
      message: msg(`1M input tokens cost $${fmtN(perMillion)} (threshold $${threshold})`, `100 万输入 token 费用 $${fmtN(perMillion)}，超过阈值 $${threshold}`),
      detail: { cost: perMillion }
    })
  }
  return {
    ok: errors.length === 0,
    expression,
    expr_hash: hashOf(expression),
    errors,
    warnings,
    samples: 38,
    cost_per_million_input: money(perMillion),
    analysis,
    mode,
    config
  }
}

function priceFilter(q: Record<string, string>) {
  return prices
    .filter((p) => {
      if (q.mode && p.mode !== q.mode) return false
      if (q.source && p.source !== q.source) return false
      if (q.sync_source_id && String(p.sync_source_id ?? '') !== q.sync_source_id) return false
      if (q.enabled && (q.enabled === 'true' || q.enabled === '1') !== p.enabled) return false
      if (q.q && !`${p.model} ${p.note}`.toLowerCase().includes(q.q.toLowerCase())) return false
      return true
    })
    .sort((a, b) => a.model.localeCompare(b.model) || a.id - b.id)
}

/** API shape of a price: adds the name of its sync source. */
function priceView(p: MockPrice) {
  const src = p.sync_source_id ? sources.find((s) => s.id === p.sync_source_id) : undefined
  return { ...p, sync_source_name: src ? src.name : null }
}

const findPrice = (req: MockRequest) => prices.find((p) => p.id === Number(req.params.id))

on('GET', '/prices', (req) => {
  const r = paginate(priceFilter(req.query), req.query)
  return { ...r, data: r.data.map(priceView) }
})
on('POST', '/prices/validate', (req) => {
  const v = validate(req.body)
  return { ok: v.ok, expression: v.expression, expr_hash: v.expr_hash, errors: v.errors, warnings: v.warnings, samples: v.samples, cost_per_million_input: v.cost_per_million_input, analysis: v.analysis }
})

function tierBounds(expression: string) {
  return [...expression.matchAll(/len\s*<=\s*(\d+)\s*\?\s*tier\("((?:[^"\\]|\\.)*)"/g)].map((m) => ({ name: m[2], max_len: Number(m[1]) }))
}

on('POST', '/prices/preview', (req) => {
  const b = req.body || {}
  let src: { mode: PriceMode; config: Record<string, any>; expression: string }
  if (b.price_id) {
    const p = prices.find((x) => x.id === Number(b.price_id))
    if (!p) return fail(404, 'not_found', 'price not found')
    src = { mode: p.mode, config: p.config, expression: p.expression }
  } else {
    const e = exprOf(b)
    if (e.issue) return fail(400, 'invalid_argument', e.issue)
    src = e
  }
  const params = Object.fromEntries(Object.entries(b.params || {}).map(([k, v]) => [k, v]))
  const r = tryEvaluate(src.expression, { usage: b.usage || {}, headers: b.headers, params, metrics: b.metrics, at: b.at ? new Date(b.at) : undefined })
  if (!r) return fail(400, 'invalid_argument', 'expression cannot be evaluated')
  const rate = b.group_id ? (Number(b.group_id) === 2 ? 1.5 : 1) : 1
  return {
    cost: money(r.cost * rate),
    base_cost: money(r.cost),
    rate_multiplier: rate.toFixed(2),
    tier: r.tier,
    rules: r.rules,
    expression: src.expression,
    expr_hash: hashOf(src.expression),
    breakdown: {
      vars: r.tokens,
      tiers: tierBounds(src.expression),
      items: r.items,
      subtotal: money(r.subtotal),
      base: money(r.cost),
      rules_multiplier: r.multiplier
    }
  }
})

on('GET', '/prices/history/:hash', (req) => history.get(req.params.hash) || fail(404, 'not_found', 'no such expression'))

on('GET', '/prices/:id', (req) => {
  const p = findPrice(req)
  return p ? { ...priceView(p), analysis: { params: [], headers: [], metrics: [] } } : fail(404, 'not_found', 'price not found')
})

function checkSave(body: any) {
  const v = validate(body)
  if (v.errors.length) return fail(400, 'invalid_argument', 'expression is invalid', { expression: v.expression, errors: v.errors, warnings: v.warnings })
  if (v.warnings.some((w) => w.code === 'big_cost') && !body?.confirm) {
    return fail(400, 'invalid_argument', 'high cost requires confirmation', { expression: v.expression, errors: [], warnings: v.warnings, confirmation_required: true })
  }
  return v
}

const modelInvalid = () => fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'model', code: 'invalid', message: 'a complete model id (no wildcards)' }] })
const modelTaken = () => fail(409, 'conflict', 'a price for this model already exists')

on('POST', '/prices', (req) => {
  const b = req.body || {}
  if (!b.model) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'model', code: 'required', message: 'Model is required' }] })
  if (!MODEL_ID.test(b.model)) return modelInvalid()
  if ('platform' in b) return fail(400, 'invalid_argument', 'unknown field "platform"')
  const v = checkSave(b)
  if ('__status' in v) return v
  // One price per model (prices are global and keyed by complete model ids).
  if (prices.some((p) => p.model === b.model)) return modelTaken()
  const p: MockPrice = {
    id: nextId(),
    model: b.model,
    mode: v.mode,
    config: v.config,
    expression: v.expression,
    expr_version: 1,
    expr_hash: remember(v.expression),
    source: 'manual',
    sync_source_id: null,
    synced_at: null,
    enabled: b.enabled !== false,
    note: b.note || '',
    updated_by: 1,
    updated_at: now()
  }
  prices.push(p)
  return { __status: 201, body: { data: priceView(p) } }
})

on('PATCH', '/prices/:id', (req) => {
  const p = findPrice(req)
  if (!p) return fail(404, 'not_found', 'price not found')
  const b = req.body || {}
  const keys = Object.keys(b).filter((k) => k !== 'confirm')
  if (b.model !== undefined && !MODEL_ID.test(b.model)) return modelInvalid()
  if (b.model !== undefined && b.model !== p.model && prices.some((x) => x.model === b.model)) return modelTaken()
  if (keys.every((k) => k === 'enabled' || k === 'note')) {
    if (b.enabled !== undefined) p.enabled = !!b.enabled
    if (b.note !== undefined) p.note = String(b.note)
    p.updated_at = now()
    return priceView(p)
  }
  const v = checkSave({ ...p, ...b })
  if ('__status' in v) return v
  // Editing the definition of a synced price turns it into a manual price.
  const defChanged =
    (b.model !== undefined && b.model !== p.model) ||
    v.mode !== p.mode ||
    v.expression !== p.expression ||
    (b.config !== undefined && JSON.stringify(b.config) !== JSON.stringify(p.config))
  Object.assign(p, {
    model: b.model ?? p.model,
    mode: v.mode,
    config: v.config,
    expression: v.expression,
    expr_hash: remember(v.expression),
    enabled: b.enabled ?? p.enabled,
    note: b.note ?? p.note,
    updated_by: 1,
    updated_at: now()
  })
  if (defChanged && p.source === 'sync') Object.assign(p, { source: 'manual', sync_source_id: null })
  return priceView(p)
})

on('DELETE', '/prices/:id', (req) => {
  const i = prices.findIndex((p) => p.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'price not found')
  prices.splice(i, 1)
  return noContent()
})

// ------------------------------------------------------------------ price math of group pricing (pricingStore.ts, pricing.ts)

/** A price as the pricing endpoints return it. `multiplier` is only on the list price of a multiplier rule: that price (the global one, as it is) times it. */
export interface PriceDef {
  mode: PriceMode
  config: Record<string, any> | null
  expression: string
  multiplier?: string
}

/** The enabled global price of a model; null when it has none. */
export function globalPrice(model: string): PriceDef | null {
  const p = priceFor(model)
  return p && { mode: p.mode, config: p.config, expression: p.expression }
}

/** The models with an enabled global price, sorted. */
export function pricedModels(): string[] {
  return prices.filter((p) => p.enabled).map((p) => p.model).sort()
}

/** A price given as a config: what a custom price rule holds. */
export function configPrice(mode: PriceMode, config: Record<string, any>): PriceDef {
  return { mode, config, expression: genExpr(mode, config) }
}

/** What a call with `usage` costs at a price, in USD, its multiplier included; null when the expression cannot be evaluated with that usage (an error, or a result that is not a finite non-negative number, as expr.Eval refuses). */
export function priceCost(def: PriceDef, usage: Partial<Usage>): number | null {
  const cost = tryEvaluate(def.expression, { usage })?.cost
  return cost !== undefined && Number.isFinite(cost) && cost >= 0 ? cost * Number(def.multiplier ?? 1) : null
}

// ------------------------------------------------------------------ price sync sources

type SourceKind = 'litellm' | 'models_dev' | 'sup2api'
const SOURCE_KINDS: SourceKind[] = ['litellm', 'models_dev', 'sup2api']
const DEFAULT_URL: Record<SourceKind, string> = {
  litellm: 'https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json',
  models_dev: 'https://models.dev/api.json',
  sup2api: ''
}
const DEFAULT_PROVIDERS: Record<'litellm' | 'models_dev', string[]> = {
  litellm: ['anthropic', 'openai', 'gemini'],
  models_dev: ['anthropic', 'openai', 'google']
}

interface MockSource {
  id: number
  name: string
  kind: SourceKind
  url: string
  api_key: string
  options: Record<string, any>
  enabled: boolean
  last_synced_at: string | null
  last_error: string
  created_at: string
  updated_at: string
}

const sources: MockSource[] = [
  {
    id: 1,
    name: 'LiteLLM',
    kind: 'litellm',
    url: DEFAULT_URL.litellm,
    api_key: '',
    options: { providers: [...DEFAULT_PROVIDERS.litellm] },
    enabled: true,
    last_synced_at: now(-2 * 86400),
    last_error: '',
    created_at: now(-30 * 86400),
    updated_at: now(-2 * 86400)
  },
  {
    id: 2,
    name: 'models.dev',
    kind: 'models_dev',
    url: DEFAULT_URL.models_dev,
    api_key: '',
    options: { providers: [...DEFAULT_PROVIDERS.models_dev] },
    enabled: true,
    last_synced_at: now(-5 * 86400),
    last_error: '',
    created_at: now(-30 * 86400),
    updated_at: now(-5 * 86400)
  },
  {
    id: 3,
    name: '上游 A',
    kind: 'sup2api',
    url: 'https://up.example.com',
    api_key: 'sk-s2a-mock-upstream',
    options: { apply_multiplier: true },
    enabled: true,
    last_synced_at: null,
    last_error: '',
    created_at: now(-3 * 86400),
    updated_at: now(-3 * 86400)
  },
  {
    id: 4,
    name: '上游 B（离线）',
    kind: 'sup2api',
    url: 'https://down.example.com',
    api_key: 'sk-s2a-mock-down',
    options: { apply_multiplier: false },
    enabled: false,
    last_synced_at: null,
    last_error: 'GET https://down.example.com/api/v1/me/prices: dial tcp: connection refused',
    created_at: now(-3 * 86400),
    updated_at: now(-3 * 86400)
  }
]

function sourceView(s: MockSource) {
  const { api_key, ...rest } = s
  return { ...rest, has_api_key: !!api_key, price_count: prices.filter((p) => p.sync_source_id === s.id).length }
}

const findSource = (req: MockRequest) => sources.find((s) => s.id === Number(req.params.id))

const fieldFail = (fields: Array<{ field: string; code: string; message: string }>) => fail(400, 'invalid_argument', 'invalid', { fields })

function isHttpUrl(v: unknown): boolean {
  try {
    const u = new URL(String(v))
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.host
  } catch {
    return false
  }
}

/** Validates and normalizes options for a kind; returns the options or a field error list. */
function sourceOptions(kind: SourceKind, raw: any): Record<string, any> | Array<{ field: string; code: string; message: string }> {
  if (kind === 'sup2api') {
    if (raw?.apply_multiplier !== undefined && typeof raw.apply_multiplier !== 'boolean') {
      return [{ field: 'options.apply_multiplier', code: 'invalid', message: 'must be a boolean' }]
    }
    return { apply_multiplier: raw?.apply_multiplier !== false }
  }
  if (raw?.providers === undefined) return { providers: [...DEFAULT_PROVIDERS[kind]] }
  if (!Array.isArray(raw.providers) || raw.providers.some((x: unknown) => typeof x !== 'string' || !/^[a-z0-9_.-]{1,64}$/i.test(x))) {
    return [{ field: 'options.providers', code: 'invalid', message: 'providers must be a list of vendor ids' }]
  }
  return { providers: [...new Set(raw.providers as string[])] }
}

on('GET', '/price-sources', () => sources.map(sourceView))

on('POST', '/price-sources', (req) => {
  const b = req.body || {}
  const errs: Array<{ field: string; code: string; message: string }> = []
  const name = String(b.name ?? '').trim()
  if (!name) errs.push({ field: 'name', code: 'required', message: 'Name is required' })
  if (!SOURCE_KINDS.includes(b.kind)) errs.push({ field: 'kind', code: 'enum', message: 'kind must be litellm, models_dev or sup2api' })
  const kind = b.kind as SourceKind
  const url = String(b.url ?? (SOURCE_KINDS.includes(kind) ? DEFAULT_URL[kind] : '')).trim()
  if (!url) errs.push({ field: 'url', code: 'required', message: 'URL is required' })
  else if (!isHttpUrl(url)) errs.push({ field: 'url', code: 'invalid', message: 'must be an http(s) URL' })
  if (kind === 'sup2api' && !String(b.api_key ?? '').trim()) errs.push({ field: 'api_key', code: 'required', message: 'An upstream sup2api source needs an API key' })
  const opts = SOURCE_KINDS.includes(kind) ? sourceOptions(kind, b.options) : {}
  if (Array.isArray(opts)) errs.push(...opts)
  if (errs.length) return fieldFail(errs)
  if (sources.some((s) => s.name === name)) return fail(409, 'conflict', 'a price source with this name already exists')
  const s: MockSource = {
    id: nextId(),
    name,
    kind,
    url,
    api_key: String(b.api_key ?? '').trim(),
    options: opts as Record<string, any>,
    enabled: b.enabled !== false,
    last_synced_at: null,
    last_error: '',
    created_at: now(),
    updated_at: now()
  }
  sources.push(s)
  return { __status: 201, body: { data: sourceView(s) } }
})

on('PATCH', '/price-sources/:id', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const b = req.body || {}
  const errs: Array<{ field: string; code: string; message: string }> = []
  const kind: SourceKind = b.kind !== undefined ? b.kind : s.kind
  if (!SOURCE_KINDS.includes(kind)) errs.push({ field: 'kind', code: 'enum', message: 'kind must be litellm, models_dev or sup2api' })
  const name = b.name !== undefined ? String(b.name).trim() : s.name
  if (!name) errs.push({ field: 'name', code: 'required', message: 'Name is required' })
  const url = b.url !== undefined ? String(b.url).trim() : s.url
  if (!url) errs.push({ field: 'url', code: 'required', message: 'URL is required' })
  else if (!isHttpUrl(url)) errs.push({ field: 'url', code: 'invalid', message: 'must be an http(s) URL' })
  // api_key: omitted / null = keep, "" = clear, other = replace.
  const apiKey = b.api_key === undefined || b.api_key === null ? s.api_key : String(b.api_key).trim()
  if (kind === 'sup2api' && !apiKey) errs.push({ field: 'api_key', code: 'required', message: 'An upstream sup2api source needs an API key' })
  const opts = SOURCE_KINDS.includes(kind) ? (b.options !== undefined || kind !== s.kind ? sourceOptions(kind, b.options) : s.options) : {}
  if (Array.isArray(opts)) errs.push(...opts)
  if (errs.length) return fieldFail(errs)
  if (sources.some((x) => x.id !== s.id && x.name === name)) return fail(409, 'conflict', 'a price source with this name already exists')
  Object.assign(s, { name, kind, url, api_key: apiKey, options: opts, enabled: b.enabled !== undefined ? !!b.enabled : s.enabled, updated_at: now() })
  return sourceView(s)
})

on('DELETE', '/price-sources/:id', (req) => {
  const i = sources.findIndex((s) => s.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'price source not found')
  const id = sources[i].id
  // Imported prices are kept; they just lose the link to the source.
  for (const p of prices) if (p.sync_source_id === id) p.sync_source_id = null
  sources.splice(i, 1)
  return noContent()
})

/** "Fetches" a source: the mock catalog filtered by vendors, or an error message. */
function fetchCatalog(s: MockSource): { entries: CatalogEntry[] } | { error: string } {
  if (/down|fail/i.test(s.url)) return { error: `GET ${s.url.replace(/\/$/, '')}${s.kind === 'sup2api' ? '/api/v1/me/prices' : ''}: dial tcp: connection refused` }
  if (s.kind === 'sup2api') {
    if (!s.api_key) return { error: 'upstream rejected the API key (401 unauthenticated)' }
    const m = s.options?.apply_multiplier === false ? 1 : UPSTREAM_GROUP_RATE
    return { entries: CATALOG.sup2api.map((e) => ({ ...e, config: m === 1 ? e.config : scaleConfig(e.config, m) })) }
  }
  const kind = s.kind
  const want = new Set<string>(Array.isArray(s.options?.providers) ? s.options.providers : DEFAULT_PROVIDERS[kind])
  return { entries: CATALOG[kind].filter((e) => want.has(vendorOf(kind, e.family))) }
}

function incomingOf(e: CatalogEntry) {
  return { mode: e.mode, config: e.config, expression: genExpr(e.mode, e.config) }
}

function syncAction(e: CatalogEntry): { action: 'create' | 'update' | 'manual' | 'unchanged'; current: MockPrice | undefined } {
  const current = prices.find((p) => p.model === e.model)
  if (!current) return { action: 'create', current }
  const inc = incomingOf(e)
  if (current.mode === inc.mode && current.expression.trim() === inc.expression.trim()) return { action: 'unchanged', current }
  return { action: current.source === 'sync' ? 'update' : 'manual', current }
}

on('POST', '/price-sources/:id/preview', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const r = fetchCatalog(s)
  if ('error' in r) {
    s.last_error = r.error
    return fail(503, 'unavailable', `cannot fetch prices from ${s.name}: ${r.error}`)
  }
  s.last_error = ''
  const items = r.entries
    .map((e) => {
      const { action, current } = syncAction(e)
      return {
        model: e.model,
        action,
        incoming: incomingOf(e),
        current: current
          ? { id: current.id, mode: current.mode, config: current.config, expression: current.expression, source: current.source, sync_source_id: current.sync_source_id, enabled: current.enabled }
          : null
      }
    })
    .sort((a, b) => a.model.localeCompare(b.model))
  return { source_id: s.id, fetched_at: now(), total: items.length, skipped: SKIPPED[s.kind], items }
})

on('POST', '/price-sources/:id/apply', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const models = req.body?.models
  if (!Array.isArray(models) || models.some((m: unknown) => typeof m !== 'string')) {
    return fieldFail([{ field: 'models', code: 'invalid', message: 'models must be a list of model ids' }])
  }
  const r = fetchCatalog(s)
  if ('error' in r) {
    s.last_error = r.error
    return fail(503, 'unavailable', `cannot fetch prices from ${s.name}: ${r.error}`)
  }
  const byModel = new Map(r.entries.map((e) => [e.model, e]))
  const out = { created: 0, updated: 0, unchanged: 0, skipped: [] as Array<{ model: string; reason: string }> }
  for (const model of new Set<string>(models)) {
    const e = byModel.get(model)
    if (!e) {
      out.skipped.push({ model, reason: 'not offered by the source' })
      continue
    }
    if (!MODEL_ID.test(model)) {
      out.skipped.push({ model, reason: 'not a complete model id' })
      continue
    }
    const inc = incomingOf(e)
    const { action, current } = syncAction(e)
    if (action === 'unchanged') {
      out.unchanged++
      continue
    }
    const fields = {
      mode: inc.mode,
      config: inc.config,
      expression: inc.expression,
      expr_hash: remember(inc.expression),
      source: 'sync' as const,
      sync_source_id: s.id,
      synced_at: now(),
      updated_by: 1,
      updated_at: now()
    }
    if (current) {
      Object.assign(current, fields, { expr_version: current.expr_version })
      out.updated++
    } else {
      prices.push({ id: nextId(), model, expr_version: 1, enabled: true, note: '', ...fields })
      out.created++
    }
  }
  s.last_synced_at = now()
  s.last_error = ''
  return out
})

// ------------------------------------------------------------------ usage

const USERS = [
  { id: 1, email: 'admin@example.com', name: 'Admin' },
  { id: 2, email: 'zhangsan@example.com', name: '张三' },
  { id: 3, email: 'lisi@example.com', name: '李四' }
]
const GROUPS = [
  { id: 1, name: 'default', rate: 1 },
  { id: 2, name: 'vip', rate: 1.5 }
]
// Same accounts as mock/accounts.ts. Anthropic-platform accounts sometimes
// serve the openai chat endpoint through conversion to anthropic.messages;
// the built-in openai / gemini accounts serve their own platforms.
const ACCOUNTS = [
  { id: 12, name: 'claude-main', plugin_key: 'anthropic', type: 'apikey', upstream: 'anthropic.messages' },
  { id: 13, name: 'claude-bak', plugin_key: 'anthropic', type: 'apikey', upstream: 'anthropic.messages' },
  { id: 16, name: 'relay-1', plugin_key: 'relay', type: 'relay_key', upstream: 'anthropic.messages' },
  { id: 19, name: 'openai-main', plugin_key: 'openai', type: 'apikey', upstream: 'openai.chat' },
  { id: 20, name: 'gemini-main', plugin_key: 'gemini', type: 'apikey', upstream: 'gemini.generate' }
]
// Client addresses per user: most calls come from one machine, some from a second.
const CLIENT_IPS: Record<number, string[]> = { 1: ['203.0.113.10', '203.0.113.11'], 2: ['198.51.100.23', '2001:db8::5'], 3: ['192.0.2.44', '192.0.2.45'] }
const MODELS = ['claude-sonnet-x', 'claude-sonnet-4-5', 'claude-haiku-4-5']
const OWN_MODELS: Record<string, string[]> = { openai: ['gpt-4o', 'gpt-4o-mini'], gemini: ['gemini-2.5-flash'] }
/** Client endpoint per upstream protocol of the native accounts. */
const NATIVE_ENDPOINT: Record<string, { platform: string; protocol: string; endpoint: string }> = {
  'openai.chat': { platform: 'openai', protocol: 'openai.chat', endpoint: '/v1/chat/completions' },
  'gemini.generate': { platform: 'gemini', protocol: 'gemini.generate', endpoint: '/v1beta/models/gemini-2.5-flash:generateContent' }
}

function rnd(seed: number) {
  const x = Math.sin(seed) * 10000
  return x - Math.floor(x)
}

function priceFor(model: string) {
  // One price per complete model id (no wildcards).
  return prices.find((p) => p.enabled && p.model === model) || null
}

/**
 * How group pricing priced a record (billing_detail.inputs.pricing): the
 * segment of the user, where the rate comes from and the rule that gave the
 * list price, if one did. A record at the global price and the group's
 * default rate has none.
 */
interface PricingTerms {
  segment_id: number
  rate_source: 'segment' | 'discount' | 'group'
  /** A rule of the segment or of the group: a multiplier of the global price, or a price of its own. */
  rule?: { source: 'segment' | 'group'; scale?: number; custom?: PriceDef }
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
function charge(price: MockPrice, input: EvalInput, rate: number, terms?: PricingTerms) {
  const { source, scale, custom } = terms?.rule ?? {}
  const global = tryEvaluate(price.expression, input)
  const ev = custom ? tryEvaluate(custom.expression, input) : global
  if (!ev || !global) return null
  const listCost = ev.cost * (scale ?? 1)
  const total = listCost * rate
  const pricing = terms && {
    segment_id: terms.segment_id,
    rate_source: terms.rate_source,
    ...(source ? { price_source: source } : {}),
    ...(scale === undefined ? {} : { scale: String(scale) }),
    ...(custom ? { base: { expression: price.expression, expr_hash: price.expr_hash } } : {})
  }
  return {
    status: 'billed',
    total,
    tier: ev.tier,
    detail: {
      tier: ev.tier,
      rules: ev.rules,
      breakdown: {
        vars: ev.tokens,
        tiers: tierBounds(custom?.expression ?? price.expression),
        items: ev.items,
        subtotal: money(ev.subtotal),
        base: money(ev.cost),
        rules_multiplier: ev.multiplier
      },
      cost: money(global.cost),
      ...(source ? { list_cost: money(listCost) } : {}),
      rate_multiplier: rate.toFixed(2),
      total_cost: money(total),
      expr_version: 1,
      ...(pricing ? { inputs: { pricing } } : {})
    }
  }
}

const usageRows: any[] = []

/**
 * The base cost of a record (usage_logs.base_cost, CONTRACTS §97.5): the
 * global price times the usage, before any rule and any rate. It is the
 * billing_detail.cost a record keeps; one without a detail falls back to
 * total_cost / rate_multiplier, as rows from before 0096 do (0 at a rate of 0).
 * A record priced by a rule shows why it matters: there the fallback would be
 * its list cost.
 */
function baseCost(u: any): number {
  if (u._detail?.cost !== undefined) return Number(u._detail.cost)
  return Number(u.rate_multiplier) > 0 ? Number(u.total_cost) / Number(u.rate_multiplier) : 0
}
const ledgerRows: any[] = []

for (let i = 0; i < 90; i++) {
  const r = (k: number) => rnd(i * 7 + k)
  const user = USERS[Math.floor(r(1) * USERS.length)]
  const group = GROUPS[Math.floor(r(2) * GROUPS.length)]
  const account = ACCOUNTS[Math.floor(r(3) * ACCOUNTS.length)]
  const own = OWN_MODELS[account.plugin_key]
  const model = own ? own[Math.floor(r(4) * own.length)] : MODELS[Math.floor(r(4) * MODELS.length)]
  const at = new Date(Date.now() - Math.floor(r(5) * 7 * 86400000))
  const blocked = r(6) < 0.06
  const failed = !blocked && r(7) < 0.08
  const success = !blocked && !failed
  const inTok = success ? Math.floor(r(8) * 4000) + 200 : 0
  const outTok = success ? Math.floor(r(9) * 1500) + 50 : 0
  const cr = success && r(10) < 0.6 ? Math.floor(r(11) * 60000) : 0
  const cc = success && r(12) < 0.3 ? Math.floor(r(13) * 5000) : 0
  const fast = r(14) < 0.2
  // Client used the openai chat endpoint; anthropic-platform accounts serve it via conversion.
  const viaOpenAI = account.plugin_key === 'anthropic' && rnd(i * 13 + 99) < 0.2
  const price = priceFor(model)
  const requestId = 'req_' + hashOf('u' + i).slice(0, 16)
  // Client X-Request-Id (CONTRACTS §14.4): most SDK calls send one.
  const clientRequestId = r(20) < 0.7 ? `cli-${hashOf('c' + i).slice(0, 8)}-${hashOf('d' + i).slice(0, 4)}` : ''
  // The body's arrival (upload_ms) is part of the first token and the total.
  const upload = 20 + Math.floor(r(22) * r(22) * 2500)
  const native = NATIVE_ENDPOINT[account.upstream]
  const hook_decisions = [
    { plugin_key: 'guard', hook_id: 'content-filter', decision: blocked ? 'deny' : 'allow', latency_ms: 3 + Math.floor(r(15) * 20), note: blocked ? 'matched rule "no-secrets"' : undefined }
  ]
  const vars = { p: inTok, c: outTok, cr, cc, cc1h: 0 }
  const billing: any = (success && price && charge(price, { usage: vars, headers: fast ? { 'anthropic-beta': 'fast-mode' } : {}, at }, group.rate)) || { status: 'free' }
  const row: any = {
    id: 5000 + i,
    request_id: requestId,
    client_request_id: clientRequestId,
    user_id: user.id,
    user_email: user.email,
    // The console user spreads over its first two keys (the API key list's usage column).
    api_key_id: user.id === 1 ? 1 + (i % 2) : user.id * 10,
    api_key_name: `${user.name}-key`,
    client_ip: CLIENT_IPS[user.id][r(21) < 0.75 ? 0 : 1],
    group_id: group.id,
    group_name: group.name,
    account_id: blocked ? null : account.id,
    account_name: blocked ? null : account.name,
    // plugin_key/account_type: the account type; platform/protocol: the client endpoint.
    plugin_key: blocked ? '' : account.plugin_key,
    account_type: blocked ? '' : account.type,
    platform: native?.platform ?? (viaOpenAI ? 'openai' : 'anthropic'),
    protocol: native?.protocol ?? (viaOpenAI ? 'openai.chat' : 'anthropic.messages'),
    upstream_protocol: blocked ? '' : account.upstream,
    // Converted requests carry their conversion record (CONVERT-MATRIX §7); operators only.
    conversion: viaOpenAI && !blocked ? {
      v: 1, grade: i % 4 ? 'good' : 'fair', hops: ['openai.chat', 'anthropic.messages'], policy: 'allow',
      ...(i % 3 ? {} : { lost: { other: ['seed'], ...(i % 4 ? {} : { tool: ['tools[2]'] }) } })
    } : null,
    endpoint: native?.endpoint ?? (viaOpenAI ? '/v1/chat/completions' : '/v1/messages'),
    model,
    upstream_model: model,
    stream: r(16) < 0.7,
    status_code: success ? 200 : blocked ? 403 : 529,
    success,
    error_type: blocked ? 'blocked_by_hook' : failed ? 'upstream_overloaded' : '',
    error_message: blocked ? 'request rejected by guard' : failed ? 'Overloaded' : '',
    input_tokens: inTok,
    output_tokens: outTok,
    cache_read_tokens: cr,
    cache_creation_tokens: cc,
    cache_creation_1h_tokens: 0,
    total_cost: money(billing.total || 0),
    rate_multiplier: group.rate.toFixed(2),
    billing_status: billing.status,
    billing_mode: price?.mode,
    matched_tier: billing.tier || '',
    price_id: billing.status === 'billed' ? price?.id : null,
    expr_hash: billing.status === 'billed' ? price?.expr_hash : '',
    latency_ms: success ? upload + 800 + Math.floor(r(17) * 4000) : 120,
    first_token_ms: success ? upload + 300 + Math.floor(r(18) * 900) : undefined,
    upload_ms: upload,
    sticky_rule: 'claude-code-session',
    sticky_hit: r(19) < 0.7,
    hook_decisions,
    created_at: at.toISOString(),
    _detail: billing.detail,
    _price: billing.status === 'billed' && price ? { id: price.id, model: price.model, source: price.source } : null
  }
  usageRows.push(row)
}
// Two records with a model availability trace (anomalies.availability, detail only):
// every Claude subscription candidate blocked by its 7d Fable window (429, no account), and
// one candidate blocked while another served the request.
{
  const at = (min: number) => new Date(Date.now() - min * 60000)
  const unix = (d: Date, min: number) => Math.floor(d.getTime() / 1000) + min * 60
  const base = {
    user_id: 1, user_email: USERS[0].email, api_key_id: 1, api_key_name: `${USERS[0].name}-key`, client_ip: CLIENT_IPS[1][0],
    group_id: 2, group_name: 'vip', platform: 'anthropic', protocol: 'anthropic.messages', endpoint: '/v1/messages',
    model: 'claude-fable-4', upstream_model: 'claude-fable-4', stream: true, cache_read_tokens: 0, cache_creation_tokens: 0,
    cache_creation_1h_tokens: 0, rate_multiplier: '1.50', billing_mode: undefined, matched_tier: '', price_id: null, expr_hash: '',
    hook_decisions: [], upload_ms: 40, sticky_rule: 'claude-code-session', sticky_hit: false, _detail: null, _price: null
  }
  const blockedAt = at(3)
  usageRows.push({
    ...base, id: 4990, request_id: 'req_availability0001', client_request_id: '', created_at: blockedAt.toISOString(),
    account_id: null, account_name: null, plugin_key: '', account_type: '', upstream_protocol: '',
    status_code: 429, success: false, error_type: 'model_availability',
    error_message: 'the requested model is unavailable for these accounts, please retry later',
    input_tokens: 0, output_tokens: 0, total_cost: money(0), billing_status: 'free', latency_ms: 4,
    _anomalies: { availability: JSON.stringify({
      v: 1, outcome: 'blocked_all', candidates: 3, admitted: 0, retry_at: unix(blockedAt, 134),
      counts: { 'unavailable:quota_rejected:7d_fable': 3 },
      samples: [
        { account_id: 21, account_name: 'claude-key-1', model: 'claude-fable-4', decision: 'block', reason: 'unavailable:quota_rejected:7d_fable', retry_at: unix(blockedAt, 134) },
        { account_id: 22, account_name: 'claude-key-2', model: 'claude-fable-4', decision: 'block', reason: 'unavailable:quota_rejected:7d_fable', retry_at: unix(blockedAt, 391) },
        { account_id: 24, model: 'claude-fable-4', decision: 'block', reason: 'unavailable:quota_rejected:7d_fable', retry_at: unix(blockedAt, 1450) }
      ]
    }) }
  })
  const partialAt = at(9)
  usageRows.push({
    ...base, id: 4991, request_id: 'req_availability0002', client_request_id: '', created_at: partialAt.toISOString(),
    account_id: 22, account_name: 'claude-key-2', plugin_key: 'anthropic', account_type: 'apikey', upstream_protocol: 'anthropic.messages',
    status_code: 200, success: true, error_type: '', error_message: '',
    input_tokens: 1200, output_tokens: 300, total_cost: money(0), billing_status: 'free', latency_ms: 3200, first_token_ms: 900,
    _anomalies: { availability: JSON.stringify({
      v: 1, outcome: 'partial', candidates: 3, admitted: 2, retry_at: unix(partialAt, 72),
      counts: { 'unavailable:quota_rejected:7d_fable': 1, snapshot_unavailable: 1 },
      samples: [
        { account_id: 21, account_name: 'claude-key-1', model: 'claude-fable-4', decision: 'block', reason: 'unavailable:quota_rejected:7d_fable', retry_at: unix(partialAt, 72) },
        { account_id: 24, model: 'claude-fable-4', decision: 'fail_open', reason: 'snapshot_unavailable' }
      ],
      guard: [{ account_id: 24, model: 'claude-fable-4', decision: 'block', reason: 'guard_models_changed', attempt: 1 }]
    }) }
  })  // An unreported execution (design §8.5): the plugin's Execute returned no
  // usable usage, so the core wrote this free record and released the pre-charge.
  usageRows.push({
    ...base, id: 4989, request_id: 'req_unreported0001', client_request_id: '', created_at: at(15).toISOString(),
    account_id: 16, account_name: 'relay-1', plugin_key: 'relay', account_type: 'relay_key', upstream_protocol: 'anthropic.messages',
    status_code: 200, success: false, error_type: 'usage_unreported', error_message: 'plugin relay returned no usage for this execution',
    input_tokens: 0, output_tokens: 0, total_cost: money(0), billing_status: 'free', latency_ms: 2100,
    _anomalies: { usage_extract: 'unreported' }
  })
}
// Four records priced by group pricing, as mock/pricingStore.ts sets it up
// for the console user (segment VIP: ×0.9 on every group, a rate of its own
// of ×0.7 in the vip group, gpt-4o-mini at ×0.8 in the default group; the vip
// group sells claude-haiku-4-5 at a price of its own). One each of a
// multiplier rule, a custom price, the segment's own rate and its discount.
{
  const VIP = 2
  const haiku = configPrice('per_token', singleTier({ p: 0.8, c: 4, cr: 0.08, cc: 1 }))
  const tokens = { input: 12000, output: 1800 }
  const samples: Array<{ group: (typeof GROUPS)[number]; model: string; rate: number; terms: PricingTerms }> = [
    { group: GROUPS[0], model: 'gpt-4o-mini', rate: 0.9, terms: { segment_id: VIP, rate_source: 'discount', rule: { source: 'segment', scale: 0.8 } } },
    { group: GROUPS[1], model: 'claude-haiku-4-5', rate: 0.7, terms: { segment_id: VIP, rate_source: 'segment', rule: { source: 'group', custom: haiku } } },
    { group: GROUPS[1], model: 'claude-sonnet-4-5', rate: 0.7, terms: { segment_id: VIP, rate_source: 'segment' } },
    { group: GROUPS[0], model: 'claude-sonnet-x', rate: 0.9, terms: { segment_id: VIP, rate_source: 'discount' } }
  ]
  samples.forEach(({ group, model, rate, terms }, i) => {
    const price = priceFor(model)!
    const at = new Date(Date.now() - (15 + i * 10) * 60000)
    const billing = charge(price, { usage: { p: tokens.input, c: tokens.output }, at }, rate, terms)!
    const account = ACCOUNTS.find((a) => (OWN_MODELS[a.plugin_key] ?? MODELS).includes(model))!
    const native = NATIVE_ENDPOINT[account.upstream]
    const custom = terms.rule?.custom
    usageRows.push({
      id: 4992 + i, request_id: `req_grouppricing000${i + 1}`, client_request_id: '', created_at: at.toISOString(),
      user_id: 1, user_email: USERS[0].email, api_key_id: 1, api_key_name: `${USERS[0].name}-key`, client_ip: CLIENT_IPS[1][0],
      group_id: group.id, group_name: group.name, account_id: account.id, account_name: account.name, plugin_key: account.plugin_key, account_type: account.type,
      platform: native?.platform ?? 'anthropic', protocol: native?.protocol ?? 'anthropic.messages', upstream_protocol: account.upstream, conversion: null,
      endpoint: native?.endpoint ?? '/v1/messages', model, upstream_model: model, stream: true, status_code: 200, success: true, error_type: '', error_message: '',
      input_tokens: tokens.input, output_tokens: tokens.output, cache_read_tokens: 0, cache_creation_tokens: 0, cache_creation_1h_tokens: 0,
      total_cost: money(billing.total), rate_multiplier: rate.toFixed(2), billing_status: billing.status, billing_mode: price.mode, matched_tier: billing.tier,
      // The price is the global one whatever rule applied; the expression is the one that was evaluated.
      price_id: price.id, expr_hash: custom ? remember(custom.expression) : price.expr_hash,
      latency_ms: 2600, first_token_ms: 700, upload_ms: 40, sticky_rule: 'claude-code-session', sticky_hit: true, hook_decisions: [],
      _detail: billing.detail, _price: { id: price.id, model: price.model, source: price.source }
    })
  })
}
// A Messages request that declared an advisor model: the conversation at its
// model's price, the advisor's tokens as additional usage at the advisor
// model's (12000*1 + 1800*5 = 21000 and 3000*15 + 400*75 = 75000 micro-USD).
{
  const at = new Date(Date.now() - 6 * 60000)
  const chat = { model: 'claude-haiku-4-5', expression: 'p*1 + c*5' }
  const advisor = { model: 'claude-opus-4-1', expression: 'p*15 + c*75' }
  const term = (label: string, quantity: number, rate: number, cost: string) => ({ tier: '', label, quantity, rate, expr: `${label}*${rate}`, cost })
  const advisorTokens = { Input: 3000, Output: 400, CacheRead: 0, CacheCreation: 0, CacheCreation1h: 0 }
  usageRows.push({
    id: 4996, request_id: 'req_advisor0001', client_request_id: '', created_at: at.toISOString(),
    user_id: 1, user_email: USERS[0].email, api_key_id: 1, api_key_name: `${USERS[0].name}-key`, client_ip: CLIENT_IPS[1][0],
    group_id: GROUPS[0].id, group_name: GROUPS[0].name, account_id: 12, account_name: 'claude-main', plugin_key: 'anthropic', account_type: 'apikey',
    platform: 'anthropic', protocol: 'anthropic.messages', upstream_protocol: 'anthropic.messages', conversion: null, endpoint: '/v1/messages',
    model: chat.model, upstream_model: chat.model, stream: true, status_code: 200, success: true, error_type: '', error_message: '',
    input_tokens: 12000, output_tokens: 1800, cache_read_tokens: 0, cache_creation_tokens: 0, cache_creation_1h_tokens: 0,
    total_cost: money(0.096), rate_multiplier: '1.00', billing_status: 'billed', billing_mode: 'expression', matched_tier: '',
    price_id: 2, expr_hash: remember(chat.expression), latency_ms: 18400, first_token_ms: 900, upload_ms: 40,
    sticky_rule: '', sticky_hit: false, hook_decisions: [],
    _detail: {
      expr_version: 1, tier: '', rules: [], breakdown: { vars: { p: 12000, c: 1800, len: 12000 }, items: [term('p', 12000, 1, '0.012'), term('c', 1800, 5, '0.009')], subtotal: '0.021', base: '0.021', rules_multiplier: '1' }, cost: '0.096', rate_multiplier: '1', total_cost: '0.096', primary_cost: '0.021', primary_total_cost: '0.021',
      additional: [{ kind: 'advisor', model: advisor.model, price_id: 5, expression_hash: remember(advisor.expression),
        detail: { expr_version: 1, tier: '', rules: [], breakdown: { vars: { p: 3000, c: 400, len: 3000 }, items: [term('p', 3000, 15, '0.045'), term('c', 400, 75, '0.03')], subtotal: '0.075', base: '0.075', rules_multiplier: '1' }, cost: '0.075', rate_multiplier: '1', total_cost: '0.075' } }],
      inputs: { semantics: 'inclusive', additional: [{ Kind: 'advisor', Model: advisor.model, UsageSemantics: 'inclusive', Tokens: advisorTokens, RateMultiplier: '1' }] }
    },
    _price: null
  })
}
usageRows.sort((a, b) => b.created_at.localeCompare(a.created_at))

// Retried requests (attempt_trail, CONTRACTS §98), among the console user's
// recent anthropic / relay requests: switched once, the same account twice
// and then another, failed on every account, a chain long enough to fold,
// and a row from before the trail (only the count). The trail is stored as
// the list sends it, by account ID; the detail adds the names.
{
  type Step = Record<string, any>
  const upstream = (account_id: number, status: number, ms: number, message: string, next?: string): Step =>
    ({ account_id, status, error_type: 'upstream_error', ms, message, ...(next ? { next } : {}) })
  const overloaded = 'overloaded_error: Overloaded'
  const reset = 'upstream connect error or disconnect/reset before headers'
  const trails: Array<Step[] | null> = [
    [upstream(12, 529, 1180, overloaded, 'switch'), { account_id: 13, status: 200, ms: 3040 }],
    [upstream(16, 502, 640, reset, 'same'), upstream(16, 502, 702, reset, 'switch'), { account_id: 12, status: 200, ms: 2950 }],
    [upstream(12, 529, 1210, overloaded, 'switch'), upstream(13, 529, 980, overloaded, 'switch'), upstream(16, 503, 1530, 'service unavailable')],
    [12, 13, 16, 13].map((id, i) => upstream(id, 529, 900 + i * 110, overloaded, 'switch')).concat({ account_id: 12, status: 200, ms: 2710 }),
    null
  ]
  const picked = usageRows.filter((u) => u.user_id === 1 && u.id >= 5000 && ['anthropic', 'relay'].includes(u.plugin_key)).slice(0, trails.length)
  picked.forEach((u, k) => {
    const steps = trails[k]
    // Within the last hour, so they open on the request log's default range.
    u.created_at = new Date(Date.now() - (3 + k * 9) * 60000).toISOString()
    if (!steps) {
      u.attempts = 2
      return
    }
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
  })
  usageRows.sort((a, b) => b.created_at.localeCompare(a.created_at))
}

// Ledger: usage debits + a few admin credits, oldest first to compute balance_after.
{
  const running = new Map<number, number>()
  const events: any[] = []
  for (const u of USERS) events.push({ user: u, delta: 20, kind: 'admin_adjust', at: new Date(Date.now() - 8 * 86400000).toISOString(), note: 'top-up', ref_type: '', ref_id: '', operator_id: 1 })
  for (const u of usageRows) {
    if (u.billing_status !== 'billed') continue
    const user = USERS.find((x) => x.id === u.user_id)!
    events.push({ user, delta: -Number(u.total_cost), kind: 'usage', at: u.created_at, note: '', ref_type: 'usage', ref_id: u.request_id, usage: u })
  }
  events.push({ user: USERS[1], delta: 0.5, kind: 'refund', at: now(-3600), note: 'refund for failed request', ref_type: 'usage', ref_id: 'req_refund0001', operator_id: 1 })
  events.push({ user: USERS[2], delta: 5, kind: 'plugin_credit', at: now(-7200), note: 'promo code', ref_type: 'order', ref_id: 'ord_778', plugin_key: 'payments' })
  events.sort((a, b) => a.at.localeCompare(b.at))
  for (const e of events) {
    const bal = (running.get(e.user.id) || 0) + e.delta
    running.set(e.user.id, bal)
    const id = 88000 + ledgerRows.length
    if (e.usage) e.usage.ledger_id = id
    ledgerRows.push({
      id,
      user_id: e.user.id,
      user_email: e.user.email,
      user_name: e.user.name,
      delta: money(e.delta),
      balance_after: money(bal),
      kind: e.kind,
      ref_type: e.ref_type,
      ref_id: e.ref_id,
      idempotency_key: e.kind === 'usage' ? `usage:${e.ref_id}` : `mock:${id}`,
      operator_id: e.operator_id ?? null,
      plugin_key: e.plugin_key ?? null,
      note: e.note,
      created_at: e.at
    })
  }
  ledgerRows.reverse()
}

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

/** The conversion= filter: by grade, any converted grade, or with losses. */
function conversionMatch(c: any, want: string) {
  if (want === 'lossy') return !!(c?.lost || c?.response_lost)
  if (want === 'native') return !c || c.grade === 'native'
  if (want === 'converted') return ['good', 'fair', 'discouraged'].includes(c?.grade)
  return c?.grade === want
}

/** The filters that match a record's field exactly. */
const EXACT_FILTERS = [
  'user_id', 'group_id', 'group_name', 'api_key_id', 'account_id', 'client_request_id', 'client_ip', 'stream', 'billing_status', 'model',
  'platform', 'protocol', 'endpoint', 'plugin_key', 'account_type', 'error_type', 'request_id', 'status_code'
]

function usageFilter(rows: any[], q: Record<string, string>) {
  return rows.filter((u) => {
    if (q.from && u.created_at < q.from) return false
    if (q.to && u.created_at > q.to) return false
    if (EXACT_FILTERS.some((k) => q[k] && String(u[k]) !== q[k])) return false
    if (q.model_contains && !u.model.toLowerCase().includes(q.model_contains.toLowerCase())) return false
    if (q.api_key_name && !u.api_key_name.toLowerCase().includes(q.api_key_name.toLowerCase())) return false
    if (q.conversion && !conversionMatch(u.conversion, q.conversion)) return false
    if (q.retried === 'true' && !((u.attempts ?? 1) > 1)) return false
    // Like the server: a request that never reached an account (attempts 0) is in neither.
    if (q.retried === 'false' && (u.attempts ?? 1) !== 1) return false
    if (q.success === 'true' && !u.success) return false
    if (q.success === 'false' && u.success) return false
    return true
  })
}

/**
 * A detail's metrics. Explicit synthetic TTL evidence lets the detail preview
 * exercise both the compact summary and its expanded counters without using
 * production data.
 */
function usageMetrics(u: any) {
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

/** Calendar parts of an instant in an IANA zone (the summary's tz=). */
function zoned(iso: string, tz: string) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: tz || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(iso))
  const p = Object.fromEntries(parts.map((x) => [x.type, x.value]))
  return { day: `${p.year}-${p.month}-${p.day}`, hour: `${p.year}-${p.month}-${p.day} ${p.hour}:00` }
}

function usageSummary(rows: any[], query: Record<string, string>) {
  const q = { ...query }
  if (!q.from && !q.to) q.from = new Date(Date.now() - 30 * 86400000).toISOString()
  const by = q.group_by || 'day'
  const tz = q.tz || 'UTC'
  const keyOf = (u: any): string => {
    switch (by) {
      case 'model': return u.model
      case 'upstream_model': return u.upstream_model
      case 'user': return String(u.user_id)
      case 'account': return String(u.account_id ?? '')
      case 'ip': return u.client_ip
      case 'api_key': return String(u.api_key_id)
      case 'group': return u.group_name
      case 'endpoint': return u.endpoint
      case 'platform': return u.platform
      case 'protocol': return u.protocol
      case 'none': return ''
      case 'hour': return zoned(u.created_at, tz).hour
      default: return zoned(u.created_at, tz).day
    }
  }
  const map = new Map<string, any>()
  if (by === 'none') map.set('', null)
  for (const u of usageFilter(rows, q)) {
    const key = keyOf(u)
    const s = map.get(key) || { key, requests: 0, success: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, prompt_tokens: 0, total_cost: 0, standard_cost: 0, _lat: 0, _ft: 0, _n: 0 }
    if (by === 'user') s.user_email = u.user_email
    if (by === 'account') s.account_name = u.account_name
    if (by === 'ip' && (!s.last_at || u.created_at > s.last_at)) s.last_at = u.created_at
    s.requests++
    if (u.success) {
      s.success++
      s._n++
      s._lat += u.latency_ms || 0
      s._ft += u.first_token_ms || 0
    }
    s.input_tokens += u.input_tokens
    s.output_tokens += u.output_tokens
    s.cache_read_tokens += u.cache_read_tokens
    s.cache_creation_tokens += u.cache_creation_tokens + (u.cache_creation_1h_tokens || 0)
    // The mock generator's input_tokens are the exclusive pricing variable p.
    s.prompt_tokens += u.input_tokens + u.cache_read_tokens + u.cache_creation_tokens + (u.cache_creation_1h_tokens || 0)
    s.total_cost += Number(u.total_cost)
    s.standard_cost += baseCost(u)
    map.set(key, s)
  }
  const empty = { key: '', requests: 0, success: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, prompt_tokens: 0, total_cost: 0, standard_cost: 0, _lat: 0, _ft: 0, _n: 0 }
  const out = [...map.values()].map((x) => {
    const { _lat, _ft, _n, ...s } = x || empty
    return {
      ...s,
      total_tokens: s.input_tokens + s.output_tokens + s.cache_read_tokens + s.cache_creation_tokens,
      latency_ms_avg: _n ? Math.round(_lat / _n) : 0,
      first_token_ms_avg: _n ? Math.round(_ft / _n) : 0,
      total_cost: money(s.total_cost),
      standard_cost: money(s.standard_cost)
    }
  })
  if (by === 'ip') return out.sort((a, b) => b.requests - a.requests || a.key.localeCompare(b.key)).slice(0, 100)
  return q.order ? ranked(out, q.order, Number(q.limit) || 0) : out.sort((a, b) => a.key.localeCompare(b.key))
}

/** order=cost|requests|tokens&limit=n (CONTRACTS §66.6): the top n, the rest folded into key __other__. */
function ranked(rows: any[], order: string, limit: number) {
  const metric = (r: any) => (order === 'requests' ? r.requests : order === 'tokens' ? r.total_tokens : Number(r.total_cost))
  const sorted = [...rows].sort((a, b) => metric(b) - metric(a) || a.key.localeCompare(b.key))
  if (!limit || sorted.length <= limit) return sorted
  const rest = sorted.slice(limit)
  const other: any = { key: '__other__', requests: 0, success: 0, input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, prompt_tokens: 0, total_tokens: 0, total_cost: 0, standard_cost: 0 }
  for (const r of rest) {
    for (const k of ['requests', 'success', 'input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_creation_tokens', 'prompt_tokens', 'total_tokens']) other[k] += r[k]
    other.total_cost += Number(r.total_cost)
    other.standard_cost += Number(r.standard_cost)
  }
  return [...sorted.slice(0, limit), { ...other, total_cost: money(other.total_cost), standard_cost: money(other.standard_cost) }]
}

function overviewOf(rows: any[], tz: string) {
  const today = zoned(new Date().toISOString(), tz).day
  const yesterday = zoned(new Date(Date.now() - 86400000).toISOString(), tz).day
  const totals = (part: any[]) => {
    const { key: _k, total_tokens: _t, ...s } = usageSummary(part, { group_by: 'none', from: '1970-01-01T00:00:00Z' })[0]
    return s
  }
  const recent = rows.filter((u) => Date.now() - Date.parse(u.created_at) < 5 * 60000)
  return {
    tz,
    today: totals(rows.filter((u) => zoned(u.created_at, tz).day === today)),
    yesterday: totals(rows.filter((u) => zoned(u.created_at, tz).day === yesterday)),
    last_30d: totals(rows),
    last_5m: { requests: recent.length, tokens: recent.reduce((a, u) => a + u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_creation_tokens, 0) }
  }
}

/** The export's call chain (CONTRACTS §98): "#16×2 → #12" for an operator, the count for the
 *  caller's own records and for a row without a trail; empty for a request sent once. */
function callChain(u: any, self: boolean, zh: boolean): string {
  if (!(u.attempts > 1)) return ''
  if (self || !u.attempt_trail) return zh ? `${u.attempts} 次` : `${u.attempts} attempts`
  const runs: Array<{ id: number; times: number }> = []
  for (const s of u.attempt_trail.steps) {
    const last = runs[runs.length - 1]
    if (last?.id === s.account_id) last.times++
    else runs.push({ id: s.account_id, times: 1 })
  }
  const omitted = u.attempt_trail.omitted ? [`+${u.attempt_trail.omitted}`] : []
  return [...runs.map((r) => `#${r.id}${r.times > 1 ? `×${r.times}` : ''}`), ...omitted].join(' → ')
}

/** GET …/usage/export (CONTRACTS §66.5): a short CSV with the BOM, like the server. */
function usageCSV(rows: any[], q: Record<string, string>, view: UsageView) {
  const zh = q.lang === 'zh'
  const cols: Array<[string, string, (u: any) => unknown, UsageView[]?]> = [
    ['时间', 'Time', (u) => u.created_at],
    ['请求 ID', 'Request ID', (u) => u.request_id],
    ['用户', 'User', (u) => u.user_email, ['all']],
    ['API 密钥', 'API key', (u) => u.api_key_name, ['all', 'self']],
    ['账号', 'Account', (u) => u.account_name ?? '', ['all', 'account']],
    ['模型', 'Model', (u) => u.model],
    ['上游模型', 'Upstream model', (u) => u.upstream_model, ['all', 'account']],
    ['状态', 'Status', (u) => u.status_code],
    ['输入', 'Input', (u) => u.input_tokens],
    ['输出', 'Output', (u) => u.output_tokens],
    ['缓存读', 'Cache read', (u) => u.cache_read_tokens],
    ['缓存写', 'Cache write', (u) => u.cache_creation_tokens],
    // The account view bills nothing: the list price.
    ['费用', 'Cost', (u) => (view === 'account' ? money(baseCost(u)) : u.total_cost)],
    ['调用链路', 'Call chain', (u) => callChain(u, view !== 'all', zh)]
  ]
  const shown = cols.filter((c) => !c[3] || c[3].includes(view))
  const cell = (v: unknown) => {
    let s = String(v ?? '')
    if (/^[=+\-@\t\r]/.test(s)) s = "'" + s
    return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const lines = [shown.map((c) => (zh ? c[0] : c[1])).join(',')]
  for (const u of usageFilter(rows, q).slice(0, 1000)) lines.push(shown.map((c) => cell(c[2](u))).join(','))
  const body = Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from(lines.join('\r\n') + '\r\n', 'utf8')])
  const stamp = new Date().toISOString().replace(/[-:]/g, '').replace('T', '_').slice(0, 15)
  return {
    __status: 200,
    __headers: { 'Content-Type': 'text/csv; charset=utf-8', 'Content-Disposition': `attachment; filename="usage_${stamp}.csv"` },
    __bytes: body,
    body: undefined
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
// GET /usage/realtime, /me/usage/realtime (CONTRACTS §66.7): the last minute under the filters.
function realtimeOf(rows: typeof usageRows, q: Record<string, string>) {
  const since = Date.now() - 60_000
  const recent = usageFilter(rows, q).filter((u) => new Date(u.created_at).getTime() >= since)
  const ok = recent.filter((u) => u.success)
  return { rpm: ok.length, tpm: ok.reduce((n, u) => n + u.input_tokens + u.output_tokens, 0), recent_failures: recent.length - ok.length }
}
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

// ------------------------------------------------------------------ account usage

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

// ------------------------------------------------------------------ ledger

function ledgerFilter(rows: any[], q: Record<string, string>) {
  return rows.filter((l) => {
    if (q.user_id && String(l.user_id) !== q.user_id) return false
    if (q.kind && l.kind !== q.kind) return false
    if (q.from && l.created_at < q.from) return false
    if (q.to && l.created_at > q.to) return false
    return true
  })
}

on('GET', '/ledger', (req) => paginate(ledgerFilter(ledgerRows, req.query), req.query))
on('GET', '/me/ledger', (req) => paginate(ledgerFilter(ledgerRows.filter((l) => l.user_id === 1), req.query), req.query))

// ------------------------------------------------------------------ settings

const billingSettings = {
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
  const fields: Array<{ field: string; code: string; message: string }> = []
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
  const fields: Array<{ field: string; code: string; message: string }> = []
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
  const fields: Array<{ field: string; code: string; message: string }> = []
  if (!['rpm', 'concurrency', 'tpm', 'sessions'].includes(b.metric)) fields.push({ field: 'metric', code: 'invalid', message: 'invalid value' })
  if (fields.length) return fail(400, 'invalid_argument', 'invalid scheduling settings', { fields })
  return Object.assign(schedulingSettings, { metric: b.metric, limit_load: b.limit_load, model_limits: b.model_limits })
})

// ------------------------------------------------------------------ sticky rules

const stickyRules: any[] = [
  {
    id: 1,
    name: 'claude-code-session',
    source: 'builtin',
    enabled: true,
    priority: 100,
    match: { protocols: ['anthropic.messages'], models: [], user_agent_contains: [] },
    key_sources: [{ type: 'body', path: 'metadata.user_id' }],
    value_regex: '',
    ttl_seconds: 3600,
    key_includes: ['group', 'model', 'rule'],
    on_failure: 'failover'
  },
  {
    id: 2,
    name: 'openai-prompt-cache-key',
    source: 'builtin',
    enabled: true,
    priority: 101,
    match: { protocols: ['openai.chat', 'openai.responses', 'openai.responses_ws'], models: [], user_agent_contains: [] },
    key_sources: [{ type: 'body', path: 'prompt_cache_key' }],
    value_regex: '',
    ttl_seconds: 3600,
    key_includes: ['group', 'model', 'rule'],
    on_failure: 'failover'
  },
  {
    id: 3,
    name: 'cli-users-stick',
    source: 'admin',
    enabled: true,
    priority: 200,
    match: { protocols: [], models: ['claude-sonnet-*'], user_agent_contains: ['claude-cli'] },
    key_sources: [{ type: 'user' }],
    value_regex: '',
    ttl_seconds: 1800,
    key_includes: ['group', 'model'],
    on_failure: 'stick'
  }
]
// Default definitions of the built-in rules, for "restore default".
const stickyDefaults: Record<string, any> = Object.fromEntries(
  stickyRules.filter((r) => r.source === 'builtin').map((r) => [r.name, JSON.parse(JSON.stringify(r))])
)
const stickyStats: Record<string, { hits: number; misses: number; rebinds: number }> = {
  'claude-code-session': { hits: 18234, misses: 2311, rebinds: 142 },
  'openai-prompt-cache-key': { hits: 0, misses: 0, rebinds: 0 },
  'cli-users-stick': { hits: 912, misses: 88, rebinds: 3 }
}

on('GET', '/sticky-rules/stats', () => stickyRules.map((r) => ({ rule: r.name, ...(stickyStats[r.name] || { hits: 0, misses: 0, rebinds: 0 }) })))
on('GET', '/sticky-rules', (req) => paginate(stickyRules, req.query))
on('POST', '/sticky-rules', (req) => {
  const b = req.body || {}
  if (!b.name) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'required', message: 'Name is required' }] })
  if (stickyRules.some((r) => r.name === b.name)) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'duplicate', message: 'Name already used' }] })
  if ((b.key_sources || []).some((k: any) => !['body', 'header', 'api_key', 'user'].includes(k.type))) {
    return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'key_sources[0].type', code: 'invalid', message: 'type must be body, header, api_key or user' }] })
  }
  const rule = { ...b, id: nextId(), source: 'admin' }
  stickyRules.push(rule)
  return rule
})
on('PATCH', '/sticky-rules/:id', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  const b = req.body || {}
  if (r.source !== 'admin' && b.name !== undefined && b.name !== r.name) {
    return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'invalid', message: 'built-in rules cannot be renamed' }] })
  }
  const def = (x: any) => JSON.stringify([x.match, x.key_sources, x.value_regex, x.ttl_seconds, x.key_includes, x.on_failure])
  const before = def(r)
  Object.assign(r, b)
  if (r.source !== 'admin' && def(r) !== before) r.customized = true
  return r
})
on('DELETE', '/sticky-rules/:id', (req) => {
  const i = stickyRules.findIndex((x) => x.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'rule not found')
  if (stickyRules[i].source !== 'admin') return fail(400, 'invalid_argument', 'built-in rules cannot be deleted; disable them instead')
  stickyRules.splice(i, 1)
  return noContent()
})
on('POST', '/sticky-rules/:id/reset', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  if (r.source === 'admin') return fail(400, 'invalid_argument', 'only built-in rules have a default to restore')
  const d = stickyDefaults[r.name]
  Object.assign(r, { match: d.match, key_sources: d.key_sources, value_regex: d.value_regex, ttl_seconds: d.ttl_seconds, key_includes: d.key_includes, on_failure: d.on_failure, customized: false })
  return r
})
on('POST', '/sticky-rules/:id/flush', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  if (!(r.key_includes || []).includes('rule')) return fail(409, 'conflict', 'bindings are shared with other rules and cannot be flushed on their own')
  return { deleted: Math.floor(Math.random() * 40) }
})
