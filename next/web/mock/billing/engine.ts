// A tiny expression engine (mock only): evaluates a price expression as
// JavaScript, traces its surcharge rules and itemizes its cost.
import { PRICE_KEYS } from '../../src/views/prices/priceExpr'
import { money } from './shared'

type TokenVar = (typeof PRICE_KEYS)[number]
export type Usage = Record<TokenVar | 'len', number>

export const fmtN = (n: number) => String(Number(n.toPrecision(12)))

export const ALLOWED_IDENTS = new Set(
  'p c cr cc cc1h len img img_o ai ao tier flat param header has hour minute weekday day month max min abs ceil floor u true false nil v1'.split(' ')
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
export function surcharges(src: string) {
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

export interface EvalInput {
  usage: Partial<Usage>
  headers?: Record<string, string>
  params?: Record<string, unknown>
  metrics?: Record<string, unknown>
  at?: Date
}

/** A time in zone tz at `at`, as Intl formats it (UTC when it knows no such zone). */
function timeIn(at: Date, tz: string, opt: Intl.DateTimeFormatOptions): string {
  try {
    return new Intl.DateTimeFormat('en-US', { ...opt, timeZone: tz }).format(at)
  } catch {
    return new Intl.DateTimeFormat('en-US', { ...opt, timeZone: 'UTC' }).format(at)
  }
}

/** The time functions of an expression, reading `at`. */
const timeFns = (at: Date) => ({
  hour: (tz: string) => Number(timeIn(at, tz, { hour: 'numeric', hourCycle: 'h23' })),
  minute: (tz: string) => Number(timeIn(at, tz, { minute: 'numeric' })),
  weekday: (tz: string) => ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(timeIn(at, tz, { weekday: 'short' })),
  day: (tz: string) => Number(timeIn(at, tz, { day: 'numeric' })),
  month: (tz: string) => Number(timeIn(at, tz, { month: 'numeric' }))
})

/** The functions an expression calls: tier() keeps the name of the tier it prices in `seen`, __rule() whether rule i held in `hits`. */
function exprLib(input: EvalInput, hits: boolean[], seen: { tier: string }): Record<string, unknown> {
  const headers = Object.fromEntries(Object.entries(input.headers || {}).map(([k, v]) => [k.toLowerCase(), String(v)]))
  return {
    tier: (name: string, v: number) => ((seen.tier = name), v),
    flat: (x: number) => x * 1e6, // everything is scaled back by 1e-6 below
    param: (path: string) => String(path).split('.').reduce<any>((o, k) => (o == null ? o : o[k]), input.params || {}) ?? null,
    header: (name: string) => headers[String(name).toLowerCase()] ?? '',
    has: (s: unknown, sub: unknown) => String(s ?? '').includes(String(sub)),
    ...timeFns(input.at || new Date()),
    max: Math.max,
    min: Math.min,
    abs: Math.abs,
    ceil: Math.ceil,
    floor: Math.floor,
    u: (k: string) => (input.metrics || {})[k] ?? 0, // an absent usage field reads 0
    nil: null,
    __rule: (i: number, v: unknown) => ((hits[i] = !!v), v)
  }
}

function run(part: string, input: EvalInput, tokens: Usage, hits: boolean[] = []): { value: number; tier: string } {
  const seen = { tier: '' }
  const lib = exprLib(input, hits, seen)
  const names = [...PRICE_KEYS, 'len', ...Object.keys(lib)]
  const values = [...PRICE_KEYS.map((k) => tokens[k]), tokens.len, ...Object.values(lib)]
  const fn = new Function(...names, `"use strict"; return (${jsStrings(part)});`)
  const v = fn(...values)
  return { value: typeof v === 'number' ? v : Number(v), tier: seen.tier }
}

/**
 * Evaluates an expression: token variables are raw token counts and
 * coefficients are USD per 1M, so the result is divided by 1e6 (flat()
 * pre-multiplies to compensate). Every `cond ? <number> : 1` is a surcharge
 * rule; the subtotal and its items are those of the expression with the
 * rules taken out.
 */
export function evaluate(expression: string, input: EvalInput) {
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

export type Evaluation = ReturnType<typeof evaluate>

export function tryEvaluate(expression: string, input: EvalInput) {
  try {
    return evaluate(expression, input)
  } catch {
    return null
  }
}

/** The breakdown of an evaluation, as billing_detail and a preview show it. */
export const breakdownOf = (ev: Evaluation) => ({
  vars: ev.tokens,
  tiers: ev.tier ? [ev.tier] : [],
  items: ev.items,
  subtotal: money(ev.subtotal),
  base: money(ev.cost),
  rules_multiplier: ev.multiplier
})
