// Price rules while they are edited: the rule a mode starts from, whether a
// rule may be saved, and the unsaved rules of a model price table: what its
// rows show meanwhile and what saving sends.
import type { ListedPriceRule, ModelPriceRow, PriceDef, PriceRule, PriceRulePatch, PriceSource } from '@/api/pricing'
import { PRICE_KEYS, isSet, num, requestTier, simpleTier, singleTier, takesCustomPrice, type TierUnit } from '@/views/prices/priceExpr'
import { applyRule, priceCells, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The rule of a row a table edits: the group's own, or that of the segment asked for. */
export type RuleScope = 'group' | 'segment'

export const ruleOf = (row: ModelPriceRow, scope: RuleScope): ListedPriceRule | null => (scope === 'segment' ? row.segment_rule : row.group_rule) ?? null

export type CustomRule = Extract<PriceRule, { kind: 'custom' }>

/** How a custom rule prices: by the request when its one tier does, by tokens otherwise. */
export const customUnit = (rule: CustomRule): TierUnit => (simpleTier(rule.config)?.unit === 'request' ? 'request' : 'token')

/**
 * The prices a custom rule sets, in the order shown: the first `required` of
 * them must be filled. The server checks a saved rule by the same table
 * (CUSTOM_PRICES in mock/pricing.ts stands in for it): keep the two alike.
 */
export function customFields(unit: TierUnit): { keys: readonly string[]; required: number } {
  return unit === 'request' ? { keys: ['price'], required: 1 } : { keys: PRICE_KEYS, required: 2 }
}

const IMAGE_KEYS: readonly string[] = ['img', 'img_o']

/** The prices of a custom rule its editor shows: image tokens only when the global price prices them apart, or the rule does. */
export function shownFields(rule: CustomRule, base: PriceDef | null): string[] {
  const tier = base ? simpleTier(base.config) : null
  const prices = customPrices(rule)
  const images = IMAGE_KEYS.some((k) => (tier !== null && k in tier) || prices[k] !== null)
  return customFields(customUnit(rule)).keys.filter((k) => images || !IMAGE_KEYS.includes(k))
}

/** The prices of a custom rule as one row: those of its one tier. */
export function customPrices(rule: CustomRule): Record<string, number | null> {
  const row: Record<string, unknown> = { ...simpleTier(rule.config) }
  return Object.fromEntries(customFields(customUnit(rule)).keys.map((k) => [k, (row[k] as number | null | undefined) ?? null]))
}

/** The custom rule of a unit with these prices: a visual price of one request or token tier. */
export function customRule(unit: TierUnit, prices: Record<string, number | null | undefined>): CustomRule {
  return { kind: 'custom', mode: 'visual', config: unit === 'request' ? requestTier(prices.price ?? null) : singleTier(prices) }
}

/** Whether a global price takes a custom price (takesCustomPrice). */
export const takesCustom = (base: PriceDef | null) => !!base && takesCustomPrice(base.mode, base.config)

/** The rule a mode starts from: ×1, or the global price as a price of its own. A price that takes no custom price takes a multiplier only. */
export function startRule(kind: PriceRule['kind'], base: PriceDef): PriceRule {
  if (kind === 'multiplier' || !takesCustom(base)) return { kind: 'multiplier', multiplier: '1' }
  const tier = simpleTier(base.config)
  if (tier?.unit === 'request') return customRule('request', { price: priceCells(base).perRequest })
  return customRule('token', Object.fromEntries(PRICE_KEYS.map((k) => [k, isSet(tier?.[k]) ? num(tier?.[k]) : null])))
}

const isPrice = (v: unknown) => typeof v === 'number' && Number.isFinite(v) && v >= 0

/** Whether a rule may be saved: a multiplier is a rate; a custom price has its required prices and none negative. */
export function validRule(rule: PriceRule | null): boolean {
  if (!rule) return true
  if (rule.kind === 'multiplier') return validRate(rule.multiplier)
  const prices = customPrices(rule)
  const { keys, required } = customFields(customUnit(rule))
  return keys.every((k, i) => isPrice(prices[k]) || (i >= required && prices[k] === null))
}

/** Rules that price alike: nothing to save between them. */
export function sameRule(a: PriceRule | null, b: PriceRule | null): boolean {
  if (!a || !b) return a === b
  if (a.kind === 'multiplier') return b.kind === 'multiplier' && sameRate(a.multiplier, b.multiplier)
  if (b.kind !== 'custom' || customUnit(a) !== customUnit(b)) return false
  const [x, y] = [customPrices(a), customPrices(b)]
  return customFields(customUnit(a)).keys.every((k) => x[k] === y[k])
}

/**
 * A row as it reads with `rule` in `scope`: the list price and its source
 * follow the first rule in effect, as the server resolves them. An inactive
 * rule is skipped, and without a global price no rule applies.
 */
export function withRule(row: ModelPriceRow, scope: RuleScope, rule: PriceRule | null): ModelPriceRow {
  const next: ModelPriceRow = scope === 'segment' ? { ...row, segment_rule: rule } : { ...row, group_rule: rule }
  const levels: Array<[PriceSource, ListedPriceRule | null | undefined]> = row.base ? [['segment', next.segment_rule], ['group', next.group_rule]] : []
  const [source, applied] = levels.find(([, r]) => r && !r.inactive) ?? ['global', null]
  return { ...next, list_price: applyRule(row.base, applied ?? null), source }
}

/** The unsaved rules of a table by model; null removes the saved one. */
export type RuleDrafts = ReadonlyMap<string, PriceRule | null>

/** A row of the table: the saved row under its draft. */
export interface DraftedRow extends ModelPriceRow {
  /** The rule on the server, which a draft is compared with. */
  saved: PriceRule | null
  dirty: boolean
}

export function draftedRow(row: ModelPriceRow, scope: RuleScope, drafts: RuleDrafts): DraftedRow {
  const saved = ruleOf(row, scope)
  if (!drafts.has(row.model)) return { ...row, saved, dirty: false }
  return { ...withRule(row, scope, drafts.get(row.model) ?? null), saved, dirty: true }
}

/** The drafts with `rule` as the rule of `row`; a rule that prices like the saved one is no draft. */
export function setRuleDraft(drafts: RuleDrafts, row: Pick<DraftedRow, 'model' | 'saved'>, rule: PriceRule | null): RuleDrafts {
  return withDraft(drafts, row.model, sameRule(rule, row.saved) ? undefined : rule)
}

/** What saving the drafts sends; `segmentId` null = the group's own rules. */
export function rulePatches(drafts: RuleDrafts, segmentId: number | null): PriceRulePatch[] {
  return [...drafts].map(([model, rule]) => ({ segment_id: segmentId, model, rule }))
}
