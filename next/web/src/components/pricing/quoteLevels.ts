// The two chains of a quote (POST /pricing/quote) level by level, top first,
// for the price simulator: what each level is set to and which one applies.
import type { CustomPrice, PriceDef, PriceSource, PricingQuote, RateSource } from '@/api/pricing'
import { num } from '@/views/prices/priceExpr'

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
export function priceLevels(q: Pick<PricingQuote, 'price_levels' | 'base' | 'price_source'>): QuoteLevel<PriceSource, CustomPrice | PriceDef>[] {
  return resolve<PriceSource, CustomPrice | PriceDef>(
    [
      ['group', q.price_levels.group],
      ['global', q.base]
    ],
    q.price_source
  )
}

/** Rate, the most specific first: the user group's for the model, the user group's, the group's for the model, the group's base rate. One applies. */
export function rateLevels(q: Pick<PricingQuote, 'rate' | 'rate_levels'>): QuoteLevel<RateSource, number>[] {
  const levels = (['segment_model', 'segment', 'group_model', 'group'] as const).map((k): [RateSource, number | null] => [k, q.rate_levels[k] === null ? null : num(q.rate_levels[k], 1)])
  return resolve<RateSource, number>(levels, q.rate.source)
}
