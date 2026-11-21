// How a price and a price rule read in the pricing views. Labels live under
// pricing.price and pricing.rule.
import type { PriceDef, PriceRule } from '@/api/pricing'
import { priceBrief, priceCells, rateText, validRate, type Translate as T } from './pricingFormat'

/**
 * A price in a table cell: "$3 / $15", "$0.02 per request", that an
 * expression bills it ("×1.5" after it under a multiplier rule), or that
 * there is none. The numbers are those of priceCells: a multiplier is in them.
 */
export function priceLabel(t: T, def: PriceDef | null | undefined): string {
  if (!def) return t('pricing.price.none')
  const cells = priceCells(def)
  if (cells.opaque) return cells.multiplier === undefined ? t('pricing.price.expression') : t('pricing.price.expressionTimes', { rate: rateText(cells.multiplier) })
  const brief = priceBrief(def)
  return cells.perRequest === undefined ? brief : t('pricing.price.perRequest', { price: brief })
}

/** "Original price × 0.8" / "Custom": what kind of group price a rule is; '' without a rule. */
export function ruleText(t: T, rule: PriceRule | null | undefined): string {
  if (!rule) return ''
  if (rule.kind === 'custom') return t('pricing.rule.customText')
  // A multiplier still being typed has no rate to show.
  return t('pricing.rule.multiplierText', { rate: validRate(rule.multiplier) ? Number(rule.multiplier) : '—' })
}
