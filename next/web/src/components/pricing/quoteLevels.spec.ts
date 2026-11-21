import { describe, expect, it } from 'vitest'
import type { EffectiveRate, PriceDef, PriceRule } from '@/api/pricing'
import { priceLevels, rateLevels } from './quoteLevels'
import { singleTier } from '@/views/prices/priceExpr'

const base: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: '' }
const half: PriceRule = { kind: 'multiplier', multiplier: '0.5' }
const fixed: PriceRule = { kind: 'custom', mode: 'visual', config: singleTier({ p: 1, c: 8 }) }
const rate = (over: Partial<EffectiveRate>): EffectiveRate => ({ value: '1.2', source: 'group', group_default: '1.2', discount: '1', ...over })

const states = (levels: Array<{ key: string; state: string }>) => Object.fromEntries(levels.map((l) => [l.key, l.state]))

describe('quote levels', () => {
  it('marks the list price level the quote took and greys the ones under it', () => {
    const top = priceLevels({ price_levels: { segment: fixed, group: half }, base, price_source: 'segment' })
    expect(top.map((l) => l.key)).toEqual(['segment', 'group', 'global'])
    expect(states(top)).toEqual({ segment: 'applies', group: 'shadowed', global: 'shadowed' })
    expect(top[0].value).toBe(fixed)

    expect(states(priceLevels({ price_levels: { segment: null, group: half }, base, price_source: 'group' }))).toEqual({ segment: 'unset', group: 'applies', global: 'shadowed' })
    expect(states(priceLevels({ price_levels: { segment: null, group: null }, base, price_source: 'global' }))).toEqual({ segment: 'unset', group: 'unset', global: 'applies' })
  })

  it('leaves every list price level unset for a model without a global price', () => {
    expect(states(priceLevels({ price_levels: { segment: null, group: null }, base: null, price_source: 'global' }))).toEqual({ segment: 'unset', group: 'unset', global: 'unset' })
  })

  it('replaces the rate by the override of the segment', () => {
    const levels = rateLevels({ rate: rate({ value: '0.8', source: 'segment', discount: '0.9' }), rate_override: '0.8' })
    expect(levels.map((l) => [l.key, l.value, l.state])).toEqual([
      ['segment', 0.8, 'applies'],
      ['discount', 1.08, 'shadowed'],
      ['group', 1.2, 'shadowed']
    ])
  })

  it('multiplies the default rate of the group by the discount of the segment', () => {
    const levels = rateLevels({ rate: rate({ value: '1.08', source: 'discount', discount: '0.9' }), rate_override: null })
    expect(levels.map((l) => [l.key, l.value, l.state])).toEqual([
      ['segment', null, 'unset'],
      ['discount', 1.08, 'applies'],
      ['group', 1.2, 'shadowed']
    ])
  })

  it('falls back to the default rate of the group; a discount of 1 is no discount', () => {
    expect(states(rateLevels({ rate: rate({}), rate_override: null }))).toEqual({ segment: 'unset', discount: 'unset', group: 'applies' })
  })
})
