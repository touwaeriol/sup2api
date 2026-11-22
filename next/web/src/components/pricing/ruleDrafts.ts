// The pricing table of a group while it is edited: per model its model price
// (a price of its own, or the original price) and its model rate (or the
// group's base rate). What a price starts from, whether one may be saved, how
// a row reads under its unsaved changes and what the request sends.
import type { CatalogPrice, CustomPrice, ModelPricePatch, ModelPriceRow, ModelRatePatch, PriceDef } from '@/api/pricing'
import { asVisualConfig, compactVisualConfig, visualExpr } from '@/views/prices/priceExpr'
import { checkPrice, type LocalIssue } from '@/views/prices/priceCheck'
import { priceDraftOf, pricePayload, type PriceDraft } from '@/views/prices/priceDraft'
import { applyRule, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The model price of a price as its editor holds it (PriceDefEditor). */
export function customRuleOf(draft: PriceDraft): CustomPrice {
  const p = pricePayload(draft)
  if (p.mode === 'expression') return { kind: 'custom', mode: 'expression', expression: p.expression }
  return { kind: 'custom', mode: 'visual', config: asVisualConfig(p.config), expression: p.expression }
}

/** A model price as its editor takes it. */
export const priceDraftOfRule = (rule: CustomPrice): PriceDraft => priceDraftOf({ mode: rule.mode, config: rule.config ?? null, expression: rule.expression })

/** The model price a model starts from: its original price as it is, written as a price of its own. */
export const startRule = (base: PriceDef): CustomPrice => customRuleOf(priceDraftOf(base))

/** Why a model price cannot be saved yet (keys under prices.local); none for no price. */
export function ruleIssues(rule: CustomPrice | null): LocalIssue[] {
  return rule ? checkPrice({ mode: rule.mode, config: rule.config ?? {}, expression: rule.expression ?? '' }) : []
}

export const validRule = (rule: CustomPrice | null) => ruleIssues(rule).length === 0

/** What a model price bills by: its expression as written, or the one its visual config generates (every tier and markup rule in it). */
function billedBy(rule: CustomPrice): string {
  return rule.mode === 'expression' ? rule.expression.trim() : visualExpr(compactVisualConfig(asVisualConfig(rule.config)))
}

/** Model prices that price alike: nothing to save between them. */
export function sameRule(a: CustomPrice | null, b: CustomPrice | null): boolean {
  if (!a || !b) return a === b
  return a.mode === b.mode && billedBy(a) === billedBy(b)
}

/** A row for a model added before the server lists it: not provided, its original price from the catalog, nothing set. */
export const catalogRow = (model: string, base: PriceDef | null): ModelPriceRow => ({ model, offered: false, base, price: null, rate: null })

/** The rows of the catalog by model. */
export const catalogPrices = (catalog: readonly CatalogPrice[]) => new Map(catalog.map((c) => [c.model, c.price]))

/** What a row changes; a field left out is as saved, null removes what is saved. */
export interface RowDraft {
  price?: CustomPrice | null
  rate?: string | null
}

/** The unsaved changes of the table by model. */
export type RowDrafts = ReadonlyMap<string, RowDraft>

/** A row of the table: the saved row under its draft. */
export interface DraftedRow extends ModelPriceRow {
  /** The row on the server, which a draft is compared with. */
  saved: ModelPriceRow
  /** The price that applies: the model price, or the original price. */
  list_price: PriceDef | null
  dirty: boolean
  /** The draft cannot be saved yet: a price or a rate is not filled in right. */
  invalid: boolean
}

/** A row as it reads under its draft. */
export function draftedRow(row: ModelPriceRow, draft?: RowDraft): DraftedRow {
  const price = draft?.price === undefined ? row.price : draft.price
  const rate = draft?.rate === undefined ? row.rate : draft.rate
  const invalid = !validRule(price) || (rate !== null && !validRate(rate))
  return { ...row, price, rate, saved: row, list_price: applyRule(row.base, price), dirty: !!draft, invalid: !!draft && invalid }
}

/** The drafts with `change` added to what `row` changes already; a field that reads like the saved row again is none, and a row without any is no draft. */
export function mergeRowDraft(drafts: RowDrafts, row: Pick<DraftedRow, 'model' | 'saved'>, change: RowDraft): RowDrafts {
  const merged = { ...drafts.get(row.model), ...change }
  const draft: RowDraft = {}
  if (merged.price !== undefined && !sameRule(merged.price, row.saved.price)) draft.price = merged.price
  if (merged.rate !== undefined && !sameRate(merged.rate, row.saved.rate)) draft.rate = merged.rate
  return withDraft(drafts, row.model, Object.keys(draft).length ? draft : undefined)
}

/** A model price as the request takes it: a visual price by its config, an expression price by its expression. */
function rulePayload(rule: CustomPrice | null): CustomPrice | null {
  if (!rule) return null
  return rule.mode === 'expression' ? { kind: 'custom', mode: 'expression', expression: rule.expression } : { kind: 'custom', mode: 'visual', config: rule.config }
}

/** What the request sends of the drafts: the changed model prices and model rates, each list only when it has items. */
export function pricingPatches(drafts: RowDrafts): { model_prices?: ModelPricePatch[]; model_rates?: ModelRatePatch[] } {
  const entries = [...drafts]
  const model_prices = entries.filter(([, d]) => d.price !== undefined).map(([model, d]) => ({ model, rule: rulePayload(d.price ?? null) }))
  const model_rates = entries.filter(([, d]) => d.rate !== undefined).map(([model, d]) => ({ model, rate: d.rate ?? null }))
  return { ...(model_prices.length ? { model_prices } : {}), ...(model_rates.length ? { model_rates } : {}) }
}
