// The pricing table of a group while it is edited: per model its model price
// (a price of its own, standing alone, or the original price) and its model rate (or the
// group's base rate). What a price starts from, whether one may be saved, how
// a row reads under its unsaved changes and what the request sends.
import type { CatalogPrice, CustomPrice, ModelPricePatch, ModelPriceRow, ModelRatePatch, PriceDef } from '@/api/pricing'
import { asVisualConfig, blankPrices, compactVisualConfig, visualExpr } from '@/views/prices/priceExpr'
import { checkPrice, type LocalIssue } from '@/views/prices/priceCheck'
import { priceDraftOf, pricePayload, type PriceDraft } from '@/views/prices/priceDraft'
import { listPriceOf, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The model price of a price as its editor holds it (PriceDefEditor). */
export function customPriceOf(draft: PriceDraft): CustomPrice {
  const p = pricePayload(draft)
  if (p.mode === 'expression') return { mode: 'expression', expression: p.expression }
  return { mode: 'visual', config: asVisualConfig(p.config), expression: p.expression }
}

/** A model price as its editor takes it. */
export const priceDraftOfCustom = (custom: CustomPrice): PriceDraft => priceDraftOf({ mode: custom.mode, config: custom.config ?? null, expression: custom.expression })

/**
 * A saved model price as the editor sends it back (editableVisualConfig): a
 * token tier without p or c reads 0, as the server bills it. Drafts are
 * compared with it and it is checked like them, so a saved price is neither
 * changed nor refused by reading it.
 */
export const savedPrice = (custom: CustomPrice | null): CustomPrice | null => custom && customPriceOf(priceDraftOfCustom(custom))

/** A price with no value yet: one token tier, its input and output prices blank (not 0: it cannot be saved until they are typed). */
const blankDraft = (): PriceDraft => ({ mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token', p: null, c: null }], rules: [] }, expression: '' })

/** The model price a model starts from, as its editor holds it: its original price as it is, or blank without one. */
export const startDraft = (base: PriceDef | null): PriceDraft => (base ? priceDraftOf(base) : blankDraft())

/** Why a model price cannot be saved yet (keys under prices.local); none for no price. */
export function priceIssues(custom: CustomPrice | null): LocalIssue[] {
  return custom ? checkPrice({ mode: custom.mode, config: custom.config ?? {}, expression: custom.expression ?? '' }) : []
}

export const validPrice = (custom: CustomPrice | null) => priceIssues(custom).length === 0

/** What a model price bills by: its expression as written, or the one its visual config generates (every tier and markup rule in it) with the prices still blank, which are not 0. */
function billedBy(custom: CustomPrice): string {
  if (custom.mode === 'expression') return custom.expression.trim()
  const config = compactVisualConfig(asVisualConfig(custom.config))
  return `${visualExpr(config)} ${JSON.stringify(config.tiers.map(blankPrices))}`
}

/** Model prices that price alike: nothing to save between them. */
export function samePrice(a: CustomPrice | null, b: CustomPrice | null): boolean {
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
  const invalid = !validPrice(price) || (rate !== null && !validRate(rate))
  return { ...row, price, rate, saved: row, list_price: listPriceOf(row.base, price), dirty: !!draft, invalid: !!draft && invalid }
}

/** The drafts with `change` added to what `row` changes already; a field that reads like the saved row again is none, and a row without any is no draft. */
export function mergeRowDraft(drafts: RowDrafts, row: Pick<DraftedRow, 'model' | 'saved'>, change: RowDraft): RowDrafts {
  const merged = { ...drafts.get(row.model), ...change }
  const draft: RowDraft = {}
  if (merged.price !== undefined && !samePrice(merged.price, row.saved.price)) draft.price = merged.price
  if (merged.rate !== undefined && !sameRate(merged.rate, row.saved.rate)) draft.rate = merged.rate
  return withDraft(drafts, row.model, Object.keys(draft).length ? draft : undefined)
}

/** A model price as the request takes it: a visual price by its config, an expression price by its expression. */
function customPayload(custom: CustomPrice | null): CustomPrice | null {
  if (!custom) return null
  return custom.mode === 'expression' ? { mode: 'expression', expression: custom.expression } : { mode: 'visual', config: custom.config }
}

/** What the request sends of the drafts: the changed model prices (`model_prices`). */
export const modelPricePatches = (drafts: RowDrafts): ModelPricePatch[] => [...drafts].filter(([, d]) => d.price !== undefined).map(([model, d]) => ({ model, rule: customPayload(d.price ?? null) }))

/** What the request sends of the drafts: the changed model rates (`model_rates`). */
export const groupModelRatePatches = (drafts: RowDrafts): ModelRatePatch[] => [...drafts].filter(([, d]) => d.rate !== undefined).map(([model, d]) => ({ model, rate: d.rate ?? null }))
