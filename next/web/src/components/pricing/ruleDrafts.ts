// Price rules while they are edited: the rule a mode starts from, whether a
// rule may be saved, and the unsaved rules of a model price table: what its
// rows show meanwhile and what saving sends.
import type { ListedPriceRule, ModelPriceRow, PriceDef, PriceRule, PriceRulePatch, PriceSource } from '@/api/pricing'
import { TOKEN_VARS, singleTier, takesCustomPrice } from '@/views/prices/priceExpr'
import { applyRule, priceCells, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The rule of a row a table edits: the group's own, or that of the segment asked for. */
export type RuleScope = 'group' | 'segment'

export const ruleOf = (row: ModelPriceRow, scope: RuleScope): ListedPriceRule | null => (scope === 'segment' ? row.segment_rule : row.group_rule) ?? null

type CustomRule = Extract<PriceRule, { kind: 'custom' }>
type CustomMode = CustomRule['mode']

/**
 * The prices a custom rule sets, in the order shown: the first `required` of
 * them must be filled. The server checks a saved rule by the same table
 * (CUSTOM_PRICES in mock/pricing.ts stands in for it): keep the two alike.
 */
export function customFields(mode: CustomMode): { keys: readonly string[]; required: number } {
  return mode === 'per_request' ? { keys: ['price'], required: 1 } : { keys: TOKEN_VARS, required: 2 }
}

/** The prices of a custom rule as one row: its per-request price, or those of its one token tier. */
export function customPrices(rule: CustomRule): Record<string, number | null> {
  const row: Record<string, unknown> = rule.mode === 'per_request' ? rule.config : { ...rule.config.tiers[0] }
  return Object.fromEntries(customFields(rule.mode).keys.map((k) => [k, (row[k] as number | null | undefined) ?? null]))
}

/** The custom rule of a mode with these prices: a per-token one is a single token tier. */
export function customRule(mode: CustomMode, prices: Record<string, number | null | undefined>): CustomRule {
  return mode === 'per_request' ? { kind: 'custom', mode, config: { price: prices.price ?? null } } : { kind: 'custom', mode, config: singleTier(prices) }
}

/** Whether a global price takes a custom price (takesCustomPrice). */
export const takesCustom = (base: PriceDef | null) => !!base && takesCustomPrice(base.mode, base.config)

/** The rule a mode starts from: ×1, or the global price as a price of its own. A price that takes no custom price takes a multiplier only. */
export function startRule(kind: PriceRule['kind'], base: PriceDef): PriceRule {
  if (kind === 'multiplier' || !takesCustom(base)) return { kind: 'multiplier', multiplier: '1' }
  const cells = priceCells(base)
  return customRule(base.mode as CustomMode, { ...cells.tokens, price: cells.perRequest })
}

const isPrice = (v: unknown) => typeof v === 'number' && Number.isFinite(v) && v >= 0

/** Whether a rule may be saved: a multiplier is a rate; a custom price has its required prices and none negative. */
export function validRule(rule: PriceRule | null): boolean {
  if (!rule) return true
  if (rule.kind === 'multiplier') return validRate(rule.multiplier)
  const prices = customPrices(rule)
  const { keys, required } = customFields(rule.mode)
  return keys.every((k, i) => isPrice(prices[k]) || (i >= required && prices[k] === null))
}

/** Rules that price alike: nothing to save between them. */
export function sameRule(a: PriceRule | null, b: PriceRule | null): boolean {
  if (!a || !b) return a === b
  if (a.kind === 'multiplier') return b.kind === 'multiplier' && sameRate(a.multiplier, b.multiplier)
  if (b.kind !== 'custom' || a.mode !== b.mode) return false
  const [x, y] = [customPrices(a), customPrices(b)]
  return customFields(a.mode).keys.every((k) => x[k] === y[k])
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
