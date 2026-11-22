import { describe, expect, it } from 'vitest'
import type { CustomPrice, EffectiveRate, PriceDef, PricingQuote } from '@/api/pricing'
import { priceLevels, rateLevels } from './quoteLevels'
import { singleTier } from '@/views/prices/priceExpr'

const base: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: '' }
const fixed: CustomPrice = { mode: 'visual', config: singleTier({ p: 1, c: 8 }) }
const rate = (over: Partial<EffectiveRate>): EffectiveRate => ({ value: '1.2', source: 'group', group_default: '1.2', ...over })
const levels = (over: Partial<PricingQuote['rate_levels']>): PricingQuote['rate_levels'] => ({ segment_model: null, segment: null, group_model: null, group: '1.2', ...over })

const states = (levels: Array<{ key: string; state: string }>) => Object.fromEntries(levels.map((l) => [l.key, l.state]))

describe('quote levels', () => {
  it('marks the model price level the quote took and greys the one under it', () => {
    const top = priceLevels({ price_levels: { group: fixed }, base, price_source: 'group' })
    expect(top.map((l) => l.key)).toEqual(['group', 'global'])
    expect(states(top)).toEqual({ group: 'applies', global: 'shadowed' })
    expect(top[0].value).toBe(fixed)
    expect(states(priceLevels({ price_levels: { group: null }, base, price_source: 'global' }))).toEqual({ group: 'unset', global: 'applies' })
  })

  it('leaves every model price level unset for a model without an original price', () => {
    expect(states(priceLevels({ price_levels: { group: null }, base: null, price_source: 'global' }))).toEqual({ group: 'unset', global: 'unset' })
  })

  it('takes the most specific rate set and greys the ones under it', () => {
    const q = { rate: rate({ value: '0.85', source: 'segment_model' }), rate_levels: levels({ segment_model: '0.85', segment: '0.7', group_model: null }) }
    expect(rateLevels(q).map((l) => [l.key, l.value, l.state])).toEqual([
      ['segment_model', 0.85, 'applies'],
      ['segment', 0.7, 'shadowed'],
      ['group_model', null, 'unset'],
      ['group', 1.2, 'shadowed']
    ])
  })

  it('takes the model rate of the group over its base rate', () => {
    const q = { rate: rate({ value: '0.9', source: 'group_model' }), rate_levels: levels({ group_model: '0.9' }) }
    expect(states(rateLevels(q))).toEqual({ segment_model: 'unset', segment: 'unset', group_model: 'applies', group: 'shadowed' })
  })

  it('falls back to the base rate of the group', () => {
    expect(states(rateLevels({ rate: rate({}), rate_levels: levels({}) }))).toEqual({ segment_model: 'unset', segment: 'unset', group_model: 'unset', group: 'applies' })
  })
})
