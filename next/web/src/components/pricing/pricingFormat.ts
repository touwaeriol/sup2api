// What the pricing views show of a rate, a price rule and a price: pure
// functions shared by the admin editors and the user's own pages.
import type { EffectiveRate, PriceDef, PriceRule } from '@/api/pricing'
import { TOKEN_VARS, fmt, num, summarize, tokenPricesOf, type TokenPrices } from '@/views/prices/priceExpr'

export type Translate = (key: string, params?: Record<string, unknown>) => string

/** "×0.8": a rate as every pricing view writes it. */
export function rateText(v: string | number | null | undefined): string {
  return v === null || v === undefined || v === '' ? '—' : `×${Number(v)}`
}

/** The largest rate, at the four decimals the server keeps of one (numeric(10,4)). */
const MAX_RATE = 999999.9999

/** A rate or a multiplier as typed in a form: a non-negative decimal of at most four decimals, below 1000000. */
export function validRate(v: string | null | undefined): boolean {
  return !!v && /^\d+(\.\d{1,4})?$/.test(v.trim()) && Number(v) <= MAX_RATE
}

/** A rate as typed, once it is a valid one (trimmed); null until then. */
export function typedRate(v: string | null | undefined): string | null {
  const rate = v?.trim()
  return rate && validRate(rate) ? rate : null
}

/** Rates that are the same number however they are written; one still being typed is only like itself. */
export function sameRate(a: string | null, b: string | null): boolean {
  return validRate(a) && validRate(b) ? Number(a) === Number(b) : a === b
}

/**
 * The levels of a rate, the most specific first: a segment's rate for the
 * model in the group, the segment's rate in the group, the group's rate for
 * the model, the group's base rate. A level left out or null is not set.
 */
export interface RateLevels {
  segment_model?: string | null
  segment?: string | null
  group_model?: string | null
  group: string
}

const LEVELS = ['segment_model', 'segment', 'group_model'] as const

/**
 * The rate that applies: the most specific level set, alone (rates never
 * multiply), as the server resolves it. A level set to a rate still being
 * typed applies with no value yet.
 */
export function effectiveRate(levels: RateLevels): EffectiveRate {
  const source = LEVELS.find((k) => levels[k] !== null && levels[k] !== undefined) ?? 'group'
  const value = source === 'group' ? levels.group : (levels[source] as string)
  return { value: validRate(value) ? value.trim() : '', source, group_default: levels.group }
}

/**
 * The list price a rule gives over the global price `base`, in the shape the
 * server returns one: a model price is a price of its own; the multiplier of
 * a record priced before model rates goes beside the global price, which
 * stays as it is. So a price being edited and a saved one are shown through
 * the same priceCells. A multiplier still being typed leaves the price as it
 * is. Without a global price no rule applies: there is no price.
 */
export function applyRule(base: PriceDef | null, rule: PriceRule | null): PriceDef | null {
  if (!rule || !base) return base
  if (rule.kind === 'custom') return { mode: rule.mode, config: rule.config ?? {}, expression: rule.expression ?? '' }
  return validRate(rule.multiplier) ? { ...base, multiplier: rule.multiplier } : base
}

/** The cells of a price in a table: a per-request price, the tokens' prices (the rest of a tiered price: its last tier), or neither. */
export interface PriceCells {
  /** Null while the price of a draft is blank, as a token price is. */
  perRequest?: number | null
  tokens?: TokenPrices
  /** Tiers; 1 when the price has none. */
  tiers: number
  /** An expression the console cannot break down. */
  opaque?: boolean
  /** Of an opaque price under a multiplier rule: the multiplier, which no cell can take in. */
  multiplier?: string
}

/**
 * The cells of a price as it stands, whatever multiplier is on it: a visual
 * price shows its rest, the last tier, which applies when no tier before it
 * matches (the first that matches applies).
 */
function ownCells(def: PriceDef): PriceCells {
  const s = summarize(def.mode, def.config)
  if (s.kind === 'expression') return { tiers: 1, opaque: true }
  const { rest, tiers } = s
  // A draft's blank price is no price yet, not 0.
  if (rest.unit === 'request') return { perRequest: rest.price === null || rest.price === undefined ? null : num(rest.price), tiers }
  return { tokens: tokenPricesOf(rest), tiers }
}

/** A product of multipliers as shown: six decimals at most. */
export const round = (n: number) => Number(n.toFixed(6))

/**
 * What every pricing view shows of a price: its cells, times the multiplier
 * a multiplier rule put on it (PriceDef.multiplier). This is the one place a
 * multiplier becomes numbers: neither a config nor an expression is
 * rewritten for it. A price that cannot be broken down keeps the multiplier
 * beside it.
 */
export function priceCells(def: PriceDef | null | undefined): PriceCells {
  if (!def) return { tiers: 1, opaque: true }
  const cells = ownCells(def)
  if (def.multiplier === undefined) return cells
  if (cells.opaque) return { ...cells, multiplier: def.multiplier }
  const k = num(def.multiplier, 1)
  const times = (v: number | null) => (v === null ? null : round(v * k))
  if (cells.perRequest !== undefined) return { ...cells, perRequest: times(cells.perRequest) }
  const { tokens } = cells
  return tokens ? { ...cells, tokens: Object.fromEntries(TOKEN_VARS.map((v) => [v, times(tokens[v])])) as TokenPrices } : cells
}

/**
 * What is paid at a list price: the price times `rate`, which joins the
 * multiplier a multiplier rule put on it, so the cells show the product
 * (priceCells). Null when the rate is not one yet.
 */
export function timesRate(def: PriceDef, rate: string): PriceDef | null {
  if (!validRate(rate)) return null
  const k = round(num(def.multiplier, 1) * Number(rate))
  return def.multiplier === undefined && k === 1 ? def : { ...def, multiplier: String(k) }
}

/** "$3": an amount in USD as every price view writes it, at eight significant digits (a small price never reads $0) and without an exponent; '—' when unset. */
export function usd(v: number | null | undefined): string {
  return v === null || v === undefined ? '—' : `$${fmt(Number(num(v).toPrecision(8)))}`
}

/** "$3": a price per 1M tokens or per request, times `rate`. */
export function priceText(v: number | null | undefined, rate = 1): string {
  return v === null || v === undefined ? '—' : usd(num(v) * rate)
}

/** "$3 / $15": input and output of a price, or its per-request price; '' when it cannot be broken down. */
export function priceBrief(def: PriceDef | null | undefined, rate = 1): string {
  const c = priceCells(def)
  if (c.perRequest !== undefined) return priceText(c.perRequest, rate)
  return c.tokens ? `${priceText(c.tokens.p, rate)} / ${priceText(c.tokens.c, rate)}` : ''
}

/**
 * "2 tiers · 1 surcharge rule": what a price has beyond the cells of its
 * rest, its tiers when it has several and the surcharge rules of a
 * visual price; '' when it has neither (an expression's are not known
 * here).
 */
export function priceExtras(t: Translate, def: Pick<PriceDef, 'mode' | 'config'>): string {
  const s = summarize(def.mode, def.config)
  if (s.kind === 'expression') return ''
  const tiers = s.tiers > 1 ? t('prices.summary.tiers', { n: s.tiers }) : ''
  return [tiers, s.rules ? t('prices.summary.rules', { n: s.rules }) : ''].filter(Boolean).join(' · ')
}
