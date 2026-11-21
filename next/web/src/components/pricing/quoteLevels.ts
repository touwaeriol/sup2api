// The two chains of a quote (POST /pricing/quote) level by level, top first,
// for the price simulator: what each level is set to and which one applies.
import type { PriceDef, PriceRule, PriceSource, PricingQuote, RateSource } from '@/api/pricing'
import { num } from '@/views/prices/priceExpr'
import { hasDiscount, inheritedRate } from './pricingFormat'

/** applies: the level the quote took; shadowed: set, but a level above it applies; unset: nothing is set there. */
export type LevelState = 'applies' | 'shadowed' | 'unset'

export interface QuoteLevel<K extends string, V> {
  key: K
  /** As configured; null = not set. */
  value: V | null
  state: LevelState
}

function resolve<K extends string, V>(levels: Array<[K, V | null]>, source: K): QuoteLevel<K, V>[] {
  return levels.map(([key, value]) => ({ key, value, state: value === null ? 'unset' : key === source ? 'applies' : 'shadowed' }))
}

/** Model price: the group's price, the original price. */
export function priceLevels(q: Pick<PricingQuote, 'price_levels' | 'base' | 'price_source'>): QuoteLevel<PriceSource, PriceRule | PriceDef>[] {
  return resolve<PriceSource, PriceRule | PriceDef>(
    [
      ['group', q.price_levels.group],
      ['global', q.base]
    ],
    q.price_source
  )
}

/** Rate: the segment's override, the group's default times the segment's discount (set when there is a discount), the group's default. */
export function rateLevels(q: Pick<PricingQuote, 'rate' | 'rate_override'>): QuoteLevel<RateSource, number>[] {
  return resolve<RateSource, number>(
    [
      ['segment', q.rate_override === null ? null : num(q.rate_override)],
      ['discount', hasDiscount(q.rate) ? inheritedRate(q.rate) : null],
      ['group', num(q.rate.group_default, 1)]
    ],
    q.rate.source
  )
}
