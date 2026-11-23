import { describe, expect, it } from 'vitest'
import { singleTier } from '@/views/prices/priceExpr'
import type { PriceDef } from '@/api/pricing'
import { addModelOptions } from './addModelOptions'

const price = (p: number, c: number): PriceDef => ({ mode: 'visual', config: singleTier({ p, c }), expression: '' })
const t = (key: string) => key.replace('pricing.add.source.', '').replace('pricing.price.', '')

describe('what "Add model" offers', () => {
  it('lists the accounts’ models, then the global prices, then greyed what is listed, each with its source and price', () => {
    const offered = [{ model: 'gpt-5', price: price(1, 8) }, { model: 'o3', price: null }, { model: 'gpt-4o', price: price(3, 15) }]
    const catalog = [{ model: 'o3', price: price(2, 8) }, { model: 'claude', price: price(3, 15) }, { model: 'gpt-4o', price: price(3, 15) }]
    expect(addModelOptions(t, offered, catalog, new Set(['gpt-4o']))).toEqual([
      { value: 'gpt-5', label: 'gpt-5', tag: 'offered', hint: '$1 / $8' },
      { value: 'o3', label: 'o3', tag: 'offered', hint: 'none' },
      { value: 'claude', label: 'claude', tag: 'catalog', hint: '$3 / $15' },
      { value: 'gpt-4o', label: 'gpt-4o', tag: 'added', disabled: true }
    ])
  })
})
