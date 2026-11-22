// The model prices of a group while they are edited: the rule a mode starts
// from, whether a rule may be saved, and the unsaved rules of the table: what
// its rows show meanwhile and what saving sends.
import type { CatalogPrice, ModelPriceRow, PriceDef, PriceRule, PriceRulePatch } from '@/api/pricing'
import { asVisualConfig, compactVisualConfig, visualExpr } from '@/views/prices/priceExpr'
import { checkPrice, type LocalIssue } from '@/views/prices/priceCheck'
import { priceDraftOf, pricePayload, type PriceDraft } from '@/views/prices/priceDraft'
import { applyRule, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

export type CustomRule = Extract<PriceRule, { kind: 'custom' }>

/** The custom rule of a price as its editor holds it (PriceDefEditor). */
export function customRuleOf(draft: PriceDraft): CustomRule {
  const p = pricePayload(draft)
  if (p.mode === 'expression') return { kind: 'custom', mode: 'expression', expression: p.expression }
  return { kind: 'custom', mode: 'visual', config: asVisualConfig(p.config), expression: p.expression }
}

/** A custom rule as its editor takes it. */
export const priceDraftOfRule = (rule: CustomRule): PriceDraft => priceDraftOf({ mode: rule.mode, config: rule.config ?? null, expression: rule.expression })

/** The rule a mode starts from: ×1, or the global price as it is, written as a price of its own. */
export function startRule(kind: PriceRule['kind'], base: PriceDef): PriceRule {
  return kind === 'multiplier' ? { kind: 'multiplier', multiplier: '1' } : customRuleOf(priceDraftOf(base))
}

/** Why a rule cannot be saved yet (keys under pricing.rule and prices.local); none for no rule. */
export function ruleIssues(rule: PriceRule | null): LocalIssue[] {
  if (!rule) return []
  if (rule.kind === 'multiplier') return validRate(rule.multiplier) ? [] : [{ key: 'pricing.rate.invalid' }]
  return checkPrice({ mode: rule.mode, config: rule.config ?? {}, expression: rule.expression ?? '' })
}

export const validRule = (rule: PriceRule | null) => ruleIssues(rule).length === 0

/** What a custom rule bills by: its expression as written, or the one its visual config generates (every tier and markup rule in it). */
function billedBy(rule: CustomRule): string {
  return rule.mode === 'expression' ? rule.expression.trim() : visualExpr(compactVisualConfig(asVisualConfig(rule.config)))
}

/** Rules that price alike: nothing to save between them. */
export function sameRule(a: PriceRule | null, b: PriceRule | null): boolean {
  if (!a || !b) return a === b
  if (a.kind === 'multiplier') return b.kind === 'multiplier' && sameRate(a.multiplier, b.multiplier)
  return b.kind === 'custom' && a.mode === b.mode && billedBy(a) === billedBy(b)
}

/** A row for a model the server does not list yet: its global price from the catalog, no rule. */
export function catalogRow(model: string, base: PriceDef | null, offered: boolean): ModelPriceRow {
  return { model, offered, base, group_rule: null, list_price: base, source: 'global' }
}

/** The rows of the catalog by model. */
export const catalogPrices = (catalog: readonly CatalogPrice[]) => new Map(catalog.map((c) => [c.model, c.price]))

/** The unsaved rules of a table by model; null removes the saved one. */
export type RuleDrafts = ReadonlyMap<string, PriceRule | null>

/** A row of the table: the saved row under its draft. */
export interface DraftedRow extends ModelPriceRow {
  /** The rule on the server, which a draft is compared with. */
  saved: PriceRule | null
  dirty: boolean
}

/** A row as it reads under its draft: the list price and its source follow the rule, as the server resolves them (applyRule). */
export function draftedRow(row: ModelPriceRow, drafts: RuleDrafts): DraftedRow {
  const saved = row.group_rule
  if (!drafts.has(row.model)) return { ...row, saved, dirty: false }
  const rule = drafts.get(row.model) ?? null
  return { ...row, group_rule: rule, list_price: applyRule(row.base, rule), source: rule && row.base ? 'group' : 'global', saved, dirty: true }
}

/** The drafts with `rule` as the rule of `row`; a rule that prices like the saved one is no draft. */
export function setRuleDraft(drafts: RuleDrafts, row: Pick<DraftedRow, 'model' | 'saved'>, rule: PriceRule | null): RuleDrafts {
  return withDraft(drafts, row.model, sameRule(rule, row.saved) ? undefined : rule)
}

/** A rule as PUT takes it: a visual price by its config, an expression price by its expression. */
function rulePayload(rule: PriceRule | null): PriceRule | null {
  if (rule?.kind !== 'custom') return rule
  return rule.mode === 'expression' ? { kind: 'custom', mode: 'expression', expression: rule.expression } : { kind: 'custom', mode: 'visual', config: rule.config }
}

/** What saving the drafts sends. */
export function rulePatches(drafts: RuleDrafts): PriceRulePatch[] {
  return [...drafts].map(([model, rule]) => ({ model, rule: rulePayload(rule) }))
}
