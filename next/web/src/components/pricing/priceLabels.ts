// How a price reads in the pricing views. Labels live under pricing.price.
import type { PriceDef } from '@/api/pricing'
import { priceBrief, priceCells, rateText, validRate, type Translate as T } from './pricingFormat'

/**
 * A price in a table cell, times `factor` (what is paid at a rate): "$3 /
 * $15", "$0.02 per request", that an expression bills it ("×1.5" after it
 * times a rate other than 1), or that there is none.
 */
export function priceLabel(t: T, def: PriceDef | null | undefined, factor = 1): string {
  if (!def) return t('pricing.price.none')
  const cells = priceCells(def, factor)
  if (cells.opaque) return cells.factor === undefined ? t('pricing.price.expression') : t('pricing.price.expressionTimes', { rate: rateText(cells.factor) })
  const brief = priceBrief(def, factor)
  return cells.perRequest === undefined ? brief : t('pricing.price.perRequest', { price: brief })
}

/** What is paid at list price `list` and `rate`: none without a price, '—' while the rate is not one yet. */
export function paidLabel(t: T, list: PriceDef | null, rate: string): string {
  if (!list) return priceLabel(t, null)
  return validRate(rate) ? priceLabel(t, list, Number(rate)) : '—'
}
